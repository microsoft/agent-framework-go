// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestContextProvider_Invoking_WithoutProvide_ReturnsNoAdditions(t *testing.T) {
	request := message.NewText("r1")
	provider := &agent.ContextProvider{SourceID: "ctx"}

	messages, options, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agent.NewSession("")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0] != request {
		t.Fatal("expected original messages when no provider is set")
	}
	if len(options) != 1 {
		t.Fatalf("expected original options when no provider is set, got %d", len(options))
	}
}

func TestContextProvider_Invoking_PanicsWithoutSourceID(t *testing.T) {
	provider := &agent.ContextProvider{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_, _, _ = provider.BeforeRun(t.Context(), nil, agent.WithSession(agent.NewSession("")))
}

func TestContextProvider_Invoking_PassesAllMessagesToProvide(t *testing.T) {
	external := message.NewText("external")
	history := message.NewText("history")
	history.SourceID = "other"

	var captured []*message.Message
	provider := &agent.ContextProvider{
		SourceID: "ctx",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			captured = messages
			return messages, options, nil
		},
	}

	_, _, err := provider.BeforeRun(t.Context(), []*message.Message{external, history}, agent.WithSession(agent.NewSession("")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(captured) != 2 || captured[0] != external || captured[1] != history {
		t.Fatal("expected Provide to receive all input messages by default")
	}
}

func TestContextProvider_Invoking_PropagatesProvideError(t *testing.T) {
	expected := errors.New("provide failed")
	provider := &agent.ContextProvider{
		SourceID: "ctx",
		Provide: func(context.Context, []*message.Message, ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return nil, nil, expected
		},
	}

	_, _, err := provider.BeforeRun(t.Context(), []*message.Message{message.NewText("r1")}, agent.WithSession(agent.NewSession("")))
	if !errors.Is(err, expected) {
		t.Fatalf("expected Provide error, got %v", err)
	}
}

func TestContextProvider_Invoking_ReturnsProvidedMessagesAndSetsSourceID(t *testing.T) {
	provided := message.NewText("ctx")
	request := message.NewText("request")

	provider := &agent.ContextProvider{
		SourceID: "ctx",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return append(messages, provided), options, nil
		},
	}

	messages, _, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agent.NewSession("")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected original and provided messages, got %d", len(messages))
	}
	if messages[0] != request {
		t.Fatal("expected original message to be preserved first")
	}
	if messages[1] == provided {
		t.Fatal("expected provided message to be cloned")
	}
	if messages[1].SourceID != "ctx" {
		t.Fatalf("expected SourceID=ctx, got %q", messages[1].SourceID)
	}
}

func TestContextProvider_Invoking_SetsSourceIDOnPrependedMessages(t *testing.T) {
	provided := message.NewText("ctx")
	request := message.NewText("request")

	provider := &agent.ContextProvider{
		SourceID: "ctx",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			out := make([]*message.Message, 0, len(messages)+1)
			out = append(out, provided)
			out = append(out, messages...)
			return out, options, nil
		},
	}

	messages, _, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agent.NewSession("")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected provided and original messages, got %d", len(messages))
	}
	if messages[0] == provided {
		t.Fatal("expected prepended message to be cloned")
	}
	if messages[0].SourceID != "ctx" {
		t.Fatalf("expected SourceID=ctx, got %q", messages[0].SourceID)
	}
	if messages[1] != request {
		t.Fatal("expected original message to be preserved")
	}
}

func TestContextProvider_Invoking_UsesCustomSourceID(t *testing.T) {
	provider := &agent.ContextProvider{
		SourceID: "CustomContextSource",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return append(messages, message.NewText("ctx")), options, nil
		},
	}

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(agent.NewSession("")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 provided message, got %d", len(messages))
	}
	if messages[0].SourceID != "CustomContextSource" {
		t.Fatalf("expected custom source ID, got %q", messages[0].SourceID)
	}
}

func TestContextProvider_Invoked_PanicsWithoutSourceID(t *testing.T) {
	provider := &agent.ContextProvider{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = provider.AfterRun(t.Context(), nil, nil, agent.WithSession(agent.NewSession("")))
}

func TestContextProvider_Invoked_CallsStoreAndExcludesSameProviderRequestMessagesByDefault(t *testing.T) {
	req1 := message.NewText("request1")
	req2 := message.NewText("request2")
	req2.SourceID = "ctx"
	resp := message.NewText("response")

	called := false
	var storedRequest []*message.Message
	var storedResponse []*message.Message

	provider := &agent.ContextProvider{
		SourceID: "ctx",
		Store: func(_ context.Context, requestMessages, responseMessages []*message.Message, _ ...agent.Option) error {
			called = true
			storedRequest = requestMessages
			storedResponse = responseMessages
			return nil
		},
	}

	err := provider.AfterRun(t.Context(), []*message.Message{req1, req2}, []*message.Message{resp}, agent.WithSession(agent.NewSession("")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected Store to be called")
	}
	if len(storedRequest) != 1 || storedRequest[0] != req1 {
		t.Fatal("expected default request filter to exclude same-provider messages")
	}
	if len(storedResponse) != 1 || storedResponse[0] != resp {
		t.Fatal("expected response messages to pass through unchanged")
	}
}

func TestContextProvider_Invoked_PropagatesStoreError(t *testing.T) {
	expected := errors.New("store failed")
	provider := &agent.ContextProvider{
		SourceID: "ctx",
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			return expected
		},
	}

	err := provider.AfterRun(t.Context(), []*message.Message{message.NewText("r1")}, nil, agent.WithSession(agent.NewSession("")))
	if !errors.Is(err, expected) {
		t.Fatalf("expected store error, got %v", err)
	}
}

func TestContextProvider_InvokingContext_ReturnsProvidedFields(t *testing.T) {
	request := message.NewText("request")
	inputTool := functool.MustNew(functool.Config{Name: "input_tool"}, func(_ tool.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
	providedMsg := message.NewText("provided")
	providedTool := functool.MustNew(functool.Config{Name: "provided_tool"}, func(_ tool.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, nil
	})

	provider := &agent.ContextProvider{
		SourceID: "ctx",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return append(messages, providedMsg), append(options, agent.WithTool(providedTool)), nil
		},
	}

	messages, options, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agent.NewSession("")), agent.WithTool(inputTool))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected original and provided messages, got %d", len(messages))
	}
	if messages[0] != request {
		t.Fatal("expected original message to be preserved first")
	}
	if messages[1].String() != "provided" {
		t.Fatal("expected provided message to be appended")
	}
	if messages[1].SourceID != "ctx" {
		t.Fatal("expected provided message to be source-stamped with provider SourceID")
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	if len(tools) != 2 {
		t.Fatalf("expected input and provided tools, got %d", len(tools))
	}
	if got := []string{tools[0].Name(), tools[1].Name()}; !slices.Equal(got, []string{"input_tool", "provided_tool"}) {
		t.Fatalf("expected input and provided tools, got %v", got)
	}
}
