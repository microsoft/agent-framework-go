// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework/go/format"
	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/memory/inmemory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
)

// Config contains configuration for an [Agent].
type Config struct {
	ID   string
	Name string
	Opts *RunOptions

	SystemInstructions string

	// The following functions implement the core behavior of the agent.
	// If any of these are nil, the corresponding functionality is not supported,
	// and the [Agent] might fall back to default behavior or return an error.
	// The input parameters will always be non-nil and can be mutated as needed.
	NewThread          func() memory.Thread
	UnmarshalThread    func(data []byte) (memory.Thread, error)
	NewContextProvider func() memory.ContextProvider
	Run                func(ctx *RunContext, messages ...*message.Message) (*RunResponse, error)
	RunStream          func(ctx *RunContext, messages ...*message.Message) iter.Seq2[*RunResponseUpdate, error]
	RunOf              func(v any, ctx *RunContext, messages ...*message.Message) (*RunResponse, error)
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

func (a *Agent) NewThread() memory.Thread {
	if a.Config.NewThread != nil {
		return a.Config.NewThread()
	}
	var ctx memory.ContextProvider
	if a.Config.NewContextProvider != nil {
		ctx = a.Config.NewContextProvider()
	}
	return &inmemory.Thread{
		Provider: ctx,
	}
}

func (a *Agent) UnmarshalThread(data []byte) (memory.Thread, error) {
	if a.Config.UnmarshalThread != nil {
		return a.Config.UnmarshalThread(data)
	}
	var ctx memory.ContextProvider
	if a.Config.NewContextProvider != nil {
		ctx = a.Config.NewContextProvider()
	}
	thread := &inmemory.Thread{
		Provider: ctx,
	}
	if err := json.Unmarshal(data, thread); err != nil {
		return nil, err
	}
	return thread, nil
}

// Run executes the agent with the given messages and returns the response.
func (a *Agent) Run(ctx *RunContext, messages ...*message.Message) (*RunResponse, error) {
	if a.Config.Run == nil {
		return nil, fmt.Errorf("agent does not support Run")
	}
	return a.run(nil, ctx, messages...)
}

func (a *Agent) run(out any, ctx *RunContext, messages ...*message.Message) (*RunResponse, error) {
	ctx, threadMessages, err := a.prepareRun(ctx, messages)
	if err != nil {
		return nil, err
	}
	run := func(msgs ...*message.Message) (*RunResponse, error) {
		if out != nil {
			return a.Config.RunOf(out, ctx, msgs...)
		}
		return a.Config.Run(ctx, msgs...)
	}
	startLength := len(threadMessages)
	id := a.ID()
	var finalResponse *RunResponse
	const maxRetries = 5
	for range maxRetries {
		// Run approved tool calls
		handleApprovalContents(ctx.Context, ctx.Options, threadMessages...)
		// Call the chat client
		response, err := run(threadMessages...)
		if err != nil {
			return nil, err
		}
		msg := response.Messages[0]
		threadMessages = append(threadMessages, msg)
		toolResult, inputRequests := runToolCalls(ctx.Context, ctx.Options, msg.Contents...)
		if len(toolResult) > 0 {
			// Add tool results to thread and continue processing, unless there are also
			// approval requests which require stopping to wait for user input
			threadMessages = append(threadMessages, &message.Message{Role: message.RoleTool, Contents: toolResult})
			if len(inputRequests) == 0 {
				continue
			}
		}
		// Final response: either has approval requests, or no more tool calls to process
		response.UserInputRequest = inputRequests
		finalResponse = response
		break
	}
	if finalResponse == nil {
		// Exceeded max retries with tool calls pending, disable tools and try one last time
		// to get a final response.
		ctx.Options.ToolMode = tool.ToolModeNone
		finalResponse, err = run(threadMessages...)
		if err != nil {
			return nil, err
		}
		message := finalResponse.Messages[0]
		threadMessages = append(threadMessages, message)
	}
	outMessages := threadMessages[startLength:]
	if ctx.Thread != nil {
		if err := ctx.Thread.AddMessage(ctx, outMessages...); err != nil {
			return nil, err
		}
	}
	if ctxprovider := a.contextProvider(ctx.Thread); ctxprovider != nil {
		if err := ctxprovider.Invoked(&memory.InvokedContext{
			Context:   ctx,
			Messages:  messages,
			Responses: outMessages,
			Err:       nil,
		}); err != nil {
			return nil, err
		}
	}
	response := &RunResponse{
		Messages:         outMessages,
		ResponseID:       finalResponse.ResponseID,
		AgentID:          id,
		FinishReason:     finalResponse.FinishReason,
		UserInputRequest: finalResponse.UserInputRequest,
	}
	return response, nil
}

// RunOf executes the agent with the given value type and messages, and stores the result
// in the value pointed to by v.
func (a *Agent) RunOf(v any, ctx *RunContext, messages ...*message.Message) (*RunResponse, error) {
	if a.Config.RunOf == nil {
		return nil, fmt.Errorf("agent does not support RunOf")
	}
	return a.run(v, ctx, messages...)
}

// RunFor executes the agent with the given messages and returns the result of type T.
func RunFor[T any](a *Agent, ctx *RunContext, messages ...*message.Message) (T, *RunResponse, error) {
	var v T
	resp, err := a.RunOf(&v, ctx, messages...)
	return v, resp, err
}

// RunText executes the agent with a single text message and returns the response.
func (a *Agent) RunText(ctx *RunContext, msg string) (*RunResponse, error) {
	return a.Run(ctx, message.NewText(msg))
}

// RunStream executes the agent with the given messages and returns a streaming response.
func (a *Agent) RunStream(ctx *RunContext, messages ...*message.Message) iter.Seq2[*RunResponseUpdate, error] {
	if a.Config.RunStream == nil {
		return a.runStreamFallback(ctx, messages...)
	}
	ctx, threadMessages, err := a.prepareRun(ctx, messages)
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
			// Run approved tool calls
			handleApprovalContents(ctx.Context, ctx.Options, threadMessages...)
			var contents []message.Content
			for update, err := range a.Config.RunStream(ctx, threadMessages...) {
				if err != nil {
					yield(nil, err)
					return
				}
				contents = append(contents, update.Contents...)
				if !yield(update, nil) {
					return
				}
			}
			msg := &message.Message{Role: message.RoleAssistant, Contents: contents}
			threadMessages = append(threadMessages, msg)

			// Check if this response contains tool calls
			hasToolCalls := slices.ContainsFunc(contents, func(c message.Content) bool {
				_, ok := c.(*message.FunctionCallContent)
				return ok
			})

			if !hasToolCalls {
				// This is a final response (no tool calls)
				success = true
				break
			}

			// Execute tools
			toolResult, inputRequests := runToolCalls(ctx.Context, ctx.Options, msg.Contents...)
			if len(toolResult) > 0 {
				// Yield tool results and add to thread
				if !yield(&RunResponseUpdate{
					Contents:         toolResult,
					Role:             message.RoleAssistant,
					AgentID:          id,
					UserInputRequest: inputRequests,
				}, nil) {
					return
				}
				threadMessages = append(threadMessages, &message.Message{Role: message.RoleTool, Contents: toolResult})
				// Continue processing unless there are also approval requests
				if len(inputRequests) == 0 {
					continue
				}
			} else if len(inputRequests) > 0 {
				// Approval requests only, no tool results - yield them
				if !yield(&RunResponseUpdate{
					Role:             message.RoleAssistant,
					AgentID:          id,
					UserInputRequest: inputRequests,
				}, nil) {
					return
				}
			}
			// No more tool calls to process (either approval requests or final response)
			success = true
			break
		}
		if !success {
			// Exceeded max retries with tool calls pending, disable tools and try one last time
			// to get a final response.
			ctx.Options.ToolMode = tool.ToolModeNone
			for update, err := range a.Config.RunStream(ctx, threadMessages...) {
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(update, nil) {
					return
				}
			}
		}
		if ctx.Thread != nil {
			if err := ctx.Thread.AddMessage(ctx, threadMessages[startLength:]...); err != nil {
				yield(nil, err)
				return
			}
		}

		if ctxprovider := a.contextProvider(ctx.Thread); ctxprovider != nil {
			if err := ctxprovider.Invoked(&memory.InvokedContext{
				Context:   ctx,
				Messages:  messages,
				Responses: threadMessages[startLength:],
				Err:       nil,
			}); err != nil {
				yield(nil, err)
				return
			}
		}
	}
}

