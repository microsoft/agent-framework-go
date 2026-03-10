// Copyright (c) Microsoft. All rights reserved.

package memory

import (
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
		Provide: func(ctx BeforeRunContext) (Context, error) {
			var state inmemoryState
			if _, err := ctx.Session.Get(sourceID, &state); err != nil {
				return Context{}, err
			}
			return Context{Messages: state.Messages}, nil
		},
		Store: func(ctx AfterRunContext) error {
			var state inmemoryState
			if _, err := ctx.Session.Get(sourceID, &state); err != nil {
				return err
			}
			state.Messages = append(state.Messages, ctx.RequestMessages...)
			state.Messages = append(state.Messages, ctx.ResponseMessages...)
			ctx.Session.Set(sourceID, state)
			return nil
		},
	}
}

type inmemoryState struct {
	Messages []*message.Message `json:"messages,omitempty"`
}
