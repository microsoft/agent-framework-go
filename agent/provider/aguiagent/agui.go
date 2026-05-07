// Copyright (c) Microsoft. All rights reserved.

package aguiagent

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	aguiSSEClient "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	aguiEvents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguiTypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

type Config struct {
	agent.Config

	// Instructions are sent to AG-UI as a leading system message for each run.
	Instructions string

	Decoder *aguiEvents.EventDecoder
}

type provider struct {
	client  *aguiSSEClient.Client
	decoder *aguiEvents.EventDecoder
	cfg     Config
}

func New(aclient *aguiSSEClient.Client, config Config) *agent.Agent {
	p := &provider{
		cfg:    config,
		client: aclient,
	}
	if config.Instructions != "" {
		config.RunOptions = append(config.RunOptions, agent.WithInstructions(config.Instructions))
	}
	if config.Decoder != nil {
		p.decoder = config.Decoder
	} else {
		p.decoder = aguiEvents.NewEventDecoder(nil)
	}
	if !config.DisableFuncAutoCall {
		config.Middlewares = slices.Clone(config.Middlewares)
		config.Middlewares = append(config.Middlewares, toolautocall.New(toolautocall.Config{
			Logger:           config.Logger,
			LogSensitiveData: config.LogSensitiveData,
		}))
	}
	return agent.New(agent.ProviderConfig{
		ProviderName: "agui",
		Run:          p.run,
	}, config.Config)
}

func (p *provider) run(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		session, _ := agent.GetOption(options, agent.WithSession)
		threadID := getOrCreateThreadID(session)
		runID := aguiEvents.GenerateRunID()

		state, convertedMessages, err := toAGUIInputMessages(messages)
		if err != nil {
			yield(nil, err)
			return
		}
		instructions := slices.Collect(agent.AllOptions(options, agent.WithInstructions))
		if len(instructions) > 0 {
			convertedMessages = append([]aguiTypes.Message{{
				ID:      aguiEvents.GenerateMessageID(),
				Role:    aguiTypes.RoleSystem,
				Content: strings.Join(instructions, "\n"),
			}}, convertedMessages...)
		}

		payload := aguiTypes.RunAgentInput{
			ThreadID:       threadID,
			RunID:          runID,
			State:          state,
			Messages:       convertedMessages,
			Tools:          toAGUITools(agent.AllOptions(options, agent.WithTool)),
			Context:        []aguiTypes.Context{},
			ForwardedProps: map[string]any{},
		}

		frames, errs, err := p.client.Stream(aguiSSEClient.StreamOptions{Context: ctx, Payload: payload})
		if err != nil {
			yield(nil, err)
			return
		}

		acc := toolCallAccumulator{pending: map[string]*pendingToolCall{}}
		for frames != nil || errs != nil {
			select {
			case frame, ok := <-frames:
				if !ok {
					frames = nil
					continue
				}
				evt, derr := decodeFrame(p.decoder, frame.Data)
				if derr != nil {
					yield(nil, derr)
					return
				}
				updates, uerr := acc.onEvent(evt)
				if uerr != nil {
					yield(nil, uerr)
					return
				}
				for _, update := range updates {
					if update == nil {
						continue
					}
					if update.ResponseID == "" {
						update.ResponseID = runID
					}
					if update.AdditionalProperties == nil {
						update.AdditionalProperties = map[string]any{}
					}
					update.AdditionalProperties["agui_thread_id"] = threadID
					if !yield(update, nil) {
						return
					}
				}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					yield(nil, err)
					return
				}
			}
		}
	}
}

func decodeFrame(decoder *aguiEvents.EventDecoder, data []byte) (aguiEvents.Event, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	return decoder.DecodeEvent(envelope.Type, data)
}

func getOrCreateThreadID(session agent.Session) string {
	if session != nil && session.ServiceID() != "" {
		return session.ServiceID()
	}
	threadID := aguiEvents.GenerateThreadID()
	if session != nil {
		session.SetServiceID(threadID)
	}
	return threadID
}

func toAGUITools(tools iter.Seq[tool.Tool]) []aguiTypes.Tool {
	out := []aguiTypes.Tool{}
	for t := range tools {
		funcTool, ok := t.(tool.FuncTool)
		if !ok {
			continue
		}
		out = append(out, aguiTypes.Tool{
			Name:        funcTool.Name(),
			Description: funcTool.Description(),
			Parameters:  funcTool.Schema(),
		})
	}
	return out
}

