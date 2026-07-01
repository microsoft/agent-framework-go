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
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestContextProvider_Invoking_WithoutProvide_ReturnsNoAdditions(t *testing.T) {
	request := message.NewText("r1")
	provider := agent.NewContextProvider(agent.ContextProviderConfig{SourceID: "ctx"})

	messages, options, err := invokeContextProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
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

	var provider agent.ContextProvider
	_ = agent.ContextProviderMiddleware(provider)
}

func TestContextProviderMiddleware_Run_ProviderOptionsEnrichTools(t *testing.T) {
	baselineTool := stubTool{name: "baseline"}
	providerTool := stubTool{name: "provider"}
	var capturedTools []tool.Tool
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "provider-a",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return nil, []agent.Option{agent.WithTool(providerTool)}, nil
		},
	})

	_, err := collectContextProviderMiddlewareResponse(agent.ContextProviderMiddleware(provider).Run(
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
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "provider-a",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return nil, []agent.Option{agent.WithTool(stubTool{name: "provider"})}, nil
		},
	})
	sharedOptions := []agent.Option{
		agent.WithSession(agenttest.CreateSession()),
		agent.WithTool(stubTool{name: "baseline"}),
	}

	for range 3 {
		_, err := collectContextProviderMiddlewareResponse(agent.ContextProviderMiddleware(provider).Run(
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
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "provider-a",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return nil, []agent.Option{agent.WithTool(stubTool{name: "provider"})}, nil
		},
	})
	sharedOptions := []agent.Option{
		agent.WithSession(agenttest.CreateSession()),
		agent.WithTool(baselineTool),
	}

	_, err := collectContextProviderMiddlewareResponse(agent.ContextProviderMiddleware(provider).Run(
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
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "provider-a",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			storedResponseMessages = invoked.ResponseMessages
			return nil
		},
	})

	_, err := collectContextProviderMiddlewareResponse(agent.ContextProviderMiddleware(provider).Run(
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

func TestContextProviderMiddleware_Run_SkipsDefaultStoreOnRunError(t *testing.T) {
	expected := errors.New("run failed")
	storeCalled := false
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "provider-a",
		Store: func(context.Context, agent.InvokedContext) error {
			storeCalled = true
			return nil
		},
	})

	_, err := collectContextProviderMiddlewareResponse(agent.ContextProviderMiddleware(provider).Run(
		func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(nil, expected)
			}
		},
		context.Background(),
		[]*message.Message{message.NewText("hello")},
		agent.WithSession(agenttest.CreateSession()),
	))
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
	if storeCalled {
		t.Fatal("expected store to be skipped after run error")
	}
}

func TestContextProviderMiddleware_Run_PassesRunErrorToCustomProvider(t *testing.T) {
	expected := errors.New("run failed")
	var invokedErr error
	provider := contextProviderFunc{
		invoking: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return invoking.Messages, invoking.Options, nil
		},
		invoked: func(_ context.Context, invoked agent.InvokedContext) error {
			invokedErr = invoked.Err
			return nil
		},
	}

	_, err := collectContextProviderMiddlewareResponse(agent.ContextProviderMiddleware(provider).Run(
		func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(nil, expected)
			}
		},
		context.Background(),
		[]*message.Message{message.NewText("hello")},
		agent.WithSession(agenttest.CreateSession()),
	))
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
	if !errors.Is(invokedErr, expected) {
		t.Fatalf("expected invoked error %v, got %v", expected, invokedErr)
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

func invokeContextProvider(provider agent.ContextProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func invokeContextProviderInvoked(provider agent.ContextProvider, ctx context.Context, requestMessages, responseMessages []*message.Message, options ...agent.Option) error {
	return provider.Invoked(ctx, agent.InvokedContext{RequestMessages: requestMessages, ResponseMessages: responseMessages, Options: options})
}

type contextProviderFunc struct {
	invoking func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error)
	invoked  func(context.Context, agent.InvokedContext) error
}

func (p contextProviderFunc) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	if p.invoking == nil {
		return invoking.Messages, invoking.Options, nil
	}
	return p.invoking(ctx, invoking)
}

func (p contextProviderFunc) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	if p.invoked == nil {
		return nil
	}
	return p.invoked(ctx, invoked)
}

func TestContextProvider_Invoking_PanicsWithoutSourceID(t *testing.T) {
	provider := agent.NewContextProvider(agent.ContextProviderConfig{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_, _, _ = invokeContextProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
}

func TestContextProvider_Invoking_SourceStampsProvidedMessages(t *testing.T) {
	provided := message.NewText("provided")
	request := message.NewText("request")
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return []*message.Message{provided}, nil, nil
		},
	})

	messages, _, err := invokeContextProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected original and provided messages, got %d", len(messages))
	}
	if messages[0] != request {
		t.Fatal("expected original request message to be preserved")
	}
	if messages[1] == provided {
		t.Fatal("expected provided message to be cloned")
	}
	if messages[1].Source != (message.Source{Type: agent.SourceTypeContextProvider, ID: "ctx"}) {
		t.Fatalf("provided message source = %#v, want context provider source", messages[1].Source)
	}
}

func TestContextProvider_Invoking_HonorsNilProvideOutputs(t *testing.T) {
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return nil, nil, nil
		},
	})

	messages, options, err := invokeContextProvider(provider, t.Context(), []*message.Message{message.NewText("r1")}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := messageStrings(messages); !slices.Equal(got, []string{"r1"}) {
		t.Fatalf("expected original messages to be preserved, got %v", got)
	}
	if len(options) != 1 {
		t.Fatalf("expected original options to be preserved, got %d", len(options))
	}
}

