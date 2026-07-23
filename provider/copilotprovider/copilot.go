// Copyright (c) Microsoft. All rights reserved.

package copilotprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

const (
	defaultName        = "GitHub Copilot Agent"
	defaultDescription = "An AI agent powered by GitHub Copilot"
)

// AgentConfig contains configuration for a GitHub Copilot-backed [agent.Agent].
type AgentConfig struct {
	agent.Config

	// SessionConfig configures Copilot sessions created or resumed by the agent.
	SessionConfig *copilot.SessionConfig

	// Instructions are appended to the Copilot system message for each run.
	Instructions string
}

type provider struct {
	client *copilot.Client
	cfg    AgentConfig
}

// NewAgent creates an agent backed by the GitHub Copilot SDK.
func NewAgent(cclient *copilot.Client, config AgentConfig) *agent.Agent {
	if cclient == nil {
		panic("copilotprovider: client cannot be nil")
	}
	if config.Name == "" {
		config.Name = defaultName
	}
	if config.Description == "" {
		config.Description = defaultDescription
	}
	if config.Instructions != "" {
		config.RunOptions = append(config.RunOptions, agent.WithInstructions(config.Instructions))
	}
	p := &provider{
		client: cclient,
		cfg:    config,
	}
	return agent.New(agent.ProviderConfig{
		ProviderName: "copilot",
		Run:          p.run,
	}, config.Config)
}

func (p *provider) run(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		if p.client == nil {
			yield(nil, errors.New("copilotprovider: client cannot be nil"))
			return
		}

		if err := p.client.Start(ctx); err != nil {
			yield(nil, err)
			return
		}

		events := newSessionEventQueue()
		eventHandler := func(event copilot.SessionEvent) {
			select {
			case <-ctx.Done():
				return
			default:
			}
			events.push(event)
		}

		frameworkSession, _ := agent.GetOption(options, agent.WithSession)
		isStreaming := p.streaming(options)
		copilotSession, err := p.openSession(ctx, frameworkSession, isStreaming, options)
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = copilotSession.Disconnect() }()
		unsubscribe := copilotSession.On(eventHandler)
		defer unsubscribe()

		if frameworkSession != nil && frameworkSession.ServiceID() == "" {
			frameworkSession.SetServiceID(copilotSession.SessionID)
		}

		messageOptions, cleanupAttachments, err := buildMessageOptions(messages)
		if err != nil {
			yield(nil, err)
			return
		}
		defer cleanupAttachments()

		if _, err := copilotSession.Send(ctx, messageOptions); err != nil {
			yield(nil, err)
			return
		}

		for {
			event, err := events.pop(ctx)
			if err != nil {
				yield(nil, err)
				return
			}
			update, done, eventErr := p.responseUpdateForSessionEvent(event, isStreaming)
			if update != nil {
				if !yield(update, nil) {
					return
				}
			}
			if eventErr != nil {
				yield(nil, eventErr)
				return
			}
			if done {
				return
			}
		}
	}
}

func (p *provider) responseUpdateForSessionEvent(event copilot.SessionEvent, isStreaming bool) (*agent.ResponseUpdate, bool, error) {
	switch data := event.Data.(type) {
	case *copilot.AssistantMessageDeltaData:
		return p.assistantMessageDeltaUpdate(event, data), false, nil
	case *copilot.AssistantMessageData:
		return p.assistantMessageUpdate(event, data, isStreaming), false, nil
	case *copilot.ToolExecutionStartData:
		return p.toolExecutionStartUpdate(event, data), false, nil
	case *copilot.ToolExecutionCompleteData:
		return p.toolExecutionCompleteUpdate(event, data), false, nil
	case *copilot.AssistantUsageData:
		return p.assistantUsageUpdate(event, data), false, nil
	case *copilot.SessionIdleData:
		return rawEventUpdate(event), true, nil
	case *copilot.SessionErrorData:
		return rawEventUpdate(event), true, fmt.Errorf("session error: %s", sessionErrorMessage(data))
	default:
		return rawEventUpdate(event), false, nil
	}
}