func (a *Agent) runStreamFallback(ctx *RunContext, messages ...*message.Message) iter.Seq2[*RunResponseUpdate, error] {
	resp, err := a.Run(ctx, messages...)
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

// RunContext contains context for agent execution.
type RunContext struct {
	context.Context
	Thread  memory.Thread
	Options *RunOptions
}

// RunOptions contains options for agent execution.
type RunOptions struct {
	// ResponseFormat represents the desired response format for agent execution.
	// It is up to the client implementation if or how to honor the request.
	// If the client implementation doesn't recognize the specific kind, it can be ignored.
	// If nil, the client default is used.
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
	AgentID          string
	ResponseID       string
	FinishReason     string
	Messages         []*message.Message
	UserInputRequest []message.Content
}

// String returns the concatenated text contents of the response messages.
func (r *RunResponse) String() string {
	var sb strings.Builder
	for _, msg := range r.Messages {
		for _, c := range msg.Contents {
			if textContent, ok := c.(*message.TextContent); ok {
				sb.WriteString(textContent.Text)
			}
		}
	}
	return sb.String()
}

// RunResponseUpdate represents a streaming update from an agent execution.
type RunResponseUpdate struct {
	AgentID          string
	MessageID        string
	ResponseID       string
	Role             message.Role
	Contents         []message.Content
	UserInputRequest []message.Content
}

// String returns the concatenated text contents of the response messages.
func (r *RunResponseUpdate) String() string {
	var sb strings.Builder
	for _, c := range r.Contents {
		if textContent, ok := c.(*message.TextContent); ok {
			sb.WriteString(textContent.Text)
		}
	}
	return sb.String()
}

func (a *Agent) contextProvider(thread memory.Thread) memory.ContextProvider {
	if thread != nil {
		if thread, ok := thread.(memory.ContextProviderThread); ok {
			return thread.ContextProvider()
		}
	}
	return nil
}

func (a *Agent) prepareRun(c *RunContext, messages []*message.Message) (*RunContext, []*message.Message, error) {
	var ctx RunContext
	if c != nil {
		ctx = *c
		c = nil // prevent reuse
	}
	if ctx.Context == nil {
		ctx.Context = context.Background()
	}
	ctx.Options = a.Config.Opts.Merge(ctx.Options)
	messages = slices.Clone(messages)
	instructions := a.Config.SystemInstructions
	if ctx.Options != nil {
		if err := initTools(ctx, ctx.Options.Tools); err != nil {
			return nil, nil, err
		}
		extraTools, err := loadTools(ctx, ctx.Options.Tools)
		if err != nil {
			return nil, nil, err
		}
		ctx.Options.Tools = append(ctx.Options.Tools, extraTools...)
	}
	ctxProvider := a.contextProvider(ctx.Thread)
	if ctxProvider != nil {
		ctxData, err := ctxProvider.Invoking(&memory.InvokingContext{
			Context:  ctx,
			Messages: messages,
		})
		if err != nil {
			return nil, nil, err
		}
		if ctxData != nil {
			ctx.Options.Tools = append(ctx.Options.Tools, ctxData.Tools...)
			if len(ctxData.Messages) > 0 {
				messages = append(ctxData.Messages, messages...)
			}
			if ctxData.Instructions != "" {
				instructions += "\n" + ctxData.Instructions
			}
		}
	}
	if instructions != "" {
		messages = append([]*message.Message{{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: instructions}}}}, messages...)
	}
	return &ctx, messages, nil
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

