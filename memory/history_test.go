// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestNewInMemoryHistoryProvider_DefaultConfig_RoundTripsHistory(t *testing.T) {
	provider := NewInMemoryHistoryProvider("InMemoryHistoryProvider")
	if provider.SourceID != "InMemoryHistoryProvider" {
		t.Fatalf("expected SourceID=InMemoryHistoryProvider, got %q", provider.SourceID)
	}

	session := NewSession("")
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: session, RequestMessages: []*message.Message{request}, ResponseMessages: []*message.Message{response}}); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session, Messages: []*message.Message{newRequest}})
	if err != nil {
		t.Fatalf("unexpected error loading history: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 history messages, got %d", len(out.Messages))
	}
	if out.Messages[0].String() != "request" || out.Messages[1].String() != "response" {
		t.Fatalf("unexpected output order/content")
	}
	if out.Messages[0].SourceID != "InMemoryHistoryProvider" || out.Messages[1].SourceID != "InMemoryHistoryProvider" {
		t.Fatal("expected history messages to have InMemoryHistoryProvider source ID")
	}
}

func TestNewInMemoryHistoryProvider_SourceID_MapsToSessionStateKey(t *testing.T) {
	session := NewSession("")
	session.Set("custom", inmemoryState{Messages: []*message.Message{message.NewText("custom")}})
	session.Set("InMemoryHistoryProvider", inmemoryState{Messages: []*message.Message{message.NewText("default")}})

	provider := NewInMemoryHistoryProvider("custom")
	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 history message, got %d", len(out.Messages))
	}
	if out.Messages[0].String() != "custom" {
		t.Fatalf("expected custom history message, got %q", out.Messages[0].String())
	}
}

func TestNewInMemoryHistoryProvider_DefaultStoreRequestFilter_ExcludesContextProviderMessages(t *testing.T) {
	provider := NewInMemoryHistoryProvider("k")
	session := NewSession("")

	user := message.NewText("user")
	ctxMsg := message.NewText("ctx")
	ctxMsg.SourceID = "provider-A"

	if err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: session, RequestMessages: []*message.Message{user, ctxMsg}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 || out.Messages[0].String() != "user" {
		t.Fatal("expected default request filter to exclude context-provider messages")
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreRequestMessages(t *testing.T) {
	provider := NewInMemoryHistoryProvider("k")
	provider.StoreRequestFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		return nil, nil
	}
	session := NewSession("")
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: session, RequestMessages: []*message.Message{request}, ResponseMessages: []*message.Message{response}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 || out.Messages[0].String() != "response" {
		t.Fatal("expected only response message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreResponseMessages(t *testing.T) {
	provider := NewInMemoryHistoryProvider("k")
	provider.StoreResponseFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		return nil, nil
	}
	session := NewSession("")
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: session, RequestMessages: []*message.Message{request}, ResponseMessages: []*message.Message{response}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 || out.Messages[0].String() != "request" {
		t.Fatal("expected only request message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessages(t *testing.T) {
	provider := NewInMemoryHistoryProvider("k")
	provider.StoreRequestFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		filtered := make([]*message.Message, 0, len(messages))
		for _, msg := range messages {
			if msg.SourceID == "" || msg.SourceID == "provider-A" {
				filtered = append(filtered, msg)
			}
		}
		return filtered, nil
	}
	session := NewSession("")
	user := message.NewText("user")
	ctxMsg := message.NewText("ctx")
	ctxMsg.SourceID = "provider-A"

	if err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: session, RequestMessages: []*message.Message{user, ctxMsg}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 stored request messages, got %d", len(out.Messages))
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessagesFrom(t *testing.T) {
	provider := NewInMemoryHistoryProvider("k")
	provider.StoreRequestFilter = func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		filtered := make([]*message.Message, 0, len(messages))
		for _, msg := range messages {
			if msg.SourceID == "provider-A" {
				filtered = append(filtered, msg)
			}
		}
		return filtered, nil
	}
	session := NewSession("")
	ctxA := message.NewText("ctx-a")
	ctxA.SourceID = "provider-A"
	ctxB := message.NewText("ctx-b")
	ctxB.SourceID = "provider-B"

	if err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: session, RequestMessages: []*message.Message{ctxA, ctxB}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 || out.Messages[0].String() != "ctx-a" {
		t.Fatal("expected only context messages from allowed SourceIDs to be stored")
	}
}

func TestNewInMemoryHistoryProvider_Invoking_ReturnsErrorOnStateDecodeFailure(t *testing.T) {
	provider := NewInMemoryHistoryProvider("k")
	session := NewSession("")
	session.Set("k", "not-a-history-state")

	_, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: session})
	if err == nil {
		t.Fatal("expected decode error when state cannot be read as inmemoryState")
	}
}

func TestNewInMemoryHistoryProvider_Invoked_ReturnsErrorOnStateDecodeFailure(t *testing.T) {
	provider := NewInMemoryHistoryProvider("k")
	session := NewSession("")
	session.Set("k", "not-a-history-state")

	err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: session, RequestMessages: []*message.Message{message.NewText("r1")}})
	if err == nil {
		t.Fatal("expected decode error when state cannot be read as inmemoryState")
	}
}
