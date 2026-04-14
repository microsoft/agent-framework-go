// Copyright (c) Microsoft. All rights reserved.

package contextprovider_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/middleware/contextprovider"
	"github.com/microsoft/agent-framework-go/tool"
)

type stubTool struct {
	name string
}

func (t stubTool) Name() string {
	return t.name
}

func (t stubTool) Description() string {
	return t.name
}

func TestNew_PanicsWithoutProviders(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()

	_ = contextprovider.New()
}

func TestMiddleware_Run_SingleProvider_EnrichesMessages(t *testing.T) {
	var capturedMessages []*message.Message
	provider := &memory.ContextProvider{
		SourceID: "provider-a",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			return memory.Context{Messages: []*message.Message{message.NewText("extra context")}}, nil
		},
	}

	_, err := collectResponse(middleware.RunChain(
		context.Background(),
		func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
			capturedMessages = msgs
			return singleUpdate("ok")
		},
		[]middleware.Middleware{contextprovider.New(provider)},
		[]*message.Message{message.NewText("hello")},
		agentopt.Session(memory.NewSession("")),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(capturedMessages))
	}
	if capturedMessages[1].String() != "hello" {
		t.Fatalf("expected original message to be preserved, got %q", capturedMessages[1].String())
	}
	if capturedMessages[0].String() != "extra context" {
		t.Fatalf("expected provider message to be added, got %q", capturedMessages[0].String())
	}
}

func TestMiddleware_Run_MultipleProviders_CalledInSequence(t *testing.T) {
	sequence := make([]string, 0, 2)
	var capturedMessages []*message.Message
	providerA := &memory.ContextProvider{
		SourceID: "provider-a",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			sequence = append(sequence, "provider-a")
			return memory.Context{Messages: []*message.Message{message.NewText("from a")}}, nil
		},
	}
	providerB := &memory.ContextProvider{
		SourceID: "provider-b",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			sequence = append(sequence, "provider-b")
			return memory.Context{Messages: []*message.Message{message.NewText("from b")}}, nil
		},
	}

	_, err := collectResponse(middleware.RunChain(
		context.Background(),
		func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
			capturedMessages = msgs
			return singleUpdate("ok")
		},
		[]middleware.Middleware{contextprovider.New(providerA, providerB)},
		[]*message.Message{message.NewText("hello")},
		agentopt.Session(memory.NewSession("")),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Equal(sequence, []string{"provider-a", "provider-b"}) {
		t.Fatalf("expected providers to run in sequence, got %v", sequence)
	}
	if len(capturedMessages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(capturedMessages))
	}
}

