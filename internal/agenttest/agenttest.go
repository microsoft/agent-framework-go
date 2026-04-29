// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

type Turn struct {
	Callbacks []func(context.Context, []*message.Message, ...agent.Option)
	Responses []Response
}

type ResponseBuilder struct {
	turns []Turn
}

func NewResponseBuilder(firstTurnCallbacks ...func(ctx context.Context, messages []*message.Message, opts ...agent.Option)) *ResponseBuilder {
	return &ResponseBuilder{
		turns: []Turn{{
			Responses: []Response{},
			Callbacks: firstTurnCallbacks,
		}},
	}
}

func (rb *ResponseBuilder) NewTurn(callbacks ...func(ctx context.Context, messages []*message.Message, opts ...agent.Option)) *ResponseBuilder {
	rb.turns = append(rb.turns, Turn{
		Callbacks: callbacks,
		Responses: []Response{},
	})
	return rb
}

func (rb *ResponseBuilder) AddText(text string) *ResponseBuilder {
	rb.Add(&message.ResponseUpdate{
		Role: message.RoleAssistant,
		Contents: []message.Content{
			&message.TextContent{Text: text},
		},
	})
	return rb
}

func (rb *ResponseBuilder) AddFunctionCall(callID, name string, arguments string) *ResponseBuilder {
	rb.Add(&message.ResponseUpdate{
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

func (rb *ResponseBuilder) Add(resp *message.ResponseUpdate) *ResponseBuilder {
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
	Response *message.ResponseUpdate
	Error    error
}

type testagent struct {
	responses []Turn

	currentTurn int
}

func New(responses []Turn) *agent.Agent {
	a := &testagent{
		responses: responses,
	}
	return agent.New(agent.ProviderConfig{
		ProviderName:  "agenttest",
		CreateSession: a.createSession,
		Run:           a.run,
	}, agent.Config{
		ID:          "test-agent-id",
		Name:        "TestAgent",
		Description: "A test agent",
	})
}

func (a *testagent) run(ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		defer func() { a.currentTurn++ }()
		if a.currentTurn >= len(a.responses) {
			panic("no more predefined turns")
		}
		turn := a.responses[a.currentTurn]
		for _, cb := range turn.Callbacks {
			cb(ctx, messages, opts...)
		}
		for _, resp := range turn.Responses {
			if !yield(resp.Response, resp.Error) {
				return
			}
		}
	}
}

func (a *testagent) createSession(_ context.Context, _ agent.Session, opts ...agent.Option) error {
	return nil
}

func CreateSession(options ...agent.Option) agent.Session {
	session, err := New(nil).CreateSession(context.Background(), options...)
	if err != nil {
		panic(err)
	}
	return session
}

func MarshalSession(session agent.Session) ([]byte, error) {
	return New(nil).MarshalSession(context.Background(), session)
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

func (m *Middleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
	m.called = true
	return func(yield func(*message.ResponseUpdate, error) bool) {
		defer func() { m.currentTurn++ }()
		if m.currentTurn < len(m.PreResponses) {
			turn := m.PreResponses[m.currentTurn]
			for _, resp := range turn.Responses {
				if !yield(resp.Response, resp.Error) {
					return
				}
			}
		}
		for update, err := range next(ctx, messages, opts...) {
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

func (r *Runner) Run(ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		defer func() { r.currentTurn++ }()
		if r.currentTurn >= len(r.Responses) {
			panic("no more predefined turns")
		}
		turn := r.Responses[r.currentTurn]
		for _, cb := range turn.Callbacks {
			if cb != nil {
				cb(ctx, messages, opts...)
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
