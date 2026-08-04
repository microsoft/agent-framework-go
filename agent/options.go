// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"iter"
	"reflect"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/tool"
)

// An Option is a configuration option for an Agent.
//
// Each option must be implemented as its own distinct type.
// [GetOption] and [AllOptions] use the option's type
// to uniquely identify each option.
type Option interface {
	Value() any
}

type (
	responseFormatOpt    struct{ ResponseFormat }
	continuationTokenOpt string
	instructionsOpt      string
	serviceIDOpt         string

	toolOpt struct{ tool.Tool }

	toolModeOpt                 tool.ToolMode
	streamOpt                   bool
	allowBackgroundResponsesOpt bool

	structuredOutputOpt struct{ any }
)

func (o responseFormatOpt) Value() any           { return o.ResponseFormat }
func (o streamOpt) Value() any                   { return bool(o) }
func (o continuationTokenOpt) Value() any        { return string(o) }
func (o instructionsOpt) Value() any             { return string(o) }
func (o allowBackgroundResponsesOpt) Value() any { return bool(o) }
func (o toolModeOpt) Value() any                 { return tool.ToolMode(o) }
func (o toolOpt) Value() any                     { return o.Tool }
func (o structuredOutputOpt) Value() any         { return o.any }
func (o serviceIDOpt) Value() any                { return string(o) }

type sessionOpt struct{ *Session }

func (o sessionOpt) Value() any { return o.Session }

// GetOption returns the value stored in opts with the provided setter,
// reporting whether the value is present.
//
// Example usage:
//
//	v, ok := agent.GetOption(opts, agent.WithSession)
func GetOption[T any](opts []Option, setter func(T) Option) (T, bool) {
	var zero T
	setterType := reflect.TypeOf(setter(zero))
	for _, opt := range slices.Backward(opts) {
		if reflect.TypeOf(opt) == setterType {
			v, ok := opt.Value().(T)
			return v, ok
		}
	}
	return zero, false
}

// AllOptions returns a sequence of all values of type T stored in opts with the provided setter.
//
// Example usage:
//
//	for v := range agent.AllOptions(opts, agent.WithSession) {
//	   // do something with v of type T
//	}
func AllOptions[T any](opts []Option, setter func(T) Option) iter.Seq[T] {
	return func(yield func(T) bool) {
		var zero T
		setterType := reflect.TypeOf(setter(zero))
		for _, opt := range opts {
			if reflect.TypeOf(opt) == setterType {
				v, ok := opt.Value().(T)
				if !ok {
					// Skip an option whose value is absent for T (e.g. a
					// WithTool(nil) whose Value() is a nil tool.Tool), mirroring
					// GetOption's graceful (zero, false) handling rather than
					// panicking and aborting the entire collection.
					continue
				}
				if !yield(v) {
					return
				}
			}
		}
	}
}

// WithServiceID sets the service ID for a session.
func WithServiceID(id string) Option {
	return serviceIDOpt(id)
}

// WithStructuredOutput sets the variable pointed to by v to the structured output produced by the agent.
func WithStructuredOutput(v any) Option {
	return structuredOutputOpt{v}
}

// WithTool adds a tool to the agent run.
func WithTool(tool tool.Tool) Option {
	return toolOpt{tool}
}

// WithToolMode sets the tool mode for the agent run.
func WithToolMode(mode tool.ToolMode) Option {
	return toolModeOpt(mode)
}

// Stream sets whether to use streaming responses during the agent run.
func Stream(stream bool) Option {
	return streamOpt(stream)
}

// WithResponseFormat sets the desired response format for the agent run.
func WithResponseFormat(format ResponseFormat) Option {
	return responseFormatOpt{format}
}

// WithSession sets the session to use during the agent run.
func WithSession(session *Session) Option {
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
	return continuationTokenOpt(token)
}

// WithInstructions sets system instructions for an agent run when supported by the provider.
// Use [AllOptions] to access instructions because multiple instruction values can be supplied.
func WithInstructions(instructions string) Option {
	return instructionsOpt(strings.TrimSpace(instructions))
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
	return allowBackgroundResponsesOpt(allow)
}
