// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"context"
	"encoding/json"

	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
)

var _ memory.Session = (*Session)(nil)

type Session struct {
	ConversationID  string
	MessageStore    memory.MessageStore
	ContextProvider memory.ContextProvider
}

func newSessionFromJSON(data []byte, newMessageStore func() memory.MessageStore, newContextProvider func() memory.ContextProvider) (*Session, error) {
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
	session := &Session{
		ConversationID:  tmp.ConversationID,
		ContextProvider: tmp.ContextProvider,
	}
	if tmp.ConversationID != "" {
		// Since we have an ID, we should not have a chat message store and we can return here.
		return session, nil
	}

	if newMessageStore != nil {
		session.MessageStore = newMessageStore()
	} else {
		session.MessageStore = &memory.InMemoryMessageStore{}
	}
	if err := json.Unmarshal(tmp.MessageStore, session.MessageStore); err != nil {
		return nil, err
	}
	return session, nil
}

func (t *Session) MarshalBinary() (data []byte, err error) {
	return json.Marshal(t)
}

func (t *Session) messagesReceived(ctx context.Context, messages ...*message.Message) error {
	if t.ConversationID != "" {
		// If the session messages are stored in the service
		// there is nothing to do here, since invoking the
		// service should already update the session.
		return nil
	}
	if t.MessageStore == nil {
		// If there is no conversation id, and no store we
		// can create a default in memory store and add messages to it.
		t.MessageStore = &memory.InMemoryMessageStore{}
	}
	return t.MessageStore.Add(ctx, messages...)
}
