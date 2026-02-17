// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"slices"

	"github.com/microsoft/agent-framework-go/message"
)

var _ ContextProvider = (*InMemoryMessageHistoryProvider)(nil)

// InMemoryMessageHistoryProvider is an in-memory implementation of the ContextProvider interface.
type InMemoryMessageHistoryProvider struct {
	Messages []*message.Message
}

func (s *InMemoryMessageHistoryProvider) Invoking(ctx *InvokingContext) (*Context, error) {
	return &Context{
		Messages: slices.Clone(s.Messages),
	}, nil
}

func (s *InMemoryMessageHistoryProvider) Invoked(ctx *InvokedContext) error {
	if ctx.InvokeError != nil {
		return nil
	}
	s.Messages = append(s.Messages, ctx.RequestMessages...)
	s.Messages = append(s.Messages, ctx.ResponseMessages...)
	return nil
}
