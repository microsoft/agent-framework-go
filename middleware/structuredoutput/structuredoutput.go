// Copyright (c) Microsoft. All rights reserved.

package structuredoutput

import (
	"context"
	"errors"
	"iter"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
)

type Config struct {
	Format    func(v any) (format.Format, error)
	Unmarshal func(format format.Format, data []byte, v any) error
}

func New(cfg Config) middleware.Middleware {
	return &so{
		Format:    cfg.Format,
		Unmarshal: cfg.Unmarshal,
	}
}

var _ middleware.Middleware = (*so)(nil)

type so struct {
	Format    func(v any) (format.Format, error)
	Unmarshal func(format format.Format, data []byte, v any) error
}

func (a *so) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		v, ok := agentopt.Get(options, agentopt.StructuredOutput)
		if !ok || v == nil {
			// No structured output requested or nil value, just pass through.
			for update, err := range next(ctx, messages, options...) {
				if !yield(update, err) {
					break
				}
			}
			return
		}
		if a.Format == nil || a.Unmarshal == nil {
			yield(nil, errors.New("structured output not supported"))
			return
		}
		format, err := a.Format(v)
		if err != nil {
			yield(nil, err)
			return
		}
		options = append(options, agentopt.ResponseFormat(format))
		var data []byte
		for update, err := range next(ctx, messages, options...) {
			if err != nil {
				yield(nil, err)
				return
			}
			data = append(data, update.String()...)
		}
		if err := a.Unmarshal(format, data, v); err != nil {
			yield(nil, err)
			return
		}
	}
}