type sessionEventQueue struct {
	mu    sync.Mutex
	items []copilot.SessionEvent
	ready chan struct{}
}

func newSessionEventQueue() *sessionEventQueue {
	return &sessionEventQueue{ready: make(chan struct{}, 1)}
}

func (q *sessionEventQueue) push(event copilot.SessionEvent) {
	q.mu.Lock()
	wasEmpty := len(q.items) == 0
	q.items = append(q.items, event)
	q.mu.Unlock()
	if wasEmpty {
		select {
		case q.ready <- struct{}{}:
		default:
		}
	}
}

func (q *sessionEventQueue) pop(ctx context.Context) (copilot.SessionEvent, error) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			event := q.items[0]
			var zero copilot.SessionEvent
			q.items[0] = zero
			q.items = q.items[1:]
			if len(q.items) == 0 {
				q.items = nil
			}
			q.mu.Unlock()
			return event, nil
		}
		q.mu.Unlock()

		select {
		case <-q.ready:
		case <-ctx.Done():
			return copilot.SessionEvent{}, ctx.Err()
		}
	}
}

func (p *provider) streaming(options []agent.Option) bool {
	streaming := true
	if p.cfg.SessionConfig != nil && p.cfg.SessionConfig.Streaming != nil {
		streaming = *p.cfg.SessionConfig.Streaming
	}
	if stream, ok := agent.GetOption(options, agent.Stream); ok {
		streaming = stream
	}
	return streaming
}

func (p *provider) openSession(
	ctx context.Context,
	frameworkSession *agent.Session,
	streaming bool,
	options []agent.Option,
) (*copilot.Session, error) {
	if frameworkSession != nil && frameworkSession.ServiceID() != "" {
		cfg := p.resumeSessionConfig(streaming, options)
		return p.client.ResumeSession(ctx, frameworkSession.ServiceID(), &cfg)
	}
	cfg := p.sessionConfig(streaming, options)
	return p.client.CreateSession(ctx, &cfg)
}

func (p *provider) sessionConfig(streaming bool, options []agent.Option) copilot.SessionConfig {
	cfg := copySessionConfig(p.cfg.SessionConfig)
	cfg.Streaming = copilot.Bool(streaming)
	cfg.SystemMessage = systemMessageWithInstructions(cfg.SystemMessage, slices.Collect(agent.AllOptions(options, agent.WithInstructions)))
	cfg.Tools = append(cfg.Tools, copilotTools(options)...)
	return cfg
}

func (p *provider) resumeSessionConfig(streaming bool, options []agent.Option) copilot.ResumeSessionConfig {
	cfg := copyResumeSessionConfig(p.cfg.SessionConfig)
	cfg.Streaming = copilot.Bool(streaming)
	cfg.SystemMessage = systemMessageWithInstructions(cfg.SystemMessage, slices.Collect(agent.AllOptions(options, agent.WithInstructions)))
	cfg.Tools = append(cfg.Tools, copilotTools(options)...)
	return cfg
}

func copySessionConfig(source *copilot.SessionConfig) copilot.SessionConfig {
	if source == nil {
		return copilot.SessionConfig{Streaming: copilot.Bool(true)}
	}
	clone := *source
	clone.Tools = slices.Clone(source.Tools)
	clone.Streaming = copyBoolDefaultTrue(source.Streaming)
	return clone
}