func TestMiddleware_Run_Provider_EnrichesTools(t *testing.T) {
	baselineTool := stubTool{name: "baseline"}
	providerTool := stubTool{name: "provider"}
	var capturedTools []tool.Tool
	provider := &memory.ContextProvider{
		SourceID: "provider-a",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			return memory.Context{Tools: []tool.Tool{providerTool}}, nil
		},
	}

	_, err := collectResponse(middleware.RunChain(
		context.Background(),
		func(_ context.Context, _ []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
			capturedTools = slices.Collect(agentopt.All(opts, agentopt.Tool))
			return singleUpdate("ok")
		},
		[]middleware.Middleware{contextprovider.New(provider)},
		[]*message.Message{message.NewText("hello")},
		agentopt.Session(memory.NewSession("")),
		agentopt.Tool(baselineTool),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := []string{capturedTools[0].Name(), capturedTools[1].Name()}; !slices.Equal(got, []string{"baseline", "provider"}) {
		t.Fatalf("expected tools [baseline provider], got %v", got)
	}
}

func TestMiddleware_Run_OnSuccess_AfterRunCalled(t *testing.T) {
	afterRunCalled := false
	var afterRunContext memory.AfterRunContext
	provider := &memory.ContextProvider{
		SourceID: "provider-a",
		Store: func(ctx memory.AfterRunContext) error {
			afterRunCalled = true
			afterRunContext = ctx
			return nil
		},
	}

	resp, err := collectResponse(middleware.RunChain(
		context.Background(),
		func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
			return singleUpdate("response")
		},
		[]middleware.Middleware{contextprovider.New(provider)},
		[]*message.Message{message.NewText("hello")},
		agentopt.Session(memory.NewSession("")),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !afterRunCalled {
		t.Fatal("expected Store to be called")
	}
	if afterRunContext.InvokeError != nil {
		t.Fatalf("expected nil invoke error, got %v", afterRunContext.InvokeError)
	}
	if len(afterRunContext.ResponseMessages) != 1 || afterRunContext.ResponseMessages[0].String() != resp.String() {
		t.Fatal("expected Store to receive response messages")
	}
}

func TestMiddleware_Run_OnFailure_AfterRunCalledWithInvokeError(t *testing.T) {
	expectedErr := errors.New("run failed")
	afterRunCalled := false
	var afterRunContext memory.AfterRunContext
	provider := &memory.ContextProvider{
		SourceID: "provider-a",
		Store: func(ctx memory.AfterRunContext) error {
			afterRunCalled = true
			afterRunContext = ctx
			return nil
		},
	}

	_, err := collectResponse(middleware.RunChain(
		context.Background(),
		func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, expectedErr)
			}
		},
		[]middleware.Middleware{contextprovider.New(provider)},
		[]*message.Message{message.NewText("hello")},
		agentopt.Session(memory.NewSession("")),
	))
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
	if !afterRunCalled {
		t.Fatal("expected Store to be called")
	}
	if !errors.Is(afterRunContext.InvokeError, expectedErr) {
		t.Fatalf("expected invoke error %v, got %v", expectedErr, afterRunContext.InvokeError)
	}
	if len(afterRunContext.ResponseMessages) != 0 {
		t.Fatalf("expected no response messages, got %d", len(afterRunContext.ResponseMessages))
	}
}

func TestMiddleware_Run_SharedOptions_ProviderToolsDoNotAccumulateAcrossCalls(t *testing.T) {
	toolCounts := make([]int, 0, 3)
	provider := &memory.ContextProvider{
		SourceID: "provider-a",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			return memory.Context{Tools: []tool.Tool{stubTool{name: "provider"}}}, nil
		},
	}
	sharedOptions := []agentopt.Option{
		agentopt.Session(memory.NewSession("")),
		agentopt.Tool(stubTool{name: "baseline"}),
	}

	for range 3 {
		_, err := collectResponse(middleware.RunChain(
			context.Background(),
			func(_ context.Context, _ []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
				toolCounts = append(toolCounts, len(slices.Collect(agentopt.All(opts, agentopt.Tool))))
				return singleUpdate("ok")
			},
			[]middleware.Middleware{contextprovider.New(provider)},
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

func TestMiddleware_Run_SharedOptions_OriginalToolsNotMutated(t *testing.T) {
	baselineTool := stubTool{name: "baseline"}
	provider := &memory.ContextProvider{
		SourceID: "provider-a",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			return memory.Context{Tools: []tool.Tool{stubTool{name: "provider"}}}, nil
		},
	}
	sharedOptions := []agentopt.Option{
		agentopt.Session(memory.NewSession("")),
		agentopt.Tool(baselineTool),
	}

	_, err := collectResponse(middleware.RunChain(
		context.Background(),
		func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
			return singleUpdate("ok")
		},
		[]middleware.Middleware{contextprovider.New(provider)},
		[]*message.Message{message.NewText("hello")},
		sharedOptions...,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	originalTools := slices.Collect(agentopt.All(sharedOptions, agentopt.Tool))
	if len(originalTools) != 1 {
		t.Fatalf("expected original shared options to keep 1 tool, got %d", len(originalTools))
	}
	if originalTools[0].Name() != baselineTool.Name() {
		t.Fatalf("expected original shared options to preserve baseline tool, got %q", originalTools[0].Name())
	}
}

func singleUpdate(text string) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: text}}}, nil)
	}
}

func collectResponse(seq iter.Seq2[*message.ResponseUpdate, error]) (*message.Response, error) {
	var resp message.Response
	for update, err := range seq {
		if err != nil {
			return nil, err
		}
		resp.Update(update)
	}
	resp.Coalesce()
	return &resp, nil
}
