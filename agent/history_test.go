// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
)

func invokeHistoryProvider(provider agent.HistoryProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func invokeHistoryProviderInvoked(provider agent.HistoryProvider, ctx context.Context, requestMessages, responseMessages []*message.Message, options ...agent.Option) error {
	return provider.Invoked(ctx, agent.InvokedContext{RequestMessages: requestMessages, ResponseMessages: responseMessages, Options: options})
}

func TestNewInMemoryHistoryProvider_DefaultConfig_RoundTripsHistory(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})

	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	messages, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{newRequest}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error loading history: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 2 history messages plus request, got %d", len(messages))
	}
	if messages[2] != newRequest {
		t.Fatal("expected original request message to be preserved last")
	}
	if messages[0].String() != "request" || messages[1].String() != "response" {
		t.Fatalf("unexpected output order/content")
	}
	if messages[0].Source.ID != "in-memory" || messages[1].Source.ID != "in-memory" {
		t.Fatal("expected history messages to have in-memory source ID")
	}

	newResponse := message.NewText("new response")
	if err := invokeHistoryProviderInvoked(provider, t.Context(), messages, []*message.Message{newResponse}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing next turn: %v", err)
	}
	messages, err = invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error loading updated history: %v", err)
	}
	if got, want := len(messages), 4; got != want {
		t.Fatalf("expected history to append only new messages, got %d want %d", got, want)
	}
	if messages[0].String() != "request" || messages[1].String() != "response" || messages[2].String() != "new request" || messages[3].String() != "new response" {
		t.Fatalf("unexpected updated history order/content")
	}
}

func TestNewInMemoryHistoryProvider_ProvidePassesOriginalMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "InMemoryHistoryProvider"})
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	messages, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{newRequest}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error loading history: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 2 history messages plus request, got %d", len(messages))
	}
	if messages[2] != newRequest {
		t.Fatal("expected original request message to be preserved last")
	}
	if messages[0].String() != "request" || messages[1].String() != "response" {
		t.Fatalf("unexpected output order/content")
	}
	if messages[0].Source.ID != "InMemoryHistoryProvider" || messages[1].Source.ID != "InMemoryHistoryProvider" {
		t.Fatal("expected history messages to have provider source ID")
	}
}

func TestNewInMemoryHistoryProvider_SourceID_MapsToSessionStateKey(t *testing.T) {
	session := agenttest.CreateSession()
	customProvider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "custom"})
	defaultProvider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "InMemoryHistoryProvider"})

	if err := invokeHistoryProviderInvoked(customProvider, t.Context(), []*message.Message{message.NewText("custom")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing custom history: %v", err)
	}
	if err := invokeHistoryProviderInvoked(defaultProvider, t.Context(), []*message.Message{message.NewText("default")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing default history: %v", err)
	}

	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "custom"})
	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 history message, got %d", len(messages))
	}
	if messages[0].String() != "custom" {
		t.Fatalf("expected custom history message, got %q", messages[0].String())
	}
}

func TestNewInMemoryHistoryProvider_StateKey_CanDifferFromSourceID(t *testing.T) {
	session := agenttest.CreateSession()
	writer := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "writer", StateKey: "history-state"})
	reader := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "reader", StateKey: "history-state"})

	if err := invokeHistoryProviderInvoked(writer, t.Context(), []*message.Message{message.NewText("stored")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	messages, err := invokeHistoryProvider(reader, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading history: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "stored" {
		t.Fatalf("expected stored message, got %v", messageStrings(messages))
	}
	if messages[0].Source.ID != "reader" {
		t.Fatalf("expected reader source ID, got %q", messages[0].Source.ID)
	}
}

func TestNewInMemoryHistoryProvider_StateInitializer_SeedsMissingState(t *testing.T) {
	session := agenttest.CreateSession()
	initializerCalls := 0
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StateInitializer: func(gotSession *agent.Session) []*message.Message {
			if gotSession != session {
				t.Fatalf("expected initializer session %p, got %p", session, gotSession)
			}
			initializerCalls++
			return []*message.Message{message.NewText("seed")}
		},
	})

	messages, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{message.NewText("request")}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading history: %v", err)
	}
	if initializerCalls != 1 {
		t.Fatalf("expected initializer to be called once, got %d", initializerCalls)
	}
	if got := messageStrings(messages); len(got) != 2 || got[0] != "seed" || got[1] != "request" {
		t.Fatalf("messages = %v, want [seed request]", got)
	}
	if messages[0].Source.ID != "k" {
		t.Fatalf("expected seed message source ID k, got %q", messages[0].Source.ID)
	}

	_, err = invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading initialized history: %v", err)
	}
	if initializerCalls != 1 {
		t.Fatalf("expected initialized state to be reused, got %d initializer calls", initializerCalls)
	}
}

func TestNewInMemoryHistoryProvider_DefaultStoreInputRequestMessageFilter_ExcludesHistoryMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "k"})
	session := agenttest.CreateSession()

	user := message.NewText("user")
	historyMsg := message.NewText("history")
	historyMsg.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "k"}
	otherHistoryMsg := message.NewText("other history")
	otherHistoryMsg.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "other"}
	ctxMsg := message.NewText("ctx")
	ctxMsg.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "provider-A"}

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{user, historyMsg, otherHistoryMsg, ctxMsg}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 || messages[0].String() != "user" || messages[1].String() != "ctx" {
		t.Fatal("expected default request filter to exclude all history-provider messages")
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreRequestMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputRequestMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return nil, nil
		},
	})
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "response" {
		t.Fatal("expected only response message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreResponseMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputResponseMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return nil, nil
		},
	})
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "request" {
		t.Fatal("expected only request message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputRequestMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			filtered := make([]*message.Message, 0, len(messages))
			for _, msg := range messages {
				if msg.Source.ID == "" || msg.Source.ID == "provider-A" {
					filtered = append(filtered, msg)
				}
			}
			return filtered, nil
		},
	})
	session := agenttest.CreateSession()
	user := message.NewText("user")
	ctxMsg := message.NewText("ctx")
	ctxMsg.Source = message.Source{ID: "provider-A"}

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{user, ctxMsg}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 stored request messages, got %d", len(messages))
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessagesFrom(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputRequestMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			filtered := make([]*message.Message, 0, len(messages))
			for _, msg := range messages {
				if msg.Source.ID == "provider-A" {
					filtered = append(filtered, msg)
				}
			}
			return filtered, nil
		},
	})
	session := agenttest.CreateSession()
	ctxA := message.NewText("ctx-a")
	ctxA.Source = message.Source{ID: "provider-A"}
	ctxB := message.NewText("ctx-b")
	ctxB.Source = message.Source{ID: "provider-B"}

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{ctxA, ctxB}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "ctx-a" {
		t.Fatal("expected only context messages from allowed SourceIDs to be stored")
	}
}

func TestNewInMemoryHistoryProvider_Invoking_IgnoresUnreadableState(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "k"})
	session := agenttest.CreateSession()
	session.Set("k", "not-a-history-state")
	request := []*message.Message{message.NewText("req")}

	messages, err := invokeHistoryProvider(provider, t.Context(), request, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error when state is unreadable: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "req" {
		t.Fatal("expected unreadable state to be ignored")
	}
}

func TestNewInMemoryHistoryProvider_Invoked_OverwritesUnreadableState(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "k"})
	session := agenttest.CreateSession()
	session.Set("k", "not-a-history-state")
	request := message.NewText("r1")
	response := message.NewText("a1")

	err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error when state is unreadable: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading stored history: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 stored messages, got %d", len(messages))
	}
}
