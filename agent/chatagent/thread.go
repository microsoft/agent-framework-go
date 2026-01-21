// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"context"
	"encoding/json"

	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
)

var _ memory.Thread = (*Thread)(nil)

type Thread struct {
	ConversationID  string
	MessageStore    memory.MessageStore
	ContextProvider memory.ContextProvider
}

func newThreadFromJSON(data []byte, newMessageStore func() memory.MessageStore, newContextProvider func() memory.ContextProvider) (*Thread, error) {
	var tmp struct {
		ConversationID  string
		MessageStore    json.RawMessage // delay unmarshaling until we know the ConversationID is empty
		ContextProvider memory.ContextProvider
	}
	if newContextProvider != nil {
		tmp.ContextProvider = newContextProvider()
	}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return nil, err
	}
	thread := &Thread{
		ConversationID:  tmp.ConversationID,
		ContextProvider: tmp.ContextProvider,
	}
	if tmp.ConversationID != "" {
		// Since we have an ID, we should not have a chat message store and we can return here.
		return thread, nil
	}

	if newMessageStore != nil {
		thread.MessageStore = newMessageStore()
	} else {
		thread.MessageStore = &memory.InMemoryMessageStore{}
	}
	if err := json.Unmarshal(tmp.MessageStore, thread.MessageStore); err != nil {
		return nil, err
	}
	return thread, nil
}

func (t *Thread) MessagesReceived(ctx context.Context, messages ...*message.Message) error {
	if t.ConversationID != "" {
		// If the thread messages are stored in the service
		// there is nothing to do here, since invoking the
		// service should already update the thread.
		return nil
	}
	if t.MessageStore == nil {
		// If there is no conversation id, and no store we
		// can create a default in memory store and add messages to it.
		t.MessageStore = &memory.InMemoryMessageStore{}
	}
	return t.MessageStore.Add(ctx, messages...)
}
