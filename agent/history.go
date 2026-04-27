// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

func NewInMemoryHistoryProvider(sourceID string) *ContextProvider {
	if sourceID == "" {
		panic("sourceID is required")
	}
	return &ContextProvider{
		SourceID:           sourceID,
		StoreRequestFilter: messagefilter.ExternalOnly,
		Provide: func(_ context.Context, _ []*message.Message, options ...Option) ([]*message.Message, []Option, error) {
			session, _ := GetOption(options, WithSession)
			var state inmemoryState
			if _, err := session.Get(sourceID, &state); err != nil {
				return nil, nil, err
			}
			return state.Messages, nil, nil
		},
		Store: func(_ context.Context, requestMessages, responseMessages []*message.Message, options ...Option) error {
			session, _ := GetOption(options, WithSession)
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
