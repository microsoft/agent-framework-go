// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestHistoryProvider_Invoking_WithoutProvide_ReturnsRequestMessages(t *testing.T) {
	request := message.NewText("r1")
	provider := &HistoryProvider{}

	out, err := provider.Invoking(context.Background(), NewSession(""), []*message.Message{request})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0] != request {
		t.Fatal("expected request messages to pass through when no provider is set")
	}
}

func TestHistoryProvider_Invoking_PropagatesProvideError(t *testing.T) {
	expected := errors.New("provide failed")
	provider := &HistoryProvider{
		Provide: func(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error) {
			return nil, expected
		},
	}

	_, err := provider.Invoking(context.Background(), NewSession(""), nil)
	if !errors.Is(err, expected) {
		t.Fatalf("expected provide error, got %v", err)
	}
}

func TestHistoryProvider_Invoking_PropagatesProvideFilterError(t *testing.T) {
	expected := errors.New("filter failed")
	provider := &HistoryProvider{
		Provide: func(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error) {
			return []*message.Message{message.NewText("h1")}, nil
		},
		ProvideFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return nil, expected
		},
	}

	_, err := provider.Invoking(context.Background(), NewSession(""), nil)
	if !errors.Is(err, expected) {
		t.Fatalf("expected provide filter error, got %v", err)
	}
}

func TestHistoryProvider_Invoking_AppendsHistoryBeforeRequestAndSetsSourceMetadata(t *testing.T) {
	history := message.NewText("h1")
	request := message.NewText("r1")

	provider := &HistoryProvider{
		Provide: func(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error) {
			return []*message.Message{history}, nil
		},
	}

	out, err := provider.Invoking(context.Background(), NewSession(""), []*message.Message{request})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0] == history {
		t.Fatal("expected history message to be cloned")
	}
	if out[0].SourceType != "message_history" {
		t.Fatalf("expected SourceType=message_history, got %q", out[0].SourceType)
	}
	if out[0].SourceID != "HistoryProvider" {
		t.Fatalf("expected default SourceID=HistoryProvider, got %q", out[0].SourceID)
	}
	if out[1] != request {
		t.Fatal("expected request message to be appended unchanged")
	}
}

func TestHistoryProvider_Invoking_AppliesProvideFilter(t *testing.T) {
	h1 := message.NewText("h1")
	h2 := message.NewText("h2")
	provider := &HistoryProvider{
		Provide: func(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error) {
			return []*message.Message{h1, h2}, nil
		},
		ProvideFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return messages[:1], nil
		},
	}

	out, err := provider.Invoking(context.Background(), NewSession(""), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message after filtering, got %d", len(out))
	}
	if out[0].String() != "h1" {
		t.Fatalf("expected filtered history message h1, got %q", out[0].String())
	}
}

func TestHistoryProvider_Invoking_UsesCustomSourceID(t *testing.T) {
	provider := &HistoryProvider{
		SourceID: "CustomHistorySource",
		Provide: func(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error) {
			return []*message.Message{message.NewText("h1")}, nil
		},
	}

	out, err := provider.Invoking(context.Background(), NewSession(""), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 history message, got %d", len(out))
	}
	if out[0].SourceID != "CustomHistorySource" {
		t.Fatalf("expected custom source ID, got %q", out[0].SourceID)
	}
}

