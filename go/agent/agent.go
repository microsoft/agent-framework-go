// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"cmp"
	"context"
	"iter"
	"maps"
	"slices"

	"github.com/google/uuid"
)

// Agent represents an AI agent that can execute tasks using a client and tools.
type Agent struct {
	config Config
	client Client
	opts   *RunOptions
}

// Config contains configuration for an [Agent].
type Config struct {
	ID   string
	Name string

	SystemInstructions string
}

// Client is the interface implemented by agent clients.
type Client interface {
	Run(ctx context.Context, thread Thread, config Config, opts *RunOptions, messages ...*Message) (*RunResponse, error)
}

// New creates a new [Agent].
//
// The first argument must not be nil.
//
// If non-nil, the provided configuration configure the Agent.
// If non-nil, the provided options are used as default run options for each execution.
func New(client Client, config *Config, opts *RunOptions) *Agent {
	if client == nil {
		panic("nil Client")
	}
	a := &Agent{
		client: client,
	}
	if config != nil {
		a.config = *config
	}
	if opts != nil {
		a.opts = opts
	}
	if a.config.ID == "" {
		a.config.ID = uuid.New().String()
	}
	return a
}

func (a *Agent) ID() string {
	return a.config.ID
}

func (a *Agent) Name() string {
	return a.config.Name
}

func (a *Agent) NewThread() Thread {
	return new(InMemoryThread)
}

func (a *Agent) Run(ctx context.Context, thread Thread, opts *RunOptions, messages ...*Message) (*RunResponse, error) {
	opts = a.opts.Merge(opts)
	messages = slices.Clone(messages)
	if a.config.SystemInstructions != "" {
		messages = append([]*Message{NewMessage(RoleSystem, &TextContent{Text: a.config.SystemInstructions})}, messages...)
	}
	var err error
	if opts != nil {
		if err := initTools(ctx, opts.Tools); err != nil {
			return nil, err
		}
		extraTools, err := loadTools(ctx, opts.Tools)
		if err != nil {
			return nil, err
		}
		opts.Tools = append(opts.Tools, extraTools...)
	}

	// Prepare messages with system instructions
	threadMessages, err := prepareMessages(ctx, thread, messages)
	if err != nil {
		return nil, err
	}
	startLength := len(threadMessages)
	id := a.ID()
	var finalResponse *RunResponse
	const maxRetries = 5
	for range maxRetries {
		// Call the chat client
		response, err := a.client.Run(ctx, thread, a.config, opts, threadMessages...)
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
		opts.ToolMode = ToolModeNone
		finalResponse, err = a.client.Run(ctx, thread, a.config, opts, threadMessages...)
		if err != nil {
			return nil, err
		}
		message := finalResponse.Messages[0]
		threadMessages = append(threadMessages, message)
	}
	if thread != nil {
		if err := thread.Add(ctx, threadMessages[startLength:]...); err != nil {
			return nil, err
		}
	}
	return &RunResponse{
		Messages:   threadMessages[startLength:],
		ResponseID: finalResponse.ResponseID,
		AgentID:    id,
	}, nil
}

func (a *Agent) RunText(ctx context.Context, msg string) (*RunResponse, error) {
	return a.Run(ctx, nil, nil, NewTextMessage(msg))
}

func (a *Agent) RunStream(ctx context.Context, thread Thread, opts *RunOptions, messages ...*Message) iter.Seq2[*RunResponseUpdate, error] {
	client, ok := a.client.(streamableClient)
	if !ok {
		return a.runStreamFallback(ctx, thread, opts, messages...)
	}
	opts = a.opts.Merge(opts)
	messages = slices.Clone(messages)
	if a.config.SystemInstructions != "" {
		messages = append([]*Message{NewMessage(RoleSystem, &TextContent{Text: a.config.SystemInstructions})}, messages...)
	}
	err := initTools(ctx, opts.Tools)
	var threadMessages []*Message
	if err == nil {
		threadMessages, err = prepareMessages(ctx, thread, messages)
	}
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
			for update, err := range client.RunStream(ctx, thread, a.config, opts, threadMessages...) {
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
			if !slices.ContainsFunc(contents, func(content Content) bool {
				_, ok := content.(*FunctionCallContent)
				return ok
			}) {
				break
			}
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
			opts.ToolMode = ToolModeNone
			for update, err := range client.RunStream(ctx, thread, a.config, opts, threadMessages...) {
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
			if err := thread.Add(ctx, threadMessages[startLength:]...); err != nil {
				yield(nil, err)
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
	// Tools to make available to the agent.
	Tools []Tool

	// ToolMode specifies how tools should be used.
	ToolMode ToolMode

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
	var text string
	for _, content := range r.Contents {
		if textContent, ok := content.(*TextContent); ok {
			text += textContent.Text
		}
	}
	return text
}

func prepareMessages(ctx context.Context, t Thread, messages []*Message) ([]*Message, error) {
	if t != nil {
		for msg, err := range t.All(ctx) {
			if err != nil {
				return nil, err
			}
			messages = append(messages, msg)
		}
	}
	return messages, nil
}