func toAGUIInputMessages(messages []*message.Message) (state any, converted []aguiTypes.Message, err error) {
	converted = make([]aguiTypes.Message, 0, len(messages))
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		if i == len(messages)-1 {
			if st, ok, err := extractStateFromMessage(msg); err != nil {
				return nil, nil, err
			} else if ok {
				state = st
				if len(msg.Contents) == 1 {
					continue
				}
			}
		}
		if msg.Role == message.RoleTool {
			toolMessages, err := toAGUIToolMessages(msg)
			if err != nil {
				return nil, nil, err
			}
			converted = append(converted, toolMessages...)
			continue
		}
		ag, ok, err := toAGUIMessage(msg)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			converted = append(converted, ag)
		}
	}
	return state, converted, nil
}

func toAGUIToolMessages(msg *message.Message) ([]aguiTypes.Message, error) {
	baseID := msg.ID
	if baseID == "" {
		baseID = aguiEvents.GenerateMessageID()
	}

	results := make([]*message.FunctionResultContent, 0, len(msg.Contents))
	for _, c := range msg.Contents {
		frc, ok := c.(*message.FunctionResultContent)
		if !ok {
			continue
		}
		results = append(results, frc)
	}

	if len(results) == 0 {
		return []aguiTypes.Message{{
			ID:      baseID,
			Role:    aguiTypes.RoleTool,
			Content: msg.String(),
		}}, nil
	}

	out := make([]aguiTypes.Message, 0, len(results))
	for i, frc := range results {
		content, err := serializeToolResult(frc.Result)
		if err != nil {
			return nil, err
		}
		id := baseID
		if i > 0 {
			id = aguiEvents.GenerateMessageID()
		}
		out = append(out, aguiTypes.Message{
			ID:         id,
			Role:       aguiTypes.RoleTool,
			ToolCallID: frc.CallID,
			Content:    content,
		})
	}

	return out, nil
}

func extractStateFromMessage(msg *message.Message) (any, bool, error) {
	for _, c := range msg.Contents {
		dc, ok := c.(*message.DataContent)
		if !ok || strings.ToLower(strings.TrimSpace(dc.MediaType)) != "application/json" {
			continue
		}
		bytes, err := dc.Bytes()
		if err != nil {
			return nil, false, err
		}
		var state any
		if err := json.Unmarshal(bytes, &state); err != nil {
			return nil, false, err
		}
		return state, true, nil
	}
	return nil, false, nil
}

func toAGUIMessage(msg *message.Message) (aguiTypes.Message, bool, error) {
	out := aguiTypes.Message{ID: msg.ID}
	if out.ID == "" {
		out.ID = aguiEvents.GenerateMessageID()
	}

	switch msg.Role {
	case message.RoleSystem:
		out.Role = aguiTypes.RoleSystem
		out.Content = msg.String()
		return out, true, nil
	case message.RoleUser:
		out.Role = aguiTypes.RoleUser
		out.Content = msg.String()
		return out, true, nil
	case message.RoleAssistant:
		out.Role = aguiTypes.RoleAssistant
		out.Content = msg.String()
		for _, c := range msg.Contents {
			fcc, ok := c.(*message.FunctionCallContent)
			if !ok {
				continue
			}
			out.ToolCalls = append(out.ToolCalls, aguiTypes.ToolCall{
				ID:   fcc.CallID,
				Type: aguiTypes.ToolCallTypeFunction,
				Function: aguiTypes.FunctionCall{
					Name:      fcc.Name,
					Arguments: cmp.Or(strings.TrimSpace(fcc.Arguments), "{}"),
				},
			})
		}
		return out, true, nil
	case message.RoleTool:
		out.Role = aguiTypes.RoleTool
		for _, c := range msg.Contents {
			frc, ok := c.(*message.FunctionResultContent)
			if !ok {
				continue
			}
			out.ToolCallID = frc.CallID
			content, err := serializeToolResult(frc.Result)
			if err != nil {
				return aguiTypes.Message{}, false, err
			}
			out.Content = content
			return out, true, nil
		}
		out.Content = msg.String()
		return out, true, nil
	default:
		return aguiTypes.Message{}, false, nil
	}
}

func serializeToolResult(result any) (string, error) {
	if result == nil {
		return "", nil
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool result: %w", err)
	}
	return string(b), nil
}

type pendingToolCall struct {
	Name          string
	ArgumentsJSON strings.Builder
	MessageID     string
}

type toolCallAccumulator struct {
	pending map[string]*pendingToolCall
}

