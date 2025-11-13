// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"cmp"
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework/go/format"
	"github.com/microsoft/agent-framework/go/tool"
)

// Config contains configuration for an [Agent].
type Config struct {
	ID   string
	Name string
	Opts *RunOptions

	SystemInstructions string

	NewThread func() Thread

	ContextProvider ContextProvider

	Run func(ctx context.Context, thread Thread, opts *RunOptions, messages ...*Message) (*RunResponse, error)

	RunStream func(ctx context.Context, thread Thread, opts *RunOptions, messages ...*Message) iter.Seq2[*RunResponseUpdate, error]
}

// Agent represents an AI agent that can execute tasks using a client and tools.
type Agent struct {
	Config Config
}

func (a *Agent) ID() string {
	return a.Config.ID
}

func (a *Agent) Name() string {
	return a.Config.Name
}

func (a *Agent) NewThread() Thread {
	if a.Config.NewThread != nil {
		return a.Config.NewThread()
	}
	return NewInMemoryThread(a.Config.ContextProvider)
}

func (a *Agent) Run(ctx context.Context, thread Thread, opts *RunOptions, messages ...*Message) (*RunResponse, error) {
	// Prepare messages with system instructions
	opts, threadMessages, err := a.prepareRun(ctx, thread, opts, messages)
	if err != nil {
		return nil, err
	}
	startLength := len(threadMessages)
	id := a.ID()
	var finalResponse *RunResponse
	const maxRetries = 5
	for range maxRetries {
		// Call the chat client
		response, err := a.Config.Run(ctx, thread, opts, threadMessages...)
		if err != nil {
			return nil, err
		}
		message := response.Messages[0]
		threadMessages = append(threadMessages, message)
		toolResult := runToolCalls(ctx, opts, message.Contents...)
		if len(toolResult) > 0 {
			// Add a single Message to the response with the results
			threadMessages = append(threadMessages, NewMessage(RoleTool, toolResult...))
			continue
		}
		finalResponse = response
		break
	}
	if finalResponse == nil {
		// Exceeded max retries with tool calls pending, disable tools and try one last time
		// to get a final response.
		opts.ToolMode = tool.ToolModeNone
		finalResponse, err = a.Config.Run(ctx, thread, opts, threadMessages...)
		if err != nil {
			return nil, err
		}
		message := finalResponse.Messages[0]
		threadMessages = append(threadMessages, message)
	}
	outMessages := threadMessages[startLength:]
	if thread != nil {
		if err := thread.AddMessage(ctx, outMessages...); err != nil {
			return nil, err
		}
	}
	if ctxprovider := a.contextProvider(thread); ctxprovider != nil {
		if err := ctxprovider.Invoked(ctx, messages, outMessages, nil); err != nil {
			return nil, err
		}
	}
	response := &RunResponse{
		Messages:   threadMessages[startLength:],
		ResponseID: finalResponse.ResponseID,
		AgentID:    id,
	}
	if opts.Response != nil {
		if err := opts.Response.UnmarshalBinary([]byte(response.Text())); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}
	return response, nil
}

func (a *Agent) RunText(ctx context.Context, msg string) (*RunResponse, error) {
	return a.Run(ctx, nil, nil, NewTextMessage(msg))
}

func (a *Agent) RunStream(ctx context.Context, thread Thread, opts *RunOptions, messages ...*Message) iter.Seq2[*RunResponseUpdate, error] {
	if a.Config.RunStream == nil {
		return a.runStreamFallback(ctx, thread, opts, messages...)
	}
	opts, threadMessages, err := a.prepareRun(ctx, thread, opts, messages)
	startLength := len(threadMessages)
	id := a.ID()
	return func(yield func(*RunResponseUpdate, error) bool) {
		if err != nil {
			yield(nil, err)
			return
		}
		const maxRetries = 5
		var success bool

		for range maxRetries {
			var contents []Content
			for update, err := range a.Config.RunStream(ctx, thread, opts, threadMessages...) {
				if err != nil {
					yield(nil, err)
					return
				}
				contents = append(contents, update.Contents...)
				if !yield(update, nil) {
					return
				}
			}
			threadMessages = append(threadMessages, NewMessage(RoleAssistant, contents...))

			// Check if this response contains tool calls
			hasToolCalls := slices.ContainsFunc(contents, func(content Content) bool {
				_, ok := content.(*FunctionCallContent)
				return ok
			})

			if !hasToolCalls {
				// This is a final response (no tool calls)
				success = true
				break
			}

			// Execute tools
			toolResult := runToolCalls(ctx, opts, contents...)
			if len(toolResult) > 0 {
				// Add a single Message to the response with the results
				if !yield(&RunResponseUpdate{
					Contents: toolResult,
					Role:     RoleAssistant,
					AgentID:  id,
				}, nil) {
					return
				}
				threadMessages = append(threadMessages, NewMessage(RoleTool, toolResult...))
				continue
			}
			// No more tool calls to process
			success = true
			break
		}
		if !success {
			// Exceeded max retries with tool calls pending, disable tools and try one last time
			// to get a final response.
			opts.ToolMode = tool.ToolModeNone
			for update, err := range a.Config.RunStream(ctx, thread, opts, threadMessages...) {
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(update, nil) {
					return
				}
			}
		}
		if thread != nil {
			if err := thread.AddMessage(ctx, threadMessages[startLength:]...); err != nil {
				yield(nil, err)
				return
			}
		}

		if ctxprovider := a.contextProvider(thread); ctxprovider != nil {
			if err := ctxprovider.Invoked(ctx, messages, threadMessages[startLength:], nil); err != nil {
				yield(nil, err)
				return
			}
		}
		if opts.Response != nil {
			var finalText strings.Builder
			for _, msg := range threadMessages[startLength:] {
				for _, content := range msg.Contents {
					if textContent, ok := content.(*TextContent); ok {
						finalText.WriteString(textContent.Text)
					}
				}
			}
			if err := opts.Response.UnmarshalBinary([]byte(finalText.String())); err != nil {
				yield(nil, fmt.Errorf("failed to unmarshal response: %w", err))
				return
			}
		}
	}
}

