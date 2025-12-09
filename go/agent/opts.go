// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework/go/format"
	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
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
	contextOpt           struct{ context.Context }
	responseFormatOpt    struct{ format.Format }
	threadOpt            struct{ memory.Thread }
	continuationTokenOpt struct{ any }

	messageOpt struct{ *message.Message }

	streamingOpt                bool
	allowBackgroundResponsesOpt bool
)

func (contextOpt) AgentOption()                  {}
func (responseFormatOpt) AgentOption()           {}
func (threadOpt) AgentOption()                   {}
func (streamingOpt) AgentOption()                {}
func (continuationTokenOpt) AgentOption()        {}
func (allowBackgroundResponsesOpt) AgentOption() {}
func (messageOpt) AgentOption()                  {}

func (o contextOpt) Value() any                  { return o.Context }
func (o responseFormatOpt) Value() any           { return o.Format }
func (o threadOpt) Value() any                   { return o.Thread }
func (o streamingOpt) Value() any                { return bool(o) }
func (o continuationTokenOpt) Value() any        { return o.any }
func (o allowBackgroundResponsesOpt) Value() any { return bool(o) }
func (o messageOpt) Value() any                  { return o.Message }

// WithContext sets the context to use during the agent run.
func WithContext(context context.Context) Option {
	return contextOpt{context}
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

// WithContinuationToken sets the continuation token for the agent run.
func WithContinuationToken(token any) Option {
	return continuationTokenOpt{token}
}

// WithAllowBackgroundResponses sets whether to allow background responses during the agent run.
func WithAllowBackgroundResponses(allow bool) Option {
	return allowBackgroundResponsesOpt(allow)
}

// WithMessage adds a message to the agent run.
func WithMessage(message *message.Message) Option {
	return messageOpt{message}
}

// GetOption returns the value stored in opts with the provided setter,
// reporting whether the value is present.
func GetOption[T any](setter func(T) Option, opts ...Option) (T, bool) {
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

// GetOptions returns a sequence of all values stored in opts with the provided setter.
func GetOptions[T any](setter func(T) Option, opts ...Option) iter.Seq[T] {
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
