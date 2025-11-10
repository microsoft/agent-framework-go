// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"context"
	"iter"
	"sync"

	"github.com/microsoft/agent-framework/go/agent"
)

// Client is a configurable stub implementation of Client and streamableClient
// that can be used for testing agent functionality.
type Client struct {
	mu sync.Mutex

	agent *agent.Agent

	// RunFunc is called when Run is invoked. If nil, uses default behavior.
	RunFunc func(ctx context.Context, thread agent.Thread, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error)

	// RunStreamFunc is called when RunStream is invoked. If nil, uses default behavior.
	RunStreamFunc func(ctx context.Context, thread agent.Thread, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error]

	// RunCalls records all calls to Run.
	RunCalls []RunCall

	// RunStreamCalls records all calls to RunStream.
	RunStreamCalls []RunStreamCall

	// DefaultResponse is returned when RunFunc is nil.
	DefaultResponse *agent.RunResponse

	// DefaultResponseUpdates are returned when RunStreamFunc is nil.
	DefaultResponseUpdates []*agent.RunResponseUpdate

	// DefaultError is returned when RunFunc/RunStreamFunc is nil and an error should be returned.
	DefaultError error
}

func (c *Client) ID() string {
	return c.agent.ID()
}

// RunCall records a call to Run.
type RunCall struct {
	Ctx      context.Context
	Thread   agent.Thread
	Opts     *agent.RunOptions
	Messages []*agent.Message
}

// RunStreamCall records a call to RunStream.
type RunStreamCall struct {
	Ctx      context.Context
	Thread   agent.Thread
	Opts     *agent.RunOptions
	Messages []*agent.Message
}

// NewAgent creates a new Client with sensible defaults.
func NewAgent() (*Client, *agent.Agent) {
	id := "agent"
	c := &Client{
		DefaultResponse: &agent.RunResponse{
			AgentID:    id,
			ResponseID: "response-1",
			Messages: []*agent.Message{
				agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "response"}),
			},
		},
	}
	c.agent = &agent.Agent{
		Config: agent.Config{
			ID:        id,
			Run:       c.Run,
			RunStream: c.RunStream,
		},
	}
	return c, c.agent
}

// Run implements the Client interface.
func (c *Client) Run(ctx context.Context, thread agent.Thread, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Record the call
	c.RunCalls = append(c.RunCalls, RunCall{
		Ctx:      ctx,
		Thread:   thread,
		Opts:     opts,
		Messages: messages,
	})

	// Use custom function if provided
	if c.RunFunc != nil {
		return c.RunFunc(ctx, thread, opts, messages...)
	}

	// Return error if configured
	if c.DefaultError != nil {
		return nil, c.DefaultError
	}

	// Return default response
	if c.DefaultResponse != nil {
		return c.DefaultResponse, nil
	}

	// Fallback to a minimal response
	return &agent.RunResponse{
		AgentID:    c.ID(),
		ResponseID: "response",
		Messages: []*agent.Message{
			agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "response"}),
		},
	}, nil
}

// RunStream implements the streamableClient interface.
func (c *Client) RunStream(ctx context.Context, thread agent.Thread, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	c.mu.Lock()
	// Record the call
	c.RunStreamCalls = append(c.RunStreamCalls, RunStreamCall{
		Ctx:      ctx,
		Thread:   thread,
		Opts:     opts,
		Messages: messages,
	})

	// Capture values needed for the iterator
	runStreamFunc := c.RunStreamFunc
	defaultError := c.DefaultError
	defaultUpdates := c.DefaultResponseUpdates
	configID := c.ID()
	c.mu.Unlock()

	// Use custom function if provided
	if runStreamFunc != nil {
		return runStreamFunc(ctx, thread, opts, messages...)
	}

	// Return an iterator
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		// Return error if configured
		if defaultError != nil {
			yield(nil, defaultError)
			return
		}

		// Return default updates if configured
		if len(defaultUpdates) > 0 {
			for _, update := range defaultUpdates {
				if !yield(update, nil) {
					return
				}
			}
			return
		}

		// Fallback to a single update
		update := &agent.RunResponseUpdate{
			AgentID:    configID,
			MessageID:  "message-1",
			ResponseID: "response-1",
			Role:       agent.RoleAssistant,
			Contents:   []agent.Content{&agent.TextContent{Text: "streaming response"}},
		}
		yield(update, nil)
	}
}