func copyResumeSessionConfig(source *copilot.SessionConfig) copilot.ResumeSessionConfig {
	if source == nil {
		return copilot.ResumeSessionConfig{Streaming: copilot.Bool(true)}
	}
	return copilot.ResumeSessionConfig{
		Model:               source.Model,
		ReasoningEffort:     source.ReasoningEffort,
		Tools:               slices.Clone(source.Tools),
		SystemMessage:       source.SystemMessage,
		AvailableTools:      source.AvailableTools,
		ExcludedTools:       source.ExcludedTools,
		Provider:            source.Provider,
		OnPermissionRequest: source.OnPermissionRequest,
		OnUserInputRequest:  source.OnUserInputRequest,
		Hooks:               source.Hooks,
		WorkingDirectory:    source.WorkingDirectory,
		ConfigDirectory:     source.ConfigDirectory,
		MCPServers:          source.MCPServers,
		CustomAgents:        source.CustomAgents,
		SkillDirectories:    source.SkillDirectories,
		DisabledSkills:      source.DisabledSkills,
		InfiniteSessions:    source.InfiniteSessions,
		Streaming:           copyBoolDefaultTrue(source.Streaming),
	}
}

func copyBoolDefaultTrue(source *bool) *bool {
	if source == nil {
		return copilot.Bool(true)
	}
	value := *source
	return &value
}

func systemMessageWithInstructions(base *copilot.SystemMessageConfig, instructions []string) *copilot.SystemMessageConfig {
	instructions = compactInstructions(instructions)
	if len(instructions) == 0 {
		return base
	}
	joined := strings.Join(instructions, "\n")
	if base == nil {
		return &copilot.SystemMessageConfig{
			Mode:    "append",
			Content: joined,
		}
	}
	clone := *base
	if clone.Content == "" {
		clone.Content = joined
	} else {
		clone.Content += "\n" + joined
	}
	return &clone
}

func compactInstructions(instructions []string) []string {
	return slices.DeleteFunc(slices.Clone(instructions), func(instruction string) bool {
		return strings.TrimSpace(instruction) == ""
	})
}

func copilotTools(options []agent.Option) []copilot.Tool {
	var out []copilot.Tool
	for tl := range agent.AllOptions(options, agent.WithTool) {
		funcTool, ok := tl.(tool.FuncTool)
		if !ok {
			continue
		}
		converted, err := toCopilotTool(funcTool)
		if err != nil {
			converted = copilot.Tool{
				Name:        funcTool.Name(),
				Description: funcTool.Description(),
			}
		}
		out = append(out, converted)
	}
	return out
}

func toCopilotTool(funcTool tool.FuncTool) (copilot.Tool, error) {
	parameters, err := schemaMap(funcTool.Schema())
	if err != nil {
		return copilot.Tool{}, err
	}
	converted := copilot.Tool{
		Name:           funcTool.Name(),
		Description:    funcTool.Description(),
		Parameters:     parameters,
		SkipPermission: !approvalRequired(funcTool),
		Handler: func(invocation copilot.ToolInvocation) (copilot.ToolResult, error) {
			arguments, err := toolArguments(invocation.Arguments)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			ctx := invocation.TraceContext
			if ctx == nil {
				ctx = context.Background()
			}
			result, err := funcTool.Call(ctx, arguments)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			return toolResult(result)
		},
	}
	return converted, nil
}

func schemaMap(schema any) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	if schemaMap, ok := schema.(map[string]any); ok {
		return schemaMap, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool schema of type %T: %w", schema, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tool schema as JSON object: %w", err)
	}
	return out, nil
}

func approvalRequired(t tool.Tool) bool {
	approvalTool, ok := t.(tool.ApprovalRequiredTool)
	return ok && approvalTool.ApprovalRequired()
}