func (a *Agent) runStreamFallback(ctx context.Context, thread Thread, options *RunOptions, messages ...*Message) iter.Seq2[*RunResponseUpdate, error] {
	resp, err := a.Run(ctx, thread, options, messages...)
	id := a.ID()
	return func(yield func(*RunResponseUpdate, error) bool) {
		if err != nil {
			yield(nil, err)
			return
		}
		for _, msg := range resp.Messages {
			resp := &RunResponseUpdate{
				AgentID:    id,
				MessageID:  msg.MessageID,
				ResponseID: resp.ResponseID,
				Role:       msg.Role,
				Contents:   msg.Contents,
			}
			if !yield(resp, nil) {
				return
			}
		}
	}
}

// RunOptions contains options for agent execution.
type RunOptions struct {
	// Response is an object to unmarshal the response into.
	// For streaming responses, this will be called once with the full response.
	// Ignored if nil.
	Response encoding.BinaryUnmarshaler

	// ResponseFormat represents the desired response format for agent execution.
	// It is up to the client implementation if or how to honor the request.
	// If the client implementation doesn't recognize the specific kind, it can be ignored.
	// If nil and Response implemented [format.FormatProvider], it is obtained from there.
	// Otherwise, the client default is used.
	ResponseFormat format.Format

	// Tools to make available to the agent.
	Tools []tool.Tool

	// ToolMode specifies how tools should be used.
	ToolMode tool.ToolMode

	// MaxTurns limits the number of agent turns.
	MaxTurns int

	// Temperature controls randomness in generation.
	Temperature *float64

	// TopP controls nucleus sampling.
	TopP *float64

	// MaxTokens limits the response length.
	MaxTokens *int

	// AdditionalMetadata for provider-specific options.
	AdditionalMetadata map[string]any
}

// Merge merges another RunOptions into this one, giving precedence to the other.
func (o *RunOptions) Merge(other *RunOptions) *RunOptions {
	if o == nil || other == nil {
		if other != nil {
			return other
		}
		if o != nil {
			return o
		}
		return new(RunOptions)
	}
	result := *o // copy
	o = &result
	o.Tools = append(other.Tools, o.Tools...)
	o.ToolMode = cmp.Or(other.ToolMode, o.ToolMode)
	o.MaxTurns = cmp.Or(other.MaxTurns, o.MaxTurns)
	o.Temperature = cmp.Or(other.Temperature, o.Temperature)
	o.TopP = cmp.Or(other.TopP, o.TopP)
	o.MaxTokens = cmp.Or(other.MaxTokens, o.MaxTokens)
	o.ResponseFormat = cmp.Or(other.ResponseFormat, o.ResponseFormat)
	o.Response = cmp.Or(other.Response, o.Response)
	if other.AdditionalMetadata != nil {
		if o.AdditionalMetadata == nil {
			o.AdditionalMetadata = make(map[string]any)
		}
		maps.Copy(o.AdditionalMetadata, other.AdditionalMetadata)
	}
	return o
}

// RunResponse represents the result of an agent execution.
type RunResponse struct {
	AgentID    string
	ResponseID string
	Messages   []*Message
	Usage      *UsageDetails
}

// Text returns the concatenated text contents of the response messages.
func (r *RunResponse) Text() string {
	var text string
	for _, msg := range r.Messages {
		for _, content := range msg.Contents {
			if textContent, ok := content.(*TextContent); ok {
				text += textContent.Text
			}
		}
	}
	return text
}

// RunResponseUpdate represents a streaming update from an agent execution.
type RunResponseUpdate struct {
	AgentID    string
	MessageID  string
	ResponseID string
	Role       Role
	Contents   []Content
}

// Text returns the concatenated text contents of the response messages.
func (r *RunResponseUpdate) Text() string {
	var sb strings.Builder
	for _, content := range r.Contents {
		if textContent, ok := content.(*TextContent); ok {
			sb.WriteString(textContent.Text)
		}
	}
	return sb.String()
}