func toolNotFoundContent(fc *message.FunctionCallContent) message.Content {
	return &message.FunctionCallContent{
		Name:      fc.Name,
		CallID:    fc.CallID,
		Arguments: fc.Name,
		Error:     fmt.Errorf("tool not found: %s", fc.Name),
	}
}

// handleApprovalContents executes tool calls that have been approved by the user.
// Messages are updated in place to replace approval requests/responses with actual
// function call results.
func handleApprovalContents(ctx context.Context, options *RunOptions, messages ...*message.Message) {
	funcCalls := make(map[string]struct{})
	var contentToRemove []int
	for _, msg := range messages {
		clear(funcCalls)
		contentToRemove = contentToRemove[:0]
		// Collect existing function call IDs to avoid duplicates
		for _, c := range msg.Contents {
			if funcResult, ok := c.(*message.FunctionCallContent); ok && funcResult.CallID != "" {
				funcCalls[funcResult.CallID] = struct{}{}
			}
		}
		for i, c := range msg.Contents {
			switch c := c.(type) {
			case *message.FunctionApprovalRequestContent:
				if _, found := funcCalls[c.FunctionCall.CallID]; found {
					contentToRemove = append(contentToRemove, i)
				} else {
					// Put back the function call content only if it doesn't exist
					msg.Contents[i] = c.FunctionCall
				}
			case *message.FunctionApprovalResponseContent:
				msg.Role = message.RoleTool
				if c.Approved {
					// Run the tool and replace the approval response with the result.
					tl := funcToolByName(options.Tools, c.FunctionCall.Name)
					if tl == nil {
						// Should not happen as approval was given for an existing tool,
						// but handle gracefully.
						msg.Contents[i] = toolNotFoundContent(c.FunctionCall)
						continue
					}
					msg.Contents[i] = callFunc(ctx, tl, c.FunctionCall.CallID, c.FunctionCall.Arguments)
				} else {
					// Create a "not approved" result for rejected calls.
					msg.Contents[i] = &message.FunctionResultContent{
						CallID: c.FunctionCall.CallID,
						Result: "error: tool call invocation was rejected by user",
					}
				}
			}
		}
		// Remove duplicated contents in reverse order to keep indices valid.
		for i := len(contentToRemove) - 1; i >= 0; i-- {
			idx := contentToRemove[i]
			msg.Contents = append(msg.Contents[:idx], msg.Contents[idx+1:]...)
		}
	}
}

