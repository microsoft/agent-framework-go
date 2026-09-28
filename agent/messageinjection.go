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

		stream, _ := GetOption(options, Stream)
		var priorUsage message.UsageDetails
		var hasPriorUsage bool
		for {
			hasActionableFunctionCall := false
			var conversationID *string
			var response Response
			for update, runErr := range next(ctx, currentMessages, options...) {
				if update != nil {
					if update.ConversationID != nil {
						conversationID = update.ConversationID
					}
					if containsActionableFunctionCall(update.Contents) {
						hasActionableFunctionCall = true
					}
				}
				if stream {
					if !yield(update, runErr) || runErr != nil {
						return
					}
				} else {
					if runErr != nil {
						yield(nil, runErr)
						return
					}
					response.Update(update)
				}
			}

			var injected []*message.Message
			if !hasActionableFunctionCall {
				injected, err = m.drain(session)
				if err != nil {
					yield(nil, err)
					return
				}
			}
			if hasActionableFunctionCall || len(injected) == 0 {
				if !stream {
					// Non-streaming injection returns the final service response,
					// with usage from earlier calls but without their messages or IDs.
					if hasPriorUsage {
						if len(response.Messages) == 0 {
							response.Messages = append(response.Messages, &message.Message{Role: message.RoleAssistant})
						}
						last := response.Messages[len(response.Messages)-1]
						last.Contents = append(last.Contents, &message.UsageContent{Details: priorUsage})
					}
					response.Coalesce()
					for _, update := range response.ToUpdates() {
						if !yield(update, nil) {
							return
						}
					}
				}
				return
			}
			if !stream {
				for content := range response.Contents() {
					if usage, ok := content.(*message.UsageContent); ok {
						priorUsage.Add(usage.Details)
						hasPriorUsage = true
					}
				}
			}
			// Each provider call supplies the history handle for the next call;
			// an absent handle clears the previous request's ID.
			var serviceID string
			if conversationID != nil {
				serviceID = *conversationID
			}
			if currentID, _ := GetOption(options, WithServiceID); currentID != serviceID {
				options = append(slices.Clone(options), WithServiceID(serviceID))
			}
			currentMessages = injected
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