func toolArguments(arguments any) (string, error) {
	if arguments == nil {
		return "{}", nil
	}
	if raw, ok := arguments.(json.RawMessage); ok {
		if len(raw) == 0 || string(raw) == "null" {
			return "{}", nil
		}
		return string(raw), nil
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	if string(data) == "null" {
		return "{}", nil
	}
	return string(data), nil
}

func toolResult(result any) (copilot.ToolResult, error) {
	if result == nil {
		return copilot.ToolResult{ResultType: "success"}, nil
	}
	if copilotResult, ok := result.(copilot.ToolResult); ok {
		return copilotResult, nil
	}
	if text, ok := result.(string); ok {
		return copilot.ToolResult{
			TextResultForLLM: text,
			ResultType:       "success",
		}, nil
	}
	if raw, ok := result.(json.RawMessage); ok {
		return copilot.ToolResult{
			TextResultForLLM: string(raw),
			ResultType:       "success",
		}, nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return copilot.ToolResult{}, err
	}
	return copilot.ToolResult{
		TextResultForLLM: string(data),
		ResultType:       "success",
	}, nil
}

func buildMessageOptions(messages []*message.Message) (copilot.MessageOptions, func(), error) {
	var promptParts []string
	var attachments []copilot.Attachment
	var tempDir string
	cleanup := func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		promptParts = append(promptParts, msg.String())
		for _, content := range msg.Contents {
			dataContent, ok := content.(*message.DataContent)
			if !ok {
				continue
			}
			if tempDir == "" {
				var err error
				tempDir, err = os.MkdirTemp("", "af_copilot_*")
				if err != nil {
					cleanup()
					return copilot.MessageOptions{}, func() {}, err
				}
			}
			path, displayName, err := saveDataContentAttachment(tempDir, len(attachments), dataContent)
			if err != nil {
				cleanup()
				return copilot.MessageOptions{}, func() {}, err
			}
			attachments = append(attachments, &copilot.AttachmentFile{
				Path:        path,
				DisplayName: displayName,
			})
		}
	}
	return copilot.MessageOptions{
		Prompt:      strings.Join(promptParts, "\n"),
		Attachments: attachments,
	}, cleanup, nil
}

func saveDataContentAttachment(tempDir string, index int, content *message.DataContent) (string, string, error) {
	data, err := base64.StdEncoding.DecodeString(content.Data)
	if err != nil {
		return "", "", err
	}
	displayName := filepath.Base(content.Name)
	if displayName == "." || displayName == string(filepath.Separator) || displayName == "" {
		displayName = fmt.Sprintf("attachment-%d", index+1)
	}
	path := filepath.Join(tempDir, fmt.Sprintf("%d-%s", index+1, displayName))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", "", err
	}
	return path, displayName, nil
}

func (p *provider) assistantMessageDeltaUpdate(event copilot.SessionEvent, data *copilot.AssistantMessageDeltaData) *agent.ResponseUpdate {
	content := &message.TextContent{
		ContentHeader: message.ContentHeader{RawRepresentation: event},
		Text:          data.DeltaContent,
	}
	return &agent.ResponseUpdate{
		RawRepresentation: event,
		Role:              message.RoleAssistant,
		MessageID:         data.MessageID,
		CreatedAt:         event.Timestamp,
		Contents:          []message.Content{content},
	}
}

func (p *provider) assistantMessageUpdate(event copilot.SessionEvent, data *copilot.AssistantMessageData, streaming bool) *agent.ResponseUpdate {
	update := &agent.ResponseUpdate{
		RawRepresentation: event,
		Role:              message.RoleAssistant,
		ResponseID:        data.MessageID,
		MessageID:         data.MessageID,
		CreatedAt:         event.Timestamp,
	}
	if streaming {
		update.Contents = []message.Content{&message.RawContent{
			ContentHeader: message.ContentHeader{RawRepresentation: event},
		}}
	} else {
		update.Contents = []message.Content{&message.TextContent{
			ContentHeader: message.ContentHeader{RawRepresentation: event},
			Text:          data.Content,
		}}
	}
	return update
}

