// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"iter"

	"github.com/microsoft/agent-framework-go/agent/internal/agentopt"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/tool"
)

// An Option is a configuration option for an Agent.
//
// Each option must be implemented as its own distinct type.
// [GetOption] and [AllOptions] use the option's type
// to uniquely identify each option.
type Option = agentopt.Option

type sessionOpt struct{ Session }

func (o sessionOpt) Value() any { return o.Session }

// GetOption returns the value stored in opts with the provided setter,
// reporting whether the value is present.
//
// Example usage:
//
//	v, ok := agent.GetOption(opts, agent.WithSession)
func GetOption[T any](opts []Option, setter func(T) Option) (T, bool) {
	return agentopt.GetOption(opts, setter)
}

// AllOptions returns a sequence of all values of type T stored in opts with the provided setter.
//
// Example usage:
//
//	for v := range agent.AllOptions(opts, agent.WithSession) {
//	   // do something with v of type T
//	}
func AllOptions[T any](opts []Option, setter func(T) Option) iter.Seq[T] {
	return agentopt.AllOptions(opts, setter)
}

// WithServiceID sets the service ID for a session.
func WithServiceID(id string) Option {
	return agentopt.WithServiceID(id)
}

// WithStructuredOutput sets the variable pointed to by v to the structured output produced by the agent.
func WithStructuredOutput(v any) Option {
	return agentopt.WithStructuredOutput(v)
}

// WithTool adds a tool to the agent run.
func WithTool(tool tool.Tool) Option {
	return agentopt.WithTool(tool)
}

// WithToolMode sets the tool mode for the agent run.
func WithToolMode(mode tool.ToolMode) Option {
	return agentopt.WithToolMode(mode)
}

// Stream sets whether to use streaming responses during the agent run.
func Stream(stream bool) Option {
	return agentopt.Stream(stream)
}

// WithResponseFormat sets the desired response format for the agent run.
func WithResponseFormat(format format.Format) Option {
	return agentopt.WithResponseFormat(format)
}

// WithSession sets the session to use during the agent run.
func WithSession(session Session) Option {
	return sessionOpt{session}
}

// WithContinuationToken sets the continuation token for resuming and getting the result
// of the agent response identified by this token.
//
// This token is used for background responses that can be activated via [AllowBackgroundResponses]
// if the agent supports them. Streamed background responses, such as those returned by default by [RunStream],
// can be resumed if interrupted. This means that a continuation token obtained from the [RunResponseUpdate] continuation token
// of an update just before the interruption occurred can be passed to this function to resume the stream from
// the point of interruption. Non-streamed background responses, such as those returned by [Run], can be polled for
// completion by obtaining the token from the [RunResponse] continuation token.
func WithContinuationToken(token string) Option {
	return agentopt.WithContinuationToken(token)
}

// AllowBackgroundResponses sets whether to allow background responses during the agent run.
//
// Background responses allow running long-running operations or tasks asynchronously in the background that can be resumed
// by streaming APIs and polled for completion by non-streaming APIs.
//
// When this property is set to true, non-streaming APIs may start a background operation and return an initial
// response with a continuation token. Subsequent calls to the same API should be made in a polling manner with
// the continuation token to get the final result of the operation.
//
// When this property is set to true, streaming APIs may also start a background operation and begin streaming
// response updates until the operation is completed. If the streaming connection is interrupted, the
// continuation token obtained from the last update that has one should be supplied to a subsequent call to the same streaming API
// to resume the stream from the point of interruption and continue receiving updates until the operation is completed.
//
// This property only takes effect if the implementation it's used with supports background responses.
// If the implementation does not support background responses, this property will be ignored.
func AllowBackgroundResponses(allow bool) Option {
	return agentopt.AllowBackgroundResponses(allow)
}
