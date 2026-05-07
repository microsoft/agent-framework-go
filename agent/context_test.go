// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestContextProvider_Invoking_WithoutProvide_ReturnsNoAdditions(t *testing.T) {
	request := message.NewText("r1")
	provider := &agent.ContextProvider{SourceID: "ctx"}

	messages, options, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
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

func TestContextProvider_Middleware_PanicsWithNilProvider(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()

	var provider *agent.ContextProvider
	_ = provider.Middleware()
}

func TestContextProviderMiddleware_Run_ProviderOptionsEnrichTools(t *testing.T) {
	baselineTool := stubTool{name: "baseline"}
	providerTool := stubTool{name: "provider"}
	var capturedTools []tool.Tool
	provider := &agent.ContextProvider{
		SourceID: "provider-a",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return messages, append(options, agent.WithTool(providerTool)), nil
		},
	}

	_, err := collectContextProviderMiddlewareResponse(provider.Middleware().Run(
		func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			capturedTools = slices.Collect(agent.AllOptions(opts, agent.WithTool))
			return contextProviderMiddlewareSingleUpdate("ok")
		},
		context.Background(),
		[]*message.Message{message.NewText("hello")},
		agent.WithSession(agenttest.CreateSession()),
		agent.WithTool(baselineTool),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := []string{capturedTools[0].Name(), capturedTools[1].Name()}; !slices.Equal(got, []string{"baseline", "provider"}) {
		t.Fatalf("expected tools [baseline provider], got %v", got)
	}
}

func TestContextProviderMiddleware_Run_SharedOptions_ProviderToolsDoNotAccumulateAcrossCalls(t *testing.T) {
	toolCounts := make([]int, 0, 3)
	provider := &agent.ContextProvider{
		SourceID: "provider-a",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return messages, append(options, agent.WithTool(stubTool{name: "provider"})), nil
		},
	}
	sharedOptions := []agent.Option{
		agent.WithSession(agenttest.CreateSession()),
		agent.WithTool(stubTool{name: "baseline"}),
	}

	for range 3 {
		_, err := collectContextProviderMiddlewareResponse(provider.Middleware().Run(
			func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				toolCounts = append(toolCounts, len(slices.Collect(agent.AllOptions(opts, agent.WithTool))))
				return contextProviderMiddlewareSingleUpdate("ok")
			},
			context.Background(),
			[]*message.Message{message.NewText("hello")},
			sharedOptions...,
		))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if !slices.Equal(toolCounts, []int{2, 2, 2}) {
		t.Fatalf("expected tool counts [2 2 2], got %v", toolCounts)
	}
}

func TestContextProviderMiddleware_Run_SharedOptions_OriginalToolsNotMutated(t *testing.T) {
	baselineTool := stubTool{name: "baseline"}
	provider := &agent.ContextProvider{
		SourceID: "provider-a",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return messages, append(options, agent.WithTool(stubTool{name: "provider"})), nil
		},
	}
	sharedOptions := []agent.Option{
		agent.WithSession(agenttest.CreateSession()),
		agent.WithTool(baselineTool),
	}

	_, err := collectContextProviderMiddlewareResponse(provider.Middleware().Run(
		func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return contextProviderMiddlewareSingleUpdate("ok")
		},
		context.Background(),
		[]*message.Message{message.NewText("hello")},
		sharedOptions...,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	originalTools := slices.Collect(agent.AllOptions(sharedOptions, agent.WithTool))
	if len(originalTools) != 1 {
		t.Fatalf("expected original shared options to keep 1 tool, got %d", len(originalTools))
	}
	if originalTools[0].Name() != baselineTool.Name() {
		t.Fatalf("expected original shared options to preserve baseline tool, got %q", originalTools[0].Name())
	}
}

func TestContextProviderMiddleware_Run_PassesResponseMessagesWithServiceManagedSession(t *testing.T) {
	session := agenttest.CreateSession()
	session.SetServiceID("server-managed")
	var storedResponseMessages []*message.Message
	provider := &agent.ContextProvider{
		SourceID: "provider-a",
		Store: func(_ context.Context, _ []*message.Message, responseMessages []*message.Message, _ ...agent.Option) error {
			storedResponseMessages = responseMessages
			return nil
		},
	}

	_, err := collectContextProviderMiddlewareResponse(provider.Middleware().Run(
		func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return contextProviderMiddlewareSingleUpdate("ok")
		},
		context.Background(),
		[]*message.Message{message.NewText("hello")},
		agent.WithSession(session),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := messageStrings(storedResponseMessages); !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("stored response messages = %v, want [ok]", got)
	}
}

func contextProviderMiddlewareSingleUpdate(text string) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: text}}}, nil)
	}
}

func collectContextProviderMiddlewareResponse(seq iter.Seq2[*agent.ResponseUpdate, error]) (*agent.Response, error) {
	var resp agent.Response
	for update, err := range seq {
		if err != nil {
			return nil, err
		}
		resp.Update(update)
	}
	resp.Coalesce()
	return &resp, nil
}

func TestContextProvider_Invoking_PanicsWithoutSourceID(t *testing.T) {
	provider := &agent.ContextProvider{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_, _, _ = provider.BeforeRun(t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
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

	_, _, err := provider.BeforeRun(t.Context(), []*message.Message{external, history}, agent.WithSession(agenttest.CreateSession()))
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

	_, _, err := provider.BeforeRun(t.Context(), []*message.Message{message.NewText("r1")}, agent.WithSession(agenttest.CreateSession()))
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

	messages, _, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
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

	messages, _, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
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

	messages, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
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
	_ = provider.AfterRun(t.Context(), nil, nil, agent.WithSession(agenttest.CreateSession()))
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

	err := provider.AfterRun(t.Context(), []*message.Message{req1, req2}, []*message.Message{resp}, agent.WithSession(agenttest.CreateSession()))
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

	err := provider.AfterRun(t.Context(), []*message.Message{message.NewText("r1")}, nil, agent.WithSession(agenttest.CreateSession()))
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

	messages, options, err := provider.BeforeRun(t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()), agent.WithTool(inputTool))
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
