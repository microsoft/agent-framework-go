// Copyright (c) Microsoft. All rights reserved.

package agentopt

import (
	"iter"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/tool"
)

// An Option is a configuration option for an Agent.
//
// Each option must be implemented as its own distinct type.
// [Get] and [All] use the option's type
// to uniquely identify each option.
type Option interface {
	Value() any
}

// A RunOption configures the behavior of an Agent during a Run.
type RunOption interface {
	Option

	RunOption()
}

// A NewThreadOption configures the behavior of a new Thread created by an Agent.
type NewThreadOption interface {
	Option

	NewThreadOption()
}

type (
	responseFormatOpt    struct{ format.Format }
	threadOpt            struct{ memory.Thread }
	continuationTokenOpt string

	toolOpt struct{ tool.Tool }

	toolModeOpt                 tool.ToolMode
	streamOpt                   bool
	allowBackgroundResponsesOpt bool
)

func (responseFormatOpt) RunOption()           {}
func (threadOpt) RunOption()                   {}
func (streamOpt) RunOption()                   {}
func (continuationTokenOpt) RunOption()        {}
func (allowBackgroundResponsesOpt) RunOption() {}
func (toolOpt) RunOption()                     {}
func (toolModeOpt) RunOption()                 {}

func (o responseFormatOpt) Value() any           { return o.Format }
func (o threadOpt) Value() any                   { return o.Thread }
func (o streamOpt) Value() any                   { return bool(o) }
func (o continuationTokenOpt) Value() any        { return string(o) }
func (o allowBackgroundResponsesOpt) Value() any { return bool(o) }
func (o toolModeOpt) Value() any                 { return tool.ToolMode(o) }
func (o toolOpt) Value() any                     { return o.Tool }

// Tool adds a tool to the agent run.
func Tool(tool tool.Tool) RunOption {
	return toolOpt{tool}
}

// ToolMode sets the tool mode for the agent run.
func ToolMode(mode tool.ToolMode) RunOption {
	return toolModeOpt(mode)
}

// Stream sets whether to use streaming responses during the agent run.
func Stream(stream bool) RunOption {
	return streamOpt(stream)
}

// ResponseFormat sets the desired response format for the agent run.
func ResponseFormat(format format.Format) RunOption {
	return responseFormatOpt{format}
}

// Thread sets the thread to use during the agent run.
func Thread(thread memory.Thread) RunOption {
	return threadOpt{thread}
}

// ContinuationToken sets the continuation token for resuming and getting the result
// of the agent response identified by this token.
//
// This token is used for background responses that can be activated via [AllowBackgroundResponses]
// if the agent supports them. Streamed background responses, such as those returned by default by [RunStream],
// can be resumed if interrupted. This means that a continuation token obtained from the [RunResponseUpdate] continuation token
// of an update just before the interruption occurred can be passed to this function to resume the stream from
// the point of interruption. Non-streamed background responses, such as those returned by [Run], can be polled for
// completion by obtaining the token from the [RunResponse] continuation token.
func ContinuationToken(token string) RunOption {
	return continuationTokenOpt(token)
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
func AllowBackgroundResponses(allow bool) RunOption {
	return allowBackgroundResponsesOpt(allow)
}

// Get returns the value stored in opts with the provided setter,
// reporting whether the value is present.
func Get[T any, O Option](opts []O, setter func(T) O) (T, bool) {
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

// All returns a sequence of all values stored in opts with the provided setter.
func All[T any](opts []RunOption, setter func(T) RunOption) iter.Seq[T] {
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
