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

type Option interface {
	Value() any
}

type (
	responseFormatOpt    struct{ format.Format }
	sessionOpt           struct{ *memory.Session }
	continuationTokenOpt string
	serviceIDOpt         string

	toolOpt struct{ tool.Tool }

	toolModeOpt                 tool.ToolMode
	streamOpt                   bool
	allowBackgroundResponsesOpt bool

	structuredOutputOpt struct{ any }
)

func (o responseFormatOpt) Value() any           { return o.Format }
func (o sessionOpt) Value() any                  { return o.Session }
func (o streamOpt) Value() any                   { return bool(o) }
func (o continuationTokenOpt) Value() any        { return string(o) }
func (o allowBackgroundResponsesOpt) Value() any { return bool(o) }
func (o toolModeOpt) Value() any                 { return tool.ToolMode(o) }
func (o toolOpt) Value() any                     { return o.Tool }
func (o structuredOutputOpt) Value() any         { return o.any }
func (o serviceIDOpt) Value() any                { return string(o) }

func WithServiceID(id string) Option {
	return serviceIDOpt(id)
}

func WithStructuredOutput(v any) Option {
	return structuredOutputOpt{v}
}

func WithTool(tool tool.Tool) Option {
	return toolOpt{tool}
}

func WithToolMode(mode tool.ToolMode) Option {
	return toolModeOpt(mode)
}

func Stream(stream bool) Option {
	return streamOpt(stream)
}

func WithResponseFormat(format format.Format) Option {
	return responseFormatOpt{format}
}

func WithSession(session *memory.Session) Option {
	return sessionOpt{session}
}

func WithContinuationToken(token string) Option {
	return continuationTokenOpt(token)
}

func AllowBackgroundResponses(allow bool) Option {
	return allowBackgroundResponsesOpt(allow)
}

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

func AllOptions[T any](opts []Option, setter func(T) Option) iter.Seq[T] {
	return func(yield func(T) bool) {
		var zero T
		setterType := reflect.TypeOf(setter(zero))
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