// Reset clears all recorded calls and resets to default configuration.
func (c *Client) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.RunCalls = nil
	c.RunStreamCalls = nil
	c.RunFunc = nil
	c.RunStreamFunc = nil
	c.DefaultError = nil
}

// SetResponse sets the default response to be returned by Run.
func (c *Client) SetResponse(response *agent.RunResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DefaultResponse = response
}

// SetError sets the default error to be returned by Run and RunStream.
func (c *Client) SetError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DefaultError = err
}

// SetStreamUpdates sets the default updates to be returned by RunStream.
func (c *Client) SetStreamUpdates(updates []*agent.RunResponseUpdate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DefaultResponseUpdates = updates
}

// GetRunCallCount returns the number of times Run was called.
func (c *Client) GetRunCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.RunCalls)
}

// GetRunStreamCallCount returns the number of times RunStream was called.
func (c *Client) GetRunStreamCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.RunStreamCalls)
}

// GetLastRunCall returns the last call to Run, or nil if no calls were made.
func (c *Client) GetLastRunCall() *RunCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.RunCalls) == 0 {
		return nil
	}
	return &c.RunCalls[len(c.RunCalls)-1]
}

// GetLastRunStreamCall returns the last call to RunStream, or nil if no calls were made.
func (c *Client) GetLastRunStreamCall() *RunStreamCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.RunStreamCalls) == 0 {
		return nil
	}
	return &c.RunStreamCalls[len(c.RunStreamCalls)-1]
}

// WithResponseSequence returns a Client configured to return a sequence of responses.
// Each call to Run returns the next response in the sequence.
func (c *Client) WithResponseSequence(responses ...*agent.RunResponse) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	index := 0
	c.RunFunc = func(ctx context.Context, thread agent.Thread, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
		if index >= len(responses) {
			return responses[len(responses)-1], nil
		}
		resp := responses[index]
		index++
		return resp, nil
	}
	return c
}

// WithToolCalls returns a Client configured to return a response with tool calls
// followed by a final response.
func (c *Client) WithToolCalls(toolCalls []*agent.FunctionCallContent, finalResponse string) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	callCount := 0
	c.RunFunc = func(ctx context.Context, thread agent.Thread, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call returns tool calls
			contents := make([]agent.Content, len(toolCalls))
			for i, tc := range toolCalls {
				contents[i] = tc
			}
			return &agent.RunResponse{
				AgentID:    c.ID(),
				ResponseID: "response-with-tools",
				Messages: []*agent.Message{
					agent.NewMessage(agent.RoleAssistant, contents...),
				},
			}, nil
		}
		// Subsequent calls return final response
		return &agent.RunResponse{
			AgentID:    c.ID(),
			ResponseID: "final-response",
			Messages: []*agent.Message{
				agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: finalResponse}),
			},
		}, nil
	}
	return c
}

// WithStreamingToolCalls returns a Client configured to return streaming updates
// with tool calls followed by a final response.
func (c *Client) WithStreamingToolCalls(toolCalls []*agent.FunctionCallContent, finalResponse string) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	callCount := 0
	c.RunStreamFunc = func(ctx context.Context, thread agent.Thread, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
		callCount++
		currentCall := callCount
		return func(yield func(*agent.RunResponseUpdate, error) bool) {
			if currentCall == 1 {
				// First call returns tool calls
				contents := make([]agent.Content, len(toolCalls))
				for i, tc := range toolCalls {
					contents[i] = tc
				}
				yield(&agent.RunResponseUpdate{
					AgentID:    c.ID(),
					MessageID:  "message-with-tools",
					ResponseID: "response-with-tools",
					Role:       agent.RoleAssistant,
					Contents:   contents,
				}, nil)
				return
			}
			// Subsequent calls return final response
			yield(&agent.RunResponseUpdate{
				AgentID:    c.ID(),
				MessageID:  "final-message",
				ResponseID: "final-response",
				Role:       agent.RoleAssistant,
				Contents:   []agent.Content{&agent.TextContent{Text: finalResponse}},
			}, nil)
		}
	}
	return c
}
