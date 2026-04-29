// Copyright (c) Microsoft. All rights reserved.

package contextprovider_test

import (
	"context"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/middleware/contextprovider"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func TestContextProviderMiddleware_PanicsWithoutProviders(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()

	_ = contextprovider.New()
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

	_, err := collectMiddlewareResponse(contextprovider.New(provider).Run(
		func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
			capturedTools = slices.Collect(agent.AllOptions(opts, agent.WithTool))
			return middlewareSingleUpdate("ok")
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
		_, err := collectMiddlewareResponse(contextprovider.New(provider).Run(
			func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
				toolCounts = append(toolCounts, len(slices.Collect(agent.AllOptions(opts, agent.WithTool))))
				return middlewareSingleUpdate("ok")
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

	_, err := collectMiddlewareResponse(contextprovider.New(provider).Run(
		func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
			return middlewareSingleUpdate("ok")
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

func middlewareSingleUpdate(text string) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: text}}}, nil)
	}
}

func collectMiddlewareResponse(seq iter.Seq2[*message.ResponseUpdate, error]) (*message.Response, error) {
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

type stubTool struct {
	name string
}

func (t stubTool) Name() string {
	return t.name
}

func (t stubTool) Description() string {
	return t.name
}
