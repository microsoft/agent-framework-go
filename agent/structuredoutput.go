// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"iter"

	"github.com/microsoft/agent-framework-go/message"
)

type structuredOutputMiddleware struct {
	format    func(any) (ResponseFormat, error)
	unmarshal func(ResponseFormat, []byte, any) error
}

func (m *structuredOutputMiddleware) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return func(yield func(*ResponseUpdate, error) bool) {
		v, ok := GetOption(options, WithStructuredOutput)
		if !ok || v == nil {
			for update, err := range next(ctx, messages, options...) {
				if !yield(update, err) {
					break
				}
			}
			return
		}
		if m.format == nil || m.unmarshal == nil {
			yield(nil, errors.New("structured output not supported"))
			return
		}
		format, err := m.format(v)
		if err != nil {
			yield(nil, err)
			return
		}
		options = append(options, WithResponseFormat(format))
		var data []byte
		for update, err := range next(ctx, messages, options...) {
			if err != nil {
				yield(nil, err)
				return
			}
			if update == nil {
				continue
			}
			data = append(data, update.String()...)
		}
		if err := m.unmarshal(format, data, v); err != nil {
			yield(nil, err)
			return
		}
	}
}