func (a *Agent) contextProvider(thread Thread) ContextProvider {
	if a.Config.ContextProvider != nil {
		return a.Config.ContextProvider
	}
	if thread != nil {
		if thread, ok := thread.(contextProviderThread); ok {
			return thread.ContextProvider()
		}
	}
	return nil
}

func (a *Agent) prepareRun(ctx context.Context, thread Thread, opts *RunOptions, messages []*Message) (*RunOptions, []*Message, error) {
	opts = a.Config.Opts.Merge(opts)
	if opts.ResponseFormat == nil && opts.Response != nil {
		// Try to get the format from the response object.
		if rf, ok := opts.Response.(format.FormatProvider); ok {
			var err error
			opts.ResponseFormat, err = rf.Format()
			if err != nil {
				return nil, nil, err
			}
		}
	}
	messages = slices.Clone(messages)
	if a.Config.SystemInstructions != "" {
		messages = append([]*Message{NewMessage(RoleSystem, &TextContent{Text: a.Config.SystemInstructions})}, messages...)
	}
	if opts != nil {
		if err := initTools(ctx, opts.Tools); err != nil {
			return nil, nil, err
		}
		extraTools, err := loadTools(ctx, opts.Tools)
		if err != nil {
			return nil, nil, err
		}
		opts.Tools = append(opts.Tools, extraTools...)
	}
	ctxProvider := a.contextProvider(thread)
	if ctxProvider != nil {
		ctxData, err := ctxProvider.Invoking(ctx, messages)
		if err != nil {
			return nil, nil, err
		}
		if ctxData != nil {
			opts.Tools = append(opts.Tools, ctxData.Tools...)
			if ctxData.Instructions != "" {
				messages = append([]*Message{NewMessage(RoleSystem, &TextContent{Text: ctxData.Instructions})}, messages...)
			}
			messages = append(messages, ctxData.Messages...)
		}
	}
	return opts, messages, nil
}

func loadTools(ctx context.Context, tools []tool.Tool) ([]tool.Tool, error) {
	var result []tool.Tool
	for _, t := range tools {
		if lt, ok := t.(tool.LoaderTool); ok {
			innerTools, err := lt.LoadTools(ctx)
			if err != nil {
				name, _ := t.ToolInfo()
				return nil, fmt.Errorf("failed to load inner tools for %q: %w", name, err)
			}
			result = append(result, innerTools...)
		}
	}
	return result, nil
}

// initTools initializes all tools that implement the InitTool interface.
func initTools(ctx context.Context, tools []tool.Tool) error {
	for _, t := range tools {
		if t, ok := t.(tool.InitTool); ok {
			if err := t.Init(ctx); err != nil {
				name, _ := t.ToolInfo()
				return fmt.Errorf("failed to initialize tool %q: %w", name, err)
			}
		}
	}
	return nil
}

func runToolCalls(ctx context.Context, options *RunOptions, contents ...Content) []Content {
	if len(options.Tools) == 0 {
		return nil
	}
	funcResults := make(map[string]struct{})
	for _, contents := range contents {
		if funcResult, ok := contents.(*FunctionResultContent); ok {
			funcResults[funcResult.CallID] = struct{}{}
		}
	}
	funcCalls := make([]*FunctionCallContent, 0, len(contents)-len(funcResults))
	for _, contents := range contents {
		if fc, ok := contents.(*FunctionCallContent); ok {
			if _, executed := funcResults[fc.CallID]; executed {
				continue
			}
			funcCalls = append(funcCalls, fc)
		}
	}
	toolContent := make([]Content, 0, len(funcCalls))
	for _, fc := range funcCalls {
		toolContent = append(toolContent, funcCall(ctx, options.Tools, fc))
	}
	return toolContent
}

// funcCall executes a function tool call.
func funcCall(ctx context.Context, tools []tool.Tool, toolCall *FunctionCallContent) Content {
	if toolCall.Error != nil {
		// If there was an error parsing the tool call, return it as-is.
		// This error occurred during mapping from the AI model to FunctionCallContent.
		return toolCall
	}

	// Find the tool in the options
	var found tool.CallTool
	for _, t := range tools {
		name, _ := t.ToolInfo()
		if name == toolCall.Name {
			if t, ok := t.(tool.CallTool); ok {
				found = t
			}
			break
		}
	}

	if found == nil {
		return &FunctionResultContent{
			CallID: toolCall.CallID,
			Error:  fmt.Errorf("tool not found: %s", toolCall.Name),
		}
	}

	var args map[string]any
	if toolCall.Arguments != "" {
		if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
			return &FunctionResultContent{
				CallID: toolCall.CallID,
				Error:  fmt.Errorf("failed to parse arguments: %w", err),
			}
		}
	}

	// Handle panics during tool execution
	var result any
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					err = fmt.Errorf("tool execution panic: %w", e)
				} else {
					err = fmt.Errorf("tool execution panic: %v", r)
				}
			}
		}()
		result, err = found.Call(ctx, args)
	}()

	return &FunctionResultContent{
		CallID: toolCall.CallID,
		Error:  err,
		Result: result,
	}
}
