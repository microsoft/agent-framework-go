// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
)

type Turn struct {
	Callbacks []func(context.Context, ...agent.Option)
	Responses []Response
}

type ResponseBuilder struct {
	turns []Turn
}

func NewResponseBuilder(firstTurnCallbacks ...func(ctx context.Context, opts ...agent.Option)) *ResponseBuilder {
	return &ResponseBuilder{
		turns: []Turn{{
			Responses: []Response{},
			Callbacks: firstTurnCallbacks,
		}},
	}
}

func (rb *ResponseBuilder) NewTurn(callbacks ...func(ctx context.Context, opts ...agent.Option)) *ResponseBuilder {
	rb.turns = append(rb.turns, Turn{
		Callbacks: callbacks,
		Responses: []Response{},
	})
	return rb
}

func (rb *ResponseBuilder) AddText(text string) *ResponseBuilder {
	rb.Add(&agent.RunResponseUpdate{
		Role: message.RoleAssistant,
		Contents: []message.Content{
			&message.TextContent{Text: text},
		},
	})
	return rb
}

func (rb *ResponseBuilder) AddFunctionCall(callID, name string, arguments string) *ResponseBuilder {
	rb.Add(&agent.RunResponseUpdate{
		Role: message.RoleAssistant,
		Contents: []message.Content{
			&message.FunctionCallContent{
				CallID:    callID,
				Name:      name,
				Arguments: arguments,
			},
		},
	})
	return rb
}

func (rb *ResponseBuilder) Add(resp *agent.RunResponseUpdate) *ResponseBuilder {
	rb.add(Response{Response: resp})
	return rb
}

func (rb *ResponseBuilder) AddError(err error) *ResponseBuilder {
	rb.add(Response{Error: err})
	return rb
}

func (rb *ResponseBuilder) add(resps ...Response) {
	rb.turns[len(rb.turns)-1].Responses = append(rb.turns[len(rb.turns)-1].Responses, resps...)
}

func (rb *ResponseBuilder) Build() []Turn {
	return rb.turns
}

type Response struct {
	Response *agent.RunResponseUpdate
	Error    error
}

type Agent struct {
	Iden          agent.Identity
	Caps          agent.Capabilities
	NewThreadFunc func() memory.Thread
	Responses     []Turn

	currentTurn int
}

func (a *Agent) Identity() agent.Identity {
	if a.Iden == (agent.Identity{}) {
		return agent.NewIdentity("test-agent-id", "TestAgent", "A test agent")
	}
	return a.Iden
}

func (a *Agent) Capabilities() agent.Capabilities {
	return a.Caps
}

func (a *Agent) Run(ctx context.Context, opts ...agent.Option) iter.Seq2[*agent.RunResponseUpdate, error] {
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		defer func() { a.currentTurn++ }()
		if a.currentTurn >= len(a.Responses) {
			panic("no more predefined turns")
		}
		turn := a.Responses[a.currentTurn]
		for _, cb := range turn.Callbacks {
			cb(ctx, opts...)
		}
		for _, resp := range turn.Responses {
			if !yield(resp.Response, resp.Error) {
				return
			}
		}
	}
}

func (a *Agent) NewThread() memory.Thread {
	if a.NewThreadFunc != nil {
		return a.NewThreadFunc()
	}
	return &Thread{}
}

func (a *Agent) UnmarshalThread(data []byte) (memory.Thread, error) {
	return &Thread{}, nil
}

// Thread is a test implementation of the Thread interface
type Thread struct {
	messages []*message.Message
}

func NewThread() *Thread {
	return &Thread{}
}

func (t *Thread) MessagesReceived(ctx context.Context, messages ...*message.Message) error {
	t.messages = append(t.messages, messages...)
	return nil
}

// Middleware is a test implementation of the Middleware interface
type Middleware struct {
	PreResponses  []Turn
	PostResponses []Turn

	called      bool
	currentTurn int
}

func (m *Middleware) Called() bool {
	return m.called
}

func (m *Middleware) Run(ctx context.Context, next agent.Runner, opts ...agent.Option) iter.Seq2[*agent.RunResponseUpdate, error] {
	m.called = true
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		defer func() { m.currentTurn++ }()
		if m.currentTurn < len(m.PreResponses) {
			turn := m.PreResponses[m.currentTurn]
			for _, resp := range turn.Responses {
				if !yield(resp.Response, resp.Error) {
					return
				}
			}
		}
		for update, err := range next.Run(ctx, opts...) {
			if !yield(update, err) {
				return
			}
		}
		if m.currentTurn < len(m.PostResponses) {
			turn := m.PostResponses[m.currentTurn]
			for _, resp := range turn.Responses {
				if !yield(resp.Response, resp.Error) {
					return
				}
			}
		}
	}
}

type Runner struct {
	Responses []Turn

	currentTurn int
}

func (r *Runner) Run(ctx context.Context, opts ...agent.Option) iter.Seq2[*agent.RunResponseUpdate, error] {
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		defer func() { r.currentTurn++ }()
		if r.currentTurn >= len(r.Responses) {
			panic("no more predefined turns")
		}
		turn := r.Responses[r.currentTurn]
		for _, cb := range turn.Callbacks {
			if cb != nil {
				cb(ctx, opts...)
			}
		}
		for _, resp := range turn.Responses {
			if !yield(resp.Response, resp.Error) {
				return
			}
		}
	}
}

// Tool is a test implementation of the Tool interface
type Tool struct {
	name        string
	description string
}

func NewTool(name, description string) *Tool {
	return &Tool{
		name:        name,
		description: description,
	}
}

func (t *Tool) Name() string {
	return t.name
}

func (t *Tool) Description() string {
	return t.description
}
