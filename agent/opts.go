// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"iter"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

// An Option configures the behavior of an Agent during a Run.
//
// Each option must be implemented as its own distinct type.
// [GetOption] and [GetOptions] use the option's type
// to uniquely identify each option.
type Option interface {
	AgentOption()
	Value() any
}

type (
	responseFormatOpt    struct{ format.Format }
	threadOpt            struct{ memory.Thread }
	continuationTokenOpt struct{ any }

	messageOpt    struct{ *message.Message }
	toolOpt       struct{ tool.Tool }
	middlewareOpt struct{ Middleware }

	toolModeOpt                 tool.ToolMode
	streamingOpt                bool
	allowBackgroundResponsesOpt bool
)

func (responseFormatOpt) AgentOption()           {}
func (threadOpt) AgentOption()                   {}
func (streamingOpt) AgentOption()                {}
func (continuationTokenOpt) AgentOption()        {}
func (allowBackgroundResponsesOpt) AgentOption() {}
func (messageOpt) AgentOption()                  {}
func (toolOpt) AgentOption()                     {}
func (toolModeOpt) AgentOption()                 {}
func (middlewareOpt) AgentOption()               {}

func (o responseFormatOpt) Value() any           { return o.Format }
func (o threadOpt) Value() any                   { return o.Thread }
func (o streamingOpt) Value() any                { return bool(o) }
func (o continuationTokenOpt) Value() any        { return o.any }
func (o allowBackgroundResponsesOpt) Value() any { return bool(o) }
func (o messageOpt) Value() any                  { return o.Message }
func (o toolModeOpt) Value() any                 { return tool.ToolMode(o) }
func (o toolOpt) Value() any                     { return o.Tool }
func (o middlewareOpt) Value() any               { return Middleware(o) }

// WithMiddleware adds a middleware to the agent run.
func WithMiddleware(mw Middleware) Option {
	return middlewareOpt{mw}
}

// WithTool adds a tool to the agent run.
func WithTool(tool tool.Tool) Option {
	return toolOpt{tool}
}

// WithToolMode sets the tool mode for the agent run.
func WithToolMode(mode tool.ToolMode) Option {
	return toolModeOpt(mode)
}

// WithStreaming sets whether to use streaming responses during the agent run.
func WithStreaming(streaming bool) Option {
	return streamingOpt(streaming)
}

// WithResponseFormat sets the desired response format for the agent run.
func WithResponseFormat(format format.Format) Option {
	return responseFormatOpt{format}
}

// WithThread sets the thread to use during the agent run.
func WithThread(thread memory.Thread) Option {
	return threadOpt{thread}
}

// WithContinuationToken sets the continuation token for resuming and getting the result
// of the agent response identified by this token.
//
// This token is used for background responses that can be activated via [WithAllowBackgroundResponses]
// if the agent supports them. Streamed background responses, such as those returned by default by [RunStream],
// can be resumed if interrupted. This means that a continuation token obtained from the [RunResponseUpdate] continuation token
// of an update just before the interruption occurred can be passed to this function to resume the stream from
// the point of interruption. Non-streamed background responses, such as those returned by [Run], can be polled for
// completion by obtaining the token from the [RunResponse] continuation token.
func WithContinuationToken(token any) Option {
	return continuationTokenOpt{token}
}

// WithAllowBackgroundResponses sets whether to allow background responses during the agent run.
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
func WithAllowBackgroundResponses(allow bool) Option {
	return allowBackgroundResponsesOpt(allow)
}

// WithMessage adds a message to the agent run.
func WithMessage(message *message.Message) Option {
	return messageOpt{message}
}

// GetOption returns the value stored in opts with the provided setter,
// reporting whether the value is present.
func GetOption[T any](opts []Option, setter func(T) Option) (T, bool) {
	var zero T
	var setterType = reflect.TypeOf(setter(zero))
	for _, opt := range slices.Backward(opts) {
		if reflect.TypeOf(opt) == setterType {
			v, ok := opt.Value().(T)
			return v, ok
		}
	}
	return zero, false
}

func DeleteOption[T any](opts []Option, setter func(T) Option) []Option {
	var zero T
	var setterType = reflect.TypeOf(setter(zero))
	return slices.DeleteFunc(opts, func(opt Option) bool {
		return reflect.TypeOf(opt) == setterType
	})
}

// GetOptions returns a sequence of all values stored in opts with the provided setter.
func GetOptions[T any](opts []Option, setter func(T) Option) iter.Seq[T] {
	return func(yield func(T) bool) {
		var zero T
		var setterType = reflect.TypeOf(setter(zero))
		for _, opt := range opts {
			if reflect.TypeOf(opt) == setterType {
				v, ok := opt.Value().(T)
				if !ok {
					panic("option type mismatch")
				}
				if !yield(v) {
					return
				}
			}
		}
	}
}
