// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"context"
	"iter"
	"sync"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/agentext"
)

// Client is a configurable stub implementation of Client and streamableClient
// that can be used for testing agent functionality.
type Client struct {
	mu sync.Mutex

	// RunFunc is called when Run is invoked. If nil, uses default behavior.
	RunFunc func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error)

	// RunStreamFunc is called when RunStream is invoked. If nil, uses default behavior.
	RunStreamFunc func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error]

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

// RunCall records a call to Run.
type RunCall struct {
	Ctx      context.Context
	Thread   agent.Thread
	Config   agent.Config
	Opts     *agent.RunOptions
	Messages []*agent.Message
}

// RunStreamCall records a call to RunStream.
type RunStreamCall struct {
	Ctx      context.Context
	Thread   agent.Thread
	Config   agent.Config
	Opts     *agent.RunOptions
	Messages []*agent.Message
}

var _ agent.Client = (*Client)(nil)
var _ agentext.StreamableClient = (*Client)(nil)

// NewClient creates a new Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		DefaultResponse: &agent.RunResponse{
			AgentID:    "agent",
			ResponseID: "response-1",
			Messages: []*agent.Message{
				agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "response"}),
			},
		},
	}
}

// Run implements the Client interface.
func (m *Client) Run(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the call
	m.RunCalls = append(m.RunCalls, RunCall{
		Ctx:      ctx,
		Thread:   thread,
		Config:   config,
		Opts:     opts,
		Messages: messages,
	})

	// Use custom function if provided
	if m.RunFunc != nil {
		return m.RunFunc(ctx, thread, config, opts, messages...)
	}

	// Return error if configured
	if m.DefaultError != nil {
		return nil, m.DefaultError
	}

	// Return default response
	if m.DefaultResponse != nil {
		return m.DefaultResponse, nil
	}

	// Fallback to a minimal response
	return &agent.RunResponse{
		AgentID:    config.ID,
		ResponseID: "response",
		Messages: []*agent.Message{
			agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "response"}),
		},
	}, nil
}

// RunStream implements the streamableClient interface.
func (m *Client) RunStream(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	m.mu.Lock()
	// Record the call
	m.RunStreamCalls = append(m.RunStreamCalls, RunStreamCall{
		Ctx:      ctx,
		Thread:   thread,
		Config:   config,
		Opts:     opts,
		Messages: messages,
	})

	// Capture values needed for the iterator
	runStreamFunc := m.RunStreamFunc
	defaultError := m.DefaultError
	defaultUpdates := m.DefaultResponseUpdates
	configID := config.ID
	m.mu.Unlock()

	// Use custom function if provided
	if runStreamFunc != nil {
		return runStreamFunc(ctx, thread, config, opts, messages...)
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
func (m *Client) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RunCalls = nil
	m.RunStreamCalls = nil
	m.RunFunc = nil
	m.RunStreamFunc = nil
	m.DefaultError = nil
}

// SetResponse sets the default response to be returned by Run.
func (m *Client) SetResponse(response *agent.RunResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DefaultResponse = response
}

// SetError sets the default error to be returned by Run and RunStream.
func (m *Client) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DefaultError = err
}

// SetStreamUpdates sets the default updates to be returned by RunStream.
func (m *Client) SetStreamUpdates(updates []*agent.RunResponseUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DefaultResponseUpdates = updates
}

// GetRunCallCount returns the number of times Run was called.
func (m *Client) GetRunCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RunCalls)
}

// GetRunStreamCallCount returns the number of times RunStream was called.
func (m *Client) GetRunStreamCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RunStreamCalls)
}

// GetLastRunCall returns the last call to Run, or nil if no calls were made.
func (m *Client) GetLastRunCall() *RunCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.RunCalls) == 0 {
		return nil
	}
	return &m.RunCalls[len(m.RunCalls)-1]
}

// GetLastRunStreamCall returns the last call to RunStream, or nil if no calls were made.
func (m *Client) GetLastRunStreamCall() *RunStreamCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.RunStreamCalls) == 0 {
		return nil
	}
	return &m.RunStreamCalls[len(m.RunStreamCalls)-1]
}

// WithResponseSequence returns a Client configured to return a sequence of responses.
// Each call to Run returns the next response in the sequence.
func (m *Client) WithResponseSequence(responses ...*agent.RunResponse) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()

	index := 0
	m.RunFunc = func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
		if index >= len(responses) {
			return responses[len(responses)-1], nil
		}
		resp := responses[index]
		index++
		return resp, nil
	}
	return m
}

// WithToolCalls returns a Client configured to return a response with tool calls
// followed by a final response.
func (m *Client) WithToolCalls(toolCalls []*agent.FunctionCallContent, finalResponse string) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()

	callCount := 0
	m.RunFunc = func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call returns tool calls
			contents := make([]agent.Content, len(toolCalls))
			for i, tc := range toolCalls {
				contents[i] = tc
			}
			return &agent.RunResponse{
				AgentID:    config.ID,
				ResponseID: "response-with-tools",
				Messages: []*agent.Message{
					agent.NewMessage(agent.RoleAssistant, contents...),
				},
			}, nil
		}
		// Subsequent calls return final response
		return &agent.RunResponse{
			AgentID:    config.ID,
			ResponseID: "final-response",
			Messages: []*agent.Message{
				agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: finalResponse}),
			},
		}, nil
	}
	return m
}

// WithStreamingToolCalls returns a Client configured to return streaming updates
// with tool calls followed by a final response.
func (m *Client) WithStreamingToolCalls(toolCalls []*agent.FunctionCallContent, finalResponse string) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()

	callCount := 0
	m.RunStreamFunc = func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
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
					AgentID:    config.ID,
					MessageID:  "message-with-tools",
					ResponseID: "response-with-tools",
					Role:       agent.RoleAssistant,
					Contents:   contents,
				}, nil)
				return
			}
			// Subsequent calls return final response
			yield(&agent.RunResponseUpdate{
				AgentID:    config.ID,
				MessageID:  "final-message",
				ResponseID: "final-response",
				Role:       agent.RoleAssistant,
				Contents:   []agent.Content{&agent.TextContent{Text: finalResponse}},
			}, nil)
		}
	}
	return m
}