func (p *provider) toolExecutionStartUpdate(event copilot.SessionEvent, data *copilot.ToolExecutionStartData) *agent.ResponseUpdate {
	arguments, argErr := functionArguments(data.Arguments)
	content := &message.FunctionCallContent{
		ContentHeader: message.ContentHeader{RawRepresentation: event},
		CallID:        data.ToolCallID,
		Name:          data.ToolName,
		Arguments:     arguments,
		Error:         argErr,
	}
	return &agent.ResponseUpdate{
		RawRepresentation: event,
		Role:              message.RoleAssistant,
		CreatedAt:         event.Timestamp,
		Contents:          []message.Content{content},
	}
}

func (p *provider) toolExecutionCompleteUpdate(event copilot.SessionEvent, data *copilot.ToolExecutionCompleteData) *agent.ResponseUpdate {
	content := &message.FunctionResultContent{
		ContentHeader: message.ContentHeader{RawRepresentation: event},
		CallID:        data.ToolCallID,
		Result:        toolExecutionResult(data),
	}
	return &agent.ResponseUpdate{
		RawRepresentation: event,
		Role:              message.RoleTool,
		CreatedAt:         event.Timestamp,
		Contents:          []message.Content{content},
	}
}

func toolExecutionResult(data *copilot.ToolExecutionCompleteData) any {
	if data == nil {
		return "Tool execution failed"
	}
	if data.Success {
		if data.Result == nil {
			return nil
		}
		return data.Result.Content
	}
	if data.Error != nil && data.Error.Message != "" {
		return data.Error.Message
	}
	return "Tool execution failed"
}

func (p *provider) assistantUsageUpdate(event copilot.SessionEvent, data *copilot.AssistantUsageData) *agent.ResponseUpdate {
	inputTokens := int64Value(data.InputTokens)
	outputTokens := int64Value(data.OutputTokens)
	details := message.UsageDetails{
		InputTokenCount:       inputTokens,
		OutputTokenCount:      outputTokens,
		TotalTokenCount:       inputTokens + outputTokens,
		CachedInputTokenCount: int64Value(data.CacheReadTokens),
		ReasoningTokenCount:   int64Value(data.ReasoningTokens),
		AdditionalCounts:      additionalUsageCounts(data),
	}
	update := &agent.ResponseUpdate{
		RawRepresentation: event,
		Role:              message.RoleAssistant,
		CreatedAt:         event.Timestamp,
		Contents: []message.Content{&message.UsageContent{
			ContentHeader: message.ContentHeader{RawRepresentation: event},
			Details:       details,
		}},
	}
	if data.FinishReason != nil {
		update.FinishReason = *data.FinishReason
	}
	return update
}

func additionalUsageCounts(data *copilot.AssistantUsageData) map[string]int64 {
	additional := make(map[string]int64)
	if data.CacheWriteTokens != nil {
		additional["CacheWriteTokens"] = *data.CacheWriteTokens
	}
	if data.Cost != nil {
		additional["Cost"] = int64(*data.Cost)
	}
	if data.Duration != nil {
		additional["Duration"] = *data.Duration
	}
	if len(additional) == 0 {
		return nil
	}
	return additional
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func rawEventUpdate(event copilot.SessionEvent) *agent.ResponseUpdate {
	return &agent.ResponseUpdate{
		RawRepresentation: event,
		Role:              message.RoleAssistant,
		CreatedAt:         event.Timestamp,
		Contents: []message.Content{&message.RawContent{
			ContentHeader: message.ContentHeader{RawRepresentation: event},
		}},
	}
}

func sessionErrorMessage(data *copilot.SessionErrorData) string {
	if data != nil && data.Message != "" {
		return data.Message
	}
	return "unknown error"
}

func functionArguments(arguments any) (string, error) {
	if arguments == nil {
		return "", nil
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	if isJSONObject(data) {
		return string(data), nil
	}
	wrapped, err := json.Marshal(map[string]any{"value": arguments})
	if err != nil {
		return "", err
	}
	return string(wrapped), nil
}

func isJSONObject(data []byte) bool {
	data = []byte(strings.TrimSpace(string(data)))
	return len(data) > 0 && data[0] == '{'
}