func TestHistoryProvider_Invoked_SkipsStoreOnInvokeError(t *testing.T) {
	called := false
	provider := &HistoryProvider{
		Store: func(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error {
			called = true
			return nil
		},
	}

	err := provider.Invoked(context.Background(), NewSession(""), nil, nil, errors.New("invoke failed"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected Store not to be called when invokeError is set")
	}
}

func TestHistoryProvider_Invoked_StoresOnInvokeErrorWhenEnabled(t *testing.T) {
	invokeErr := errors.New("invoke failed")
	keep := message.NewText("keep")
	history := message.NewText("history")
	history.SourceType = "message_history"
	response := message.NewText("response")

	called := false
	var storedRequest []*message.Message
	var storedResponse []*message.Message

	provider := &HistoryProvider{
		StoreOnError: true,
		Store: func(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error {
			called = true
			storedRequest = requestMessages
			storedResponse = responseMessages
			return nil
		},
	}

	err := provider.Invoked(context.Background(), NewSession(""), []*message.Message{keep, history}, []*message.Message{response}, invokeErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected Store to be called when StoreOnError is enabled")
	}
	if len(storedRequest) != 1 || storedRequest[0] != keep {
		t.Fatal("expected default store filter to exclude history-sourced request messages")
	}
	if len(storedResponse) != 1 || storedResponse[0] != response {
		t.Fatal("expected response messages to be forwarded to Store")
	}
}

func TestHistoryProvider_Invoked_DefaultStoreFilterExcludesHistoryMessages(t *testing.T) {
	keep := message.NewText("keep")
	history := message.NewText("history")
	history.SourceType = "message_history"

	var storedRequest []*message.Message
	provider := &HistoryProvider{
		Store: func(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error {
			storedRequest = requestMessages
			return nil
		},
	}

	err := provider.Invoked(context.Background(), NewSession(""), []*message.Message{keep, history}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(storedRequest) != 1 {
		t.Fatalf("expected 1 stored request message, got %d", len(storedRequest))
	}
	if storedRequest[0] != keep {
		t.Fatal("expected non-history message to be retained")
	}
}

func TestHistoryProvider_Invoked_UsesCustomStoreFilterOutput(t *testing.T) {
	m1 := message.NewText("m1")
	m2 := message.NewText("m2")

	var storedRequest []*message.Message
	provider := &HistoryProvider{
		StoreFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return []*message.Message{messages[1]}, nil
		},
		Store: func(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error {
			storedRequest = requestMessages
			return nil
		},
	}

	err := provider.Invoked(context.Background(), NewSession(""), []*message.Message{m1, m2}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(storedRequest) != 1 || storedRequest[0] != m2 {
		t.Fatal("expected store to receive messages returned by custom store filter")
	}
}

func TestHistoryProvider_Invoked_PropagatesStoreFilterError(t *testing.T) {
	expected := errors.New("store filter failed")
	provider := &HistoryProvider{
		StoreFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return nil, expected
		},
	}

	err := provider.Invoked(context.Background(), NewSession(""), []*message.Message{message.NewText("r1")}, nil, nil)
	if !errors.Is(err, expected) {
		t.Fatalf("expected store filter error, got %v", err)
	}
}

func TestHistoryProvider_Invoked_PropagatesStoreError(t *testing.T) {
	expected := errors.New("store failed")
	provider := &HistoryProvider{
		Store: func(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error {
			return expected
		},
	}

	err := provider.Invoked(context.Background(), NewSession(""), []*message.Message{message.NewText("r1")}, nil, nil)
	if !errors.Is(err, expected) {
		t.Fatalf("expected store error, got %v", err)
	}
}

func TestNewInMemoryHistoryProvider_DefaultConfig_RoundTripsHistory(t *testing.T) {
	provider := NewInMemoryHistoryProvider(InMemoryHistoryProviderConfig{})
	if provider.SourceID != "InMemoryHistoryProvider" {
		t.Fatalf("expected SourceID=InMemoryHistoryProvider, got %q", provider.SourceID)
	}

	session := NewSession("")
	request := message.NewText("request")
	response := message.NewText("response")

	if err := provider.Invoked(context.Background(), session, []*message.Message{request}, []*message.Message{response}, nil); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	out, err := provider.Invoking(context.Background(), session, []*message.Message{newRequest})
	if err != nil {
		t.Fatalf("unexpected error loading history: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (2 history + 1 request), got %d", len(out))
	}
	if out[0].String() != "request" || out[1].String() != "response" || out[2] != newRequest {
		t.Fatalf("unexpected output order/content")
	}
	if out[0].SourceType != "message_history" || out[1].SourceType != "message_history" {
		t.Fatal("expected history messages to be marked with message_history source type")
	}
	if out[0].SourceID != "InMemoryHistoryProvider" || out[1].SourceID != "InMemoryHistoryProvider" {
		t.Fatal("expected history messages to have InMemoryHistoryProvider source ID")
	}
}

func TestNewInMemoryHistoryProvider_CustomStateKey_IsUsed(t *testing.T) {
	session := NewSession("")
	session.Set("custom", inmemoryState{Messages: []*message.Message{message.NewText("custom")}})
	session.Set("InMemoryHistoryProvider", inmemoryState{Messages: []*message.Message{message.NewText("default")}})

	provider := NewInMemoryHistoryProvider(InMemoryHistoryProviderConfig{StateKey: "custom"})
	out, err := provider.Invoking(context.Background(), session, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 history message, got %d", len(out))
	}
	if out[0].String() != "custom" {
		t.Fatalf("expected custom-key history message, got %q", out[0].String())
	}
}

func TestNewInMemoryHistoryProvider_ForwardsConfigFilters(t *testing.T) {
	provideFilterCalled := false
	storeFilterCalled := false

	provider := NewInMemoryHistoryProvider(InMemoryHistoryProviderConfig{
		ProvideFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			provideFilterCalled = true
			return messages, nil
		},
		StoreFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			storeFilterCalled = true
			return messages, nil
		},
	})

	session := NewSession("")
	if _, err := provider.Invoking(context.Background(), session, nil); err != nil {
		t.Fatalf("unexpected invoking error: %v", err)
	}
	if err := provider.Invoked(context.Background(), session, []*message.Message{message.NewText("r1")}, nil, nil); err != nil {
		t.Fatalf("unexpected invoked error: %v", err)
	}

	if !provideFilterCalled {
		t.Fatal("expected provided ProvideFilter to be used")
	}
	if !storeFilterCalled {
		t.Fatal("expected provided StoreFilter to be used")
	}
}

func TestNewInMemoryHistoryProvider_Invoking_ReturnsErrorOnStateDecodeFailure(t *testing.T) {
	provider := NewInMemoryHistoryProvider(InMemoryHistoryProviderConfig{})
	session := NewSession("")
	session.Set("InMemoryHistoryProvider", "not-a-history-state")

	_, err := provider.Invoking(context.Background(), session, nil)
	if err == nil {
		t.Fatal("expected decode error when state cannot be read as inmemoryState")
	}
}

func TestNewInMemoryHistoryProvider_Invoked_ReturnsErrorOnStateDecodeFailure(t *testing.T) {
	provider := NewInMemoryHistoryProvider(InMemoryHistoryProviderConfig{})
	session := NewSession("")
	session.Set("InMemoryHistoryProvider", "not-a-history-state")

	err := provider.Invoked(context.Background(), session, []*message.Message{message.NewText("r1")}, nil, nil)
	if err == nil {
		t.Fatal("expected decode error when state cannot be read as inmemoryState")
	}
}