func TestContextProvider_Invoking_PropagatesProvideError(t *testing.T) {
	expected := errors.New("provide failed")
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return nil, nil, expected
		},
	})

	_, _, err := invokeContextProvider(provider, t.Context(), []*message.Message{message.NewText("r1")}, agent.WithSession(agenttest.CreateSession()))
	if !errors.Is(err, expected) {
		t.Fatalf("expected Provide error, got %v", err)
	}
}

func TestContextProvider_Invoking_FiltersInputMessagesBeforeProvide(t *testing.T) {
	request := message.NewText("request")
	ctxMessage := message.NewText("ctx")
	ctxMessage.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "other"}
	var providedInput []*message.Message
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			providedInput = invoking.Messages
			return nil, nil, nil
		},
	})

	messages, _, err := invokeContextProvider(provider, t.Context(), []*message.Message{request, ctxMessage}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := messageStrings(providedInput); !slices.Equal(got, []string{"request"}) {
		t.Fatalf("provided input messages = %v, want [request]", got)
	}
	if got := messageStrings(messages); !slices.Equal(got, []string{"request", "ctx"}) {
		t.Fatalf("output messages = %v, want original unfiltered messages", got)
	}
}

func TestContextProvider_Invoking_UsesCustomProvideInputMessageFilter(t *testing.T) {
	request := message.NewText("request")
	ctxMessage := message.NewText("ctx")
	ctxMessage.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "other"}
	var providedInput []*message.Message
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID:                  "ctx",
		ProvideInputMessageFilter: messagefilter.PassThrough,
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			providedInput = invoking.Messages
			return nil, nil, nil
		},
	})

	_, _, err := invokeContextProvider(provider, t.Context(), []*message.Message{request, ctxMessage}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := messageStrings(providedInput); !slices.Equal(got, []string{"request", "ctx"}) {
		t.Fatalf("provided input messages = %v, want unfiltered input", got)
	}
}

func TestContextProvider_Invoking_ReturnsProvidedMessagesAndSetsSourceID(t *testing.T) {
	provided := message.NewText("ctx")
	request := message.NewText("request")

	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return []*message.Message{provided}, nil, nil
		},
	})

	messages, _, err := invokeContextProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
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
	if messages[1].Source.ID != "ctx" {
		t.Fatalf("expected SourceID=ctx, got %q", messages[1].Source.ID)
	}
}

func TestContextProvider_Invoking_AppendsProvidedMessages(t *testing.T) {
	provided := message.NewText("ctx")
	request := message.NewText("request")

	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return []*message.Message{provided}, nil, nil
		},
	})

	messages, _, err := invokeContextProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
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
	if messages[1].Source.ID != "ctx" {
		t.Fatalf("expected SourceID=ctx, got %q", messages[1].Source.ID)
	}
}

func TestContextProvider_Invoking_UsesCustomSourceID(t *testing.T) {
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "CustomContextSource",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return []*message.Message{message.NewText("ctx")}, nil, nil
		},
	})

	messages, _, err := invokeContextProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 provided message, got %d", len(messages))
	}
	if messages[0].Source.ID != "CustomContextSource" {
		t.Fatalf("expected custom source ID, got %q", messages[0].Source.ID)
	}
}

func TestContextProvider_Invoked_PanicsWithoutSourceID(t *testing.T) {
	provider := agent.NewContextProvider(agent.ContextProviderConfig{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = invokeContextProviderInvoked(provider, t.Context(), nil, nil, agent.WithSession(agenttest.CreateSession()))
}

func TestContextProvider_Invoked_CallsStoreAndIncludesExternalRequestMessagesByDefault(t *testing.T) {
	req1 := message.NewText("request1")
	req1.Source.ID = "ctx"
	req2 := message.NewText("request2")
	req2.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "ctx"}
	req3 := message.NewText("request3")
	req3.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "history"}
	resp := message.NewText("response")

	called := false
	var storedRequest []*message.Message
	var storedResponse []*message.Message

	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			called = true
			storedRequest = invoked.RequestMessages
			storedResponse = invoked.ResponseMessages
			return nil
		},
	})

	err := invokeContextProviderInvoked(provider, t.Context(), []*message.Message{req1, req2, req3}, []*message.Message{resp}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected Store to be called")
	}
	if len(storedRequest) != 1 || storedRequest[0] != req1 {
		t.Fatal("expected default request filter to keep only external messages")
	}
	if len(storedResponse) != 1 || storedResponse[0] != resp {
		t.Fatal("expected response messages to pass through unchanged")
	}
}

func TestContextProvider_Invoked_PropagatesStoreError(t *testing.T) {
	expected := errors.New("store failed")
	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Store: func(context.Context, agent.InvokedContext) error {
			return expected
		},
	})

	err := invokeContextProviderInvoked(provider, t.Context(), []*message.Message{message.NewText("r1")}, nil, agent.WithSession(agenttest.CreateSession()))
	if !errors.Is(err, expected) {
		t.Fatalf("expected store error, got %v", err)
	}
}

func TestContextProvider_InvokingContext_ReturnsProvidedFields(t *testing.T) {
	request := message.NewText("request")
	inputTool := functool.MustNew(functool.Config{Name: "input_tool"}, func(_ context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
	providedMsg := message.NewText("provided")
	providedTool := functool.MustNew(functool.Config{Name: "provided_tool"}, func(_ context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, nil
	})

	provider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return []*message.Message{providedMsg}, []agent.Option{agent.WithTool(providedTool)}, nil
		},
	})

	messages, options, err := invokeContextProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()), agent.WithTool(inputTool))
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
	if messages[1].Source.ID != "ctx" {
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
