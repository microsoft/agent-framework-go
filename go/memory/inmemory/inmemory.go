// Copyright (c) Microsoft. All rights reserved.

package inmemory

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
)

var _ memory.MessageStore = (*MessageStore)(nil)

type MessageStore struct {
	Messages []*message.Message
}

func (s *MessageStore) Add(ctx context.Context, msgs ...*message.Message) error {
	s.Messages = append(s.Messages, msgs...)
	return nil
}

func (s *MessageStore) All(ctx context.Context) iter.Seq2[*message.Message, error] {
	return func(yield func(*message.Message, error) bool) {
		for _, msg := range s.Messages {
			if !yield(msg, nil) {
				return
			}
		}
	}
}
