// Copyright (c) Microsoft. All rights reserved.

package harness_test

import (
	"context"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func TestConfigure_DefaultsApplyHarnessSurface(t *testing.T) {
	var capturedMessages []*message.Message
	var capturedInstructions []string
	var capturedTools []tool.Tool

	a := agent.New(agent.ProviderConfig{
		ProviderName: "test",
		Run: func(_ context.Context, msgs []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			capturedMessages = msgs
			capturedInstructions = slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
			capturedTools = slices.Collect(agent.AllOptions(opts, agent.WithTool))
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.TextContent{Text: "done"}},
				}, nil)
			}
		},
	}, harness.Configure(agent.Config{}, harness.Config{}))

	_, err := a.RunText(t.Context(), "hello", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := textMessages(capturedMessages); !slices.Equal(got, []string{"hello", "### Current todo list\n- none yet"}) {
		t.Fatalf("messages = %q, want hello plus todo summary", got)
	}

	if len(capturedTools) != 7 {
		t.Fatalf("expected 7 harness tools, got %d", len(capturedTools))
	}
	for _, name := range []string{
		"todos_add",
		"todos_complete",
		"todos_remove",
		"todos_get_remaining",
		"todos_get_all",
		"mode_set",
		"mode_get",
	} {
		if !hasToolNamed(capturedTools, name) {
			t.Fatalf("expected tool %q to be configured", name)
		}
	}

	if len(capturedInstructions) < 3 {
		t.Fatalf("expected harness instructions plus provider instructions, got %d entries", len(capturedInstructions))
	}
	if capturedInstructions[0] != harness.DefaultInstructions {
		t.Fatalf("first instructions = %q, want harness default instructions", capturedInstructions[0])
	}
}

func TestConfigure_DisableFlagsOmitHarnessDefaults(t *testing.T) {
	cfg := harness.Configure(agent.Config{}, harness.Config{
		DisableInstructions:      true,
		DisableTodoProvider:      true,
		DisableAgentModeProvider: true,
		DisableToolAutoApproval:  true,
		LoopConfig:               &loop.Config{},
	})

	if got := slices.Collect(agent.AllOptions(cfg.RunOptions, agent.WithInstructions)); len(got) != 0 {
		t.Fatalf("expected no harness instructions, got %q", got)
	}
	if len(cfg.ContextProviders) != 0 {
		t.Fatalf("expected no context providers, got %d", len(cfg.ContextProviders))
	}
	if len(cfg.Middlewares) != 0 {
		t.Fatalf("expected no middlewares, got %d", len(cfg.Middlewares))
	}
}

func TestConfigure_PrependsInstructionsAndAppendsToExistingConfig(t *testing.T) {
	customProvider := agent.NewContextProvider(agent.ContextProviderConfig{SourceID: "custom"})
	customMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return next(ctx, messages, opts...)
	})

	cfg := harness.Configure(agent.Config{
		RunOptions:       []agent.Option{agent.WithInstructions("custom instructions")},
		ContextProviders: []agent.ContextProvider{customProvider},
		Middlewares:      []agent.Middleware{customMiddleware},
	}, harness.Config{
		Instructions: "harness instructions",
		LoopConfig: &loop.Config{
			Evaluators: []loop.Evaluator{
				loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
					return loop.Stop(), nil
				}),
			},
		},
	})

	gotInstructions := slices.Collect(agent.AllOptions(cfg.RunOptions, agent.WithInstructions))
	if !slices.Equal(gotInstructions[:2], []string{"harness instructions", "custom instructions"}) {
		t.Fatalf("instructions = %q, want harness instructions before custom instructions", gotInstructions)
	}
	if len(cfg.ContextProviders) != 3 {
		t.Fatalf("expected existing provider plus 2 harness providers, got %d", len(cfg.ContextProviders))
	}
	if len(cfg.Middlewares) != 3 {
		t.Fatalf("expected existing middleware plus loop and tool-approval middleware, got %d", len(cfg.Middlewares))
	}
}

func TestConfigure_LoopConfigReinvokesAgent(t *testing.T) {
	var calls int
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test",
		Run: func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			calls++
			text := "first"
			if calls > 1 {
				text = "second"
			}
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.TextContent{Text: text}},
				}, nil)
			}
		},
	}, harness.Configure(agent.Config{}, harness.Config{
		DisableInstructions:      true,
		DisableTodoProvider:      true,
		DisableAgentModeProvider: true,
		DisableToolAutoApproval:  true,
		LoopConfig: &loop.Config{
			Evaluators: []loop.Evaluator{
				loop.EvaluatorFunc(func(_ context.Context, lc *loop.Context) (loop.Evaluation, error) {
					if lc.Iteration == 1 {
						return loop.Continue("try again"), nil
					}
					return loop.Stop(), nil
				}),
			},
		},
	}))

	_, err := a.RunText(t.Context(), "hello", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected loop middleware to re-invoke the agent once, got %d calls", calls)
	}
}

func textMessages(messages []*message.Message) []string {
	var out []string
	for _, msg := range messages {
		if msg == nil || len(msg.Contents) == 0 {
			continue
		}
		if text, ok := msg.Contents[0].(*message.TextContent); ok {
			out = append(out, text.Text)
		}
	}
	return out
}

func hasToolNamed(tools []tool.Tool, name string) bool {
	return slices.ContainsFunc(tools, func(t tool.Tool) bool {
		return t != nil && t.Name() == name
	})
}
