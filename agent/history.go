// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

const defaultInMemoryHistorySourceID = "in-memory"

// NewInMemoryHistoryProvider creates a context provider that stores conversation history in the session.
// If sourceID is empty, it defaults to "in-memory".
func NewInMemoryHistoryProvider(sourceID string) *ContextProvider {
	if sourceID == "" {
		sourceID = defaultInMemoryHistorySourceID
	}
	return &ContextProvider{
		SourceID:           sourceID,
		StoreRequestFilter: messagefilter.ExternalOnly,
		Provide: func(_ context.Context, msgs []*message.Message, options ...Option) ([]*message.Message, []Option, error) {
			session, _ := GetOption(options, WithSession)
			if session == nil {
				return msgs, options, nil
			}
			var state inmemoryState
			if _, err := session.Get(sourceID, &state); err != nil {
				return nil, nil, err
			}
			if len(state.Messages) == 0 {
				return msgs, options, nil
			}
			messages := make([]*message.Message, 0, len(state.Messages)+len(msgs))
			messages = append(messages, state.Messages...)
			messages = append(messages, msgs...)
			return messages, options, nil
		},
		Store: func(_ context.Context, requestMessages, responseMessages []*message.Message, options ...Option) error {
			session, _ := GetOption(options, WithSession)
			if session == nil {
				return nil
			}
			var state inmemoryState
			if _, err := session.Get(sourceID, &state); err != nil {
				return err
			}
			state.Messages = append(state.Messages, requestMessages...)
			state.Messages = append(state.Messages, responseMessages...)
			session.Set(sourceID, state)
			return nil
		},
	}
}

type inmemoryState struct {
	Messages []*message.Message `json:"messages,omitempty"`
}
