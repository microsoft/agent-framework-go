// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
)

func TestNewInMemoryHistoryProvider_DefaultConfig_RoundTripsHistory(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("")
	if provider.SourceID != "in-memory" {
		t.Fatalf("expected SourceID=in-memory, got %q", provider.SourceID)
	}

	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.AfterRun(t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	messages, _, err := provider.BeforeRun(t.Context(), []*message.Message{newRequest}, agent.WithSession(session))
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
	if messages[0].SourceID != "in-memory" || messages[1].SourceID != "in-memory" {
		t.Fatal("expected history messages to have in-memory source ID")
	}

	newResponse := message.NewText("new response")
	if err := provider.AfterRun(t.Context(), messages, []*message.Message{newResponse}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing next turn: %v", err)
	}
	messages, _, err = provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
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

func TestNewInMemoryHistoryProvider_ProvidePrependsHistory(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("InMemoryHistoryProvider")
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.AfterRun(t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	messages, options, err := provider.Provide(t.Context(), []*message.Message{newRequest}, agent.WithSession(session))
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
	if gotSession, ok := agent.GetOption(options, agent.WithSession); !ok || gotSession != session {
		t.Fatal("expected original session option to be preserved")
	}
}

func TestNewInMemoryHistoryProvider_SourceID_MapsToSessionStateKey(t *testing.T) {
	session := agenttest.CreateSession()
	customProvider := agent.NewInMemoryHistoryProvider("custom")
	defaultProvider := agent.NewInMemoryHistoryProvider("InMemoryHistoryProvider")

	if err := customProvider.AfterRun(t.Context(), []*message.Message{message.NewText("custom")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing custom history: %v", err)
	}
	if err := defaultProvider.AfterRun(t.Context(), []*message.Message{message.NewText("default")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing default history: %v", err)
	}

	provider := agent.NewInMemoryHistoryProvider("custom")
	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
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

func TestNewInMemoryHistoryProvider_DefaultStoreRequestFilter_ExcludesContextProviderMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("k")
	session := agenttest.CreateSession()

	user := message.NewText("user")
	ctxMsg := message.NewText("ctx")
	ctxMsg.SourceID = "provider-A"

	if err := provider.AfterRun(t.Context(), []*message.Message{user, ctxMsg}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "user" {
		t.Fatal("expected default request filter to exclude context-provider messages")
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreRequestMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("k")
	provider.StoreRequestFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		return nil, nil
	}
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.AfterRun(t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "response" {
		t.Fatal("expected only response message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreResponseMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("k")
	provider.StoreResponseFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		return nil, nil
	}
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.AfterRun(t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "request" {
		t.Fatal("expected only request message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("k")
	provider.StoreRequestFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		filtered := make([]*message.Message, 0, len(messages))
		for _, msg := range messages {
			if msg.SourceID == "" || msg.SourceID == "provider-A" {
				filtered = append(filtered, msg)
			}
		}
		return filtered, nil
	}
	session := agenttest.CreateSession()
	user := message.NewText("user")
	ctxMsg := message.NewText("ctx")
	ctxMsg.SourceID = "provider-A"

	if err := provider.AfterRun(t.Context(), []*message.Message{user, ctxMsg}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 stored request messages, got %d", len(messages))
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessagesFrom(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("k")
	provider.StoreRequestFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		filtered := make([]*message.Message, 0, len(messages))
		for _, msg := range messages {
			if msg.SourceID == "provider-A" {
				filtered = append(filtered, msg)
			}
		}
		return filtered, nil
	}
	session := agenttest.CreateSession()
	ctxA := message.NewText("ctx-a")
	ctxA.SourceID = "provider-A"
	ctxB := message.NewText("ctx-b")
	ctxB.SourceID = "provider-B"

	if err := provider.AfterRun(t.Context(), []*message.Message{ctxA, ctxB}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "ctx-a" {
		t.Fatal("expected only context messages from allowed SourceIDs to be stored")
	}
}

func TestNewInMemoryHistoryProvider_Invoking_IgnoresUnreadableState(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("k")
	session := agenttest.CreateSession()
	session.Set("k", "not-a-history-state")
	request := []*message.Message{message.NewText("req")}

	messages, _, err := provider.BeforeRun(t.Context(), request, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error when state is unreadable: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "req" {
		t.Fatal("expected unreadable state to be ignored")
	}
}

func TestNewInMemoryHistoryProvider_Invoked_OverwritesUnreadableState(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider("k")
	session := agenttest.CreateSession()
	session.Set("k", "not-a-history-state")
	request := message.NewText("r1")
	response := message.NewText("a1")

	err := provider.AfterRun(t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error when state is unreadable: %v", err)
	}

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading stored history: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 stored messages, got %d", len(messages))
	}
}
