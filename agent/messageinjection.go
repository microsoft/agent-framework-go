// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"iter"
	"slices"
	"sync"

	"github.com/microsoft/agent-framework-go/message"
)

const pendingInjectedMessagesStateKey = "agent.pendingInjectedMessages"

type pendingInjectedMessagesState struct {
	Messages []*message.Message
}

// MessageInjector queues messages for injection between provider calls.
// Its zero value is ready to use.
type MessageInjector struct {
	mu sync.Mutex
}

func (m *MessageInjector) run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return func(yield func(*ResponseUpdate, error) bool) {
		session, _ := GetOption(options, WithSession)
		if session == nil {
			yield(nil, errors.New("agent: message injection requires a session"))
			return
		}

		currentMessages, err := m.drainInto(session, messages)
		if err != nil {
			yield(nil, err)
			return
		}

		for {
			hasActionableFunctionCall := false
			for update, runErr := range next(ctx, currentMessages, options...) {
				if update != nil && containsActionableFunctionCall(update.Contents) {
					hasActionableFunctionCall = true
				}
				if !yield(update, runErr) || runErr != nil {
					return
				}
			}

			if hasActionableFunctionCall {
				return
			}

			injected, err := m.drain(session)
			if err != nil {
				yield(nil, err)
				return
			}
			if len(injected) == 0 {
				return
			}
			currentMessages = slices.Concat(currentMessages, injected)
		}
	}
}

// EnqueueMessages queues messages for the next provider call associated with session.
func (m *MessageInjector) EnqueueMessages(session *Session, messages ...*message.Message) error {
	if m == nil {
		return errors.New("agent: message injector is nil")
	}
	if session == nil {
		return errors.New("agent: message injection requires a session")
	}
	if len(messages) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := pendingInjectedMessages(session)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		if msg != nil {
			state.Messages = append(state.Messages, msg)
		}
	}
	session.Set(pendingInjectedMessagesStateKey, state)
	return nil
}

// PendingMessages returns a point-in-time snapshot of messages queued for session.
func (m *MessageInjector) PendingMessages(session *Session) ([]*message.Message, error) {
	if m == nil {
		return nil, errors.New("agent: message injector is nil")
	}
	if session == nil {
		return nil, errors.New("agent: message injection requires a session")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := pendingInjectedMessages(session)
	if err != nil {
		return nil, err
	}
	return slices.Clone(state.Messages), nil
}

func (m *MessageInjector) drainInto(session *Session, messages []*message.Message) ([]*message.Message, error) {
	injected, err := m.drain(session)
	if err != nil || len(injected) == 0 {
		return messages, err
	}
	return slices.Concat(messages, injected), nil
}

func (m *MessageInjector) drain(session *Session) ([]*message.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := pendingInjectedMessages(session)
	if err != nil {
		return nil, err
	}
	messages := state.Messages
	state.Messages = nil
	session.Set(pendingInjectedMessagesStateKey, state)
	return messages, nil
}

func pendingInjectedMessages(session *Session) (pendingInjectedMessagesState, error) {
	var state pendingInjectedMessagesState
	_, err := session.Get(pendingInjectedMessagesStateKey, &state)
	return state, err
}

func containsActionableFunctionCall(contents message.Contents) bool {
	return slices.ContainsFunc(contents, func(content message.Content) bool {
		call, ok := content.(*message.FunctionCallContent)
		return ok && call != nil && !call.InformationalOnly
	})
}
