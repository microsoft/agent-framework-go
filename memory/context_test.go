// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestContextProvider_Invoking_WithoutProvide_ReturnsNoAdditions(t *testing.T) {
	request := message.NewText("r1")
	provider := &ContextProvider{SourceID: "ctx"}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: NewSession(""), Messages: []*message.Message{request}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 0 || len(out.Tools) != 0 {
		t.Fatal("expected no additional context when no provider is set")
	}
}

func TestContextProvider_Invoking_PanicsWithoutSourceID(t *testing.T) {
	provider := &ContextProvider{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_, _ = provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: NewSession("")})
}

func TestContextProvider_Invoking_PassesAllMessagesToProvide(t *testing.T) {
	external := message.NewText("external")
	history := message.NewText("history")
	history.SourceID = "other"

	var captured []*message.Message
	provider := &ContextProvider{
		SourceID: "ctx",
		Provide: func(ctx BeforeRunContext) (Context, error) {
			captured = ctx.Messages
			return Context{}, nil
		},
	}

	_, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: NewSession(""), Messages: []*message.Message{external, history}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(captured) != 2 || captured[0] != external || captured[1] != history {
		t.Fatal("expected Provide to receive all input messages by default")
	}
}

func TestContextProvider_Invoking_PropagatesProvideError(t *testing.T) {
	expected := errors.New("provide failed")
	provider := &ContextProvider{
		SourceID: "ctx",
		Provide: func(ctx BeforeRunContext) (Context, error) {
			return Context{}, expected
		},
	}

	_, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: NewSession(""), Messages: []*message.Message{message.NewText("r1")}})
	if !errors.Is(err, expected) {
		t.Fatalf("expected Provide error, got %v", err)
	}
}

func TestContextProvider_Invoking_ReturnsProvidedMessagesAndSetsSourceID(t *testing.T) {
	provided := message.NewText("ctx")
	request := message.NewText("request")

	provider := &ContextProvider{
		SourceID: "ctx",
		Provide: func(ctx BeforeRunContext) (Context, error) {
			return Context{Messages: []*message.Message{provided}}, nil
		},
	}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: NewSession(""), Messages: []*message.Message{request}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 provided message, got %d", len(out.Messages))
	}
	if out.Messages[0] == provided {
		t.Fatal("expected provided message to be cloned")
	}
	if out.Messages[0].SourceID != "ctx" {
		t.Fatalf("expected SourceID=ctx, got %q", out.Messages[0].SourceID)
	}
}

func TestContextProvider_Invoking_UsesCustomSourceID(t *testing.T) {
	provider := &ContextProvider{
		SourceID: "CustomContextSource",
		Provide: func(ctx BeforeRunContext) (Context, error) {
			return Context{Messages: []*message.Message{message.NewText("ctx")}}, nil
		},
	}

	out, err := provider.BeforeRun(BeforeRunContext{Context: context.Background(), Session: NewSession("")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 provided message, got %d", len(out.Messages))
	}
	if out.Messages[0].SourceID != "CustomContextSource" {
		t.Fatalf("expected custom source ID, got %q", out.Messages[0].SourceID)
	}
}

func TestContextProvider_Invoked_PanicsWithoutSourceID(t *testing.T) {
	provider := &ContextProvider{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = provider.AfterRun(AfterRunContext{Context: context.Background(), Session: NewSession("")})
}

func TestContextProvider_Invoked_CallsStoreAndExcludesSameProviderRequestMessagesByDefault(t *testing.T) {
	invokeErr := errors.New("invoke failed")
	req1 := message.NewText("request1")
	req2 := message.NewText("request2")
	req2.SourceID = "ctx"
	resp := message.NewText("response")

	called := false
	var storedRequest []*message.Message
	var storedResponse []*message.Message

	provider := &ContextProvider{
		SourceID: "ctx",
		Store: func(ctx AfterRunContext) error {
			called = true
			storedRequest = ctx.RequestMessages
			storedResponse = ctx.ResponseMessages
			if !errors.Is(ctx.InvokeError, invokeErr) {
				t.Fatalf("expected invoke error to be forwarded")
			}
			return nil
		},
	}

	err := provider.AfterRun(AfterRunContext{
		Context:          context.Background(),
		Session:          NewSession(""),
		RequestMessages:  []*message.Message{req1, req2},
		ResponseMessages: []*message.Message{resp},
		InvokeError:      invokeErr,
	})
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
	provider := &ContextProvider{
		SourceID: "ctx",
		Store: func(ctx AfterRunContext) error {
			return expected
		},
	}

	err := provider.AfterRun(AfterRunContext{Context: context.Background(), Session: NewSession(""), RequestMessages: []*message.Message{message.NewText("r1")}})
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

	provider := &ContextProvider{
		SourceID: "ctx",
		Provide: func(ctx BeforeRunContext) (Context, error) {
			return Context{
				Messages: []*message.Message{providedMsg},
				Tools:    []tool.Tool{providedTool},
			}, nil
		},
	}

	out, err := provider.BeforeRun(BeforeRunContext{
		Context:  context.Background(),
		Session:  NewSession(""),
		Messages: []*message.Message{request},
		Tools:    []tool.Tool{inputTool},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 provided message, got %d", len(out.Messages))
	}
	if out.Messages[0].String() != "provided" {
		t.Fatal("expected only provided message to be returned")
	}
	if out.Messages[0].SourceID != "ctx" {
		t.Fatal("expected provided message to be source-stamped with provider SourceID")
	}
	if len(out.Tools) != 1 || out.Tools[0].Name() != "provided_tool" {
		t.Fatal("expected only provided tools to be returned")
	}
}
