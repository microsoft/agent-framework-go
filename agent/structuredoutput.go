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
		var current structuredOutputMessageKey
		var sawUpdate bool
		for update, err := range next(ctx, messages, options...) {
			if err != nil {
				yield(nil, err)
				return
			}
			if update == nil {
				continue
			}
			if !sawUpdate || current.isDifferent(update) {
				data = data[:0]
				current = structuredOutputMessageKey{}
				sawUpdate = true
			}
			data = append(data, update.String()...)
			current.update(update)
			if !yield(update, nil) {
				return
			}
		}
		if err := m.unmarshal(format, data, v); err != nil {
			yield(nil, err)
			return
		}
	}
}

type structuredOutputMessageKey struct {
	responseID string
	messageID  string
	authorName string
	role       message.Role
}

func (k structuredOutputMessageKey) isDifferent(update *ResponseUpdate) bool {
	return notEmptyNorEqual(update.ResponseID, k.responseID) ||
		notEmptyNorEqual(update.MessageID, k.messageID) ||
		notEmptyNorEqual(update.AuthorName, k.authorName) ||
		notEmptyNorEqual(string(update.Role), string(k.role))
}

func (k *structuredOutputMessageKey) update(update *ResponseUpdate) {
	if update.ResponseID != "" {
		k.responseID = update.ResponseID
	}
	if update.MessageID != "" {
		k.messageID = update.MessageID
	}
	if update.AuthorName != "" {
		k.authorName = update.AuthorName
	}
	if update.Role != "" {
		k.role = update.Role
	}
}
