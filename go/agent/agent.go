// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"
	"strings"
	"time"

	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/param"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

// RunContext contains context for agent execution.
type RunContext struct {
	context.Context
	Thread  memory.Thread
	Options *RunOptions
}

// GetContext returns the context.Context from the RunContext, or a background context if nil.
func (ctx *RunContext) GetContext() context.Context {
	if ctx == nil || ctx.Context == nil {
		return context.Background()
	}
	return ctx.Context
}

// GetThread returns the memory.Thread from the RunContext, or nil if none.
func (ctx *RunContext) GetThread() memory.Thread {
	if ctx == nil {
		return nil
	}
	return ctx.Thread
}

// GetOptions returns the RunOptions from the RunContext, or nil if none.
func (ctx *RunContext) GetOptions() *RunOptions {
	if ctx == nil {
		return nil
	}
	return ctx.Options
}

type RunOptions struct {
	ContinuationToken        any
	AllowBackgroundResponses param.Opt[bool]

	Options any
}

type Agent interface {
	ID() string
	Name() string
	Description() string

	Run(ctx *RunContext, messages ...*message.Message) (*RunResponse, error)
	RunStream(ctx *RunContext, messages ...*message.Message) iter.Seq2[*RunResponseUpdate, error]

	NewThread() memory.Thread
	UnmarshalThread(data []byte) (memory.Thread, error)
}

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

// RunResponse represents the result of an agent execution.
type RunResponse struct {
	RawRepresentation    any
	AdditionalProperties map[string]any
	ID                   string
	AgentID              string
	CreatedAt            time.Time
	Usage                *message.UsageDetails
	Messages             []*message.Message
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

func (r *RunResponse) UserInputRequests() iter.Seq[message.Content] {
	return func(yield func(message.Content) bool) {
		for _, msg := range r.Messages {
			for _, c := range msg.Contents {
				switch c := c.(type) {
				case *message.FunctionApprovalRequestContent:
					if !yield(c) {
						return
					}
				}
			}
		}
	}
}

// RunResponseUpdate represents a streaming update from an agent execution.
type RunResponseUpdate struct {
	RawRepresentation    any
	AdditionalProperties map[string]any
	AgentID              string
	MessageID            string
	ResponseID           string
	AuthorName           string
	Role                 message.Role
	CreatedAt            time.Time
	Contents             []message.Content
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

func (r *RunResponseUpdate) UserInputRequests() iter.Seq[message.Content] {
	return func(yield func(message.Content) bool) {
		for _, c := range r.Contents {
			switch c := c.(type) {
			case *message.FunctionApprovalRequestContent:
				if !yield(c) {
					return
				}
			}
		}
	}
}

// FuncTool creates a function tool that invokes the given agent.
func FuncTool(agent Agent, name, description string, thread memory.Thread) tool.FuncTool {
	type funcToolIn struct {
		Query string `jsonschema:"input query to invoke the agent"`
	}
	return functool.MustNew(&functool.Func{
		Name:        agent.Name(),
		Description: agent.Description(),
	}, func(ctx context.Context, in funcToolIn) (string, error) {
		resp, err := agent.Run(&RunContext{
			Context: ctx,
			Thread:  thread,
		}, &message.Message{
			Role:     message.RoleUser,
			Contents: []message.Content{&message.TextContent{Text: in.Query}},
		})
		if err != nil {
			return "", err
		}
		return resp.String(), nil
	})
}