func (a *toolCallAccumulator) onEvent(evt aguiEvents.Event) ([]*agent.ResponseUpdate, error) {
	switch e := evt.(type) {
	case *aguiEvents.RunStartedEvent:
		return []*agent.ResponseUpdate{{
			Role:       message.RoleAssistant,
			ResponseID: e.RunID(),
			CreatedAt:  eventTime(evt),
			AdditionalProperties: map[string]any{
				"agui_thread_id": e.ThreadID(),
				"agui_run_id":    e.RunID(),
			},
		}}, nil
	case *aguiEvents.RunFinishedEvent:
		if e.Result == nil {
			return nil, nil
		}
		text, err := serializeToolResult(e.Result)
		if err != nil {
			return nil, err
		}
		return []*agent.ResponseUpdate{{
			Role:       message.RoleAssistant,
			ResponseID: e.RunID(),
			CreatedAt:  eventTime(evt),
			Contents:   message.Contents{&message.TextContent{Text: text}},
		}}, nil
	case *aguiEvents.RunErrorEvent:
		return []*agent.ResponseUpdate{{
			Role:       message.RoleAssistant,
			ResponseID: e.RunID(),
			CreatedAt:  eventTime(evt),
			Contents:   message.Contents{&message.ErrorContent{Message: e.Message, ErrorCode: cmp.Or(deref(e.Code), "RunError")}},
		}}, nil
	case *aguiEvents.TextMessageContentEvent:
		return []*agent.ResponseUpdate{{
			Role:      message.RoleAssistant,
			MessageID: e.MessageID,
			CreatedAt: eventTime(evt),
			Contents:  message.Contents{&message.TextContent{Text: e.Delta}},
		}}, nil
	case *aguiEvents.ToolCallStartEvent:
		a.pending[e.ToolCallID] = &pendingToolCall{Name: e.ToolCallName, MessageID: cmp.Or(deref(e.ParentMessageID), e.ToolCallID)}
		return nil, nil
	case *aguiEvents.ToolCallArgsEvent:
		if p := a.pending[e.ToolCallID]; p != nil {
			p.ArgumentsJSON.WriteString(e.Delta)
		}
		return nil, nil
	case *aguiEvents.ToolCallEndEvent:
		p := a.pending[e.ToolCallID]
		if p == nil {
			return nil, nil
		}
		delete(a.pending, e.ToolCallID)
		return []*agent.ResponseUpdate{{
			Role:      message.RoleAssistant,
			MessageID: p.MessageID,
			CreatedAt: eventTime(evt),
			Contents: message.Contents{&message.FunctionCallContent{
				CallID:    e.ToolCallID,
				Name:      p.Name,
				Arguments: cmp.Or(strings.TrimSpace(p.ArgumentsJSON.String()), "{}"),
			}},
		}}, nil
	case *aguiEvents.ToolCallResultEvent:
		result := parseResultContent(e.Content)
		return []*agent.ResponseUpdate{{
			Role:      message.RoleTool,
			MessageID: cmp.Or(e.MessageID, e.ToolCallID),
			CreatedAt: eventTime(evt),
			Contents: message.Contents{&message.FunctionResultContent{
				CallID: e.ToolCallID,
				Result: result,
			}},
		}}, nil
	case *aguiEvents.StateSnapshotEvent:
		return []*agent.ResponseUpdate{{
			Role:      message.RoleAssistant,
			CreatedAt: eventTime(evt),
			Contents:  message.Contents{newJSONDataContent(e.Snapshot, "application/json")},
		}}, nil
	case *aguiEvents.StateDeltaEvent:
		return []*agent.ResponseUpdate{{
			Role:      message.RoleAssistant,
			CreatedAt: eventTime(evt),
			Contents:  message.Contents{newJSONDataContent(e.Delta, "application/json-patch+json")},
		}}, nil
	default:
		return nil, nil
	}
}

func parseResultContent(content string) any {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(content), &value); err == nil {
		return value
	}
	return content
}

func newJSONDataContent(value any, mediaType string) *message.DataContent {
	b, err := json.Marshal(value)
	if err != nil {
		if fallback, err2 := json.Marshal(fmt.Sprint(value)); err2 == nil {
			b = fallback
		} else {
			b = []byte("null")
		}
	}
	return &message.DataContent{MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(b)}
}

func eventTime(evt aguiEvents.Event) time.Time {
	if evt == nil || evt.Timestamp() == nil {
		return time.Now()
	}
	return time.UnixMilli(*evt.Timestamp())
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