// runToolCalls executes function tool calls found in the given contents.
// It returns the function call results and any input requests (e.g., approvals) generated.
// It skips function calls that have already been executed (i.e., have a corresponding FunctionResultContent).
func runToolCalls(ctx context.Context, options *RunOptions, contents ...message.Content) (funcContent, inputRequests []message.Content) {
	if len(options.Tools) == 0 {
		return nil, nil
	}
	// Collect existing function call IDs to avoid duplicates
	funcResults := make(map[string]struct{})
	var nfuncCalls int
	for _, c := range contents {
		switch c := c.(type) {
		case *message.FunctionCallContent:
			nfuncCalls++
		case *message.FunctionResultContent:
			if c.CallID != "" {
				funcResults[c.CallID] = struct{}{}
			}
		}
	}
	// Find function calls that have not yet been executed
	funcContent = make([]message.Content, 0, nfuncCalls)
	for i, c := range contents {
		fc, ok := c.(*message.FunctionCallContent)
		if !ok {
			continue
		}
		if _, executed := funcResults[fc.CallID]; executed {
			// Skip already executed function calls
			continue
		}
		if fc.Error != nil {
			// If there was an error parsing the tool call, return it as-is.
			// This error occurred during mapping from the model to FunctionCallContent.
			funcContent = append(funcContent, fc)
			continue
		}
		tl := funcToolByName(options.Tools, fc.Name)
		if tl == nil {
			// Tool not found, return an error content.
			funcContent = append(funcContent, toolNotFoundContent(fc))
			continue
		}
		if tl, ok := tl.(tool.ApprovalRequiredTool); ok && tl.ApprovalRequired() {
			// Create an approval request content instead of executing the tool.
			approvalReq := &message.FunctionApprovalRequestContent{
				ID:           fmt.Sprintf("approval-%s", fc.CallID),
				FunctionCall: fc,
			}
			inputRequests = append(inputRequests, approvalReq)
			// Replace the function call content with the approval request.
			contents[i] = approvalReq
			continue
		}
		funcContent = append(funcContent, callFunc(ctx, tl, fc.CallID, fc.Arguments))
	}

	return funcContent, inputRequests
}

func funcToolByName(tools []tool.Tool, name string) tool.FuncTool {
	var found tool.FuncTool
	for _, t := range tools {
		if v, _ := t.ToolInfo(); v == name {
			if t, ok := t.(tool.FuncTool); ok {
				found = t
			}
			break
		}
	}
	return found
}

// callFunc executes a function tool call.
func callFunc(ctx context.Context, tl tool.FuncTool, callID, arguments string) message.Content {
	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			// FunctionResultContent.Error indicates an error during tool execution,
			// but here the error occurred during argument parsing.
			// Return a FunctionCallContent with the error instead.
			return &message.FunctionCallContent{
				CallID: callID,
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
		result, err = tl.Call(ctx, args)
	}()

	return &message.FunctionResultContent{
		CallID: callID,
		Error:  err,
		Result: result,
	}
}
