// Copyright (c) Microsoft. All rights reserved.

package todo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
)

// fixedModeProvider reports one mode for every session.
type fixedModeProvider string

func (m fixedModeProvider) Mode(*agent.Session) string { return string(m) }

// seedTodos adds the given titles to the provider's session state through the
// provider's own todos_add tool, as the agent would.
func seedTodos(t *testing.T, provider *todo.Provider, opts []agent.Option, titles ...string) {
	t.Helper()
	_, outOpts, err := invokeProvider(provider, context.Background(), newMessages("start"), opts...)
	if err != nil {
		t.Fatalf("Invoking() error = %v", err)
	}
	for _, title := range titles {
		callTool(t, outOpts, "todos_add", `{"todos":[{"title":"`+title+`"}]}`)
	}
}

func completeTodo(t *testing.T, provider *todo.Provider, opts []agent.Option, id string) {
	t.Helper()
	_, outOpts, err := invokeProvider(provider, context.Background(), newMessages("continue"), opts...)
	if err != nil {
		t.Fatalf("Invoking() error = %v", err)
	}
	callTool(t, outOpts, "todos_complete", `{"items":[{"id":`+id+`,"reason":"done"}]}`)
}

func evaluate(t *testing.T, evaluator loop.Evaluator, opts []agent.Option) loop.Evaluation {
	t.Helper()
	evaluation, err := evaluator.Evaluate(context.Background(), &loop.Context{Options: opts})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	return evaluation
}

func TestCompletionLoopEvaluatorContinuesWhileTodosRemain(t *testing.T) {
	provider := todo.New(nil)
	opts := sessionOpts()
	seedTodos(t, provider, opts, "write tests", "update docs")

	evaluator := todo.NewCompletionLoopEvaluator(provider, todo.CompletionLoopEvaluatorConfig{})

	evaluation := evaluate(t, evaluator, opts)
	if !evaluation.ShouldReinvoke {
		t.Fatal("expected the loop to continue while todos remain")
	}
	for _, want := range []string{"write tests", "update docs"} {
		if !strings.Contains(evaluation.Feedback, want) {
			t.Errorf("feedback %q does not name remaining item %q", evaluation.Feedback, want)
		}
	}

	completeTodo(t, provider, opts, "1")
	evaluation = evaluate(t, evaluator, opts)
	if !evaluation.ShouldReinvoke {
		t.Fatal("expected the loop to continue while one todo remains")
	}
	if strings.Contains(evaluation.Feedback, "write tests") {
		t.Errorf("feedback %q still names the completed item", evaluation.Feedback)
	}

	completeTodo(t, provider, opts, "2")
	evaluation = evaluate(t, evaluator, opts)
	if evaluation.ShouldReinvoke {
		t.Fatal("expected the loop to stop once every todo is complete")
	}
}

func TestCompletionLoopEvaluatorCustomTemplate(t *testing.T) {
	provider := todo.New(nil)
	opts := sessionOpts()
	seedTodos(t, provider, opts, "ship it")

	template := "Remaining:\n{remaining_todos}"
	evaluator := todo.NewCompletionLoopEvaluator(provider, todo.CompletionLoopEvaluatorConfig{
		FeedbackMessageTemplate: &template,
	})

	evaluation := evaluate(t, evaluator, opts)
	if want := "Remaining:\n- 1: ship it"; evaluation.Feedback != want {
		t.Errorf("Feedback = %q, want %q", evaluation.Feedback, want)
	}
}

func TestCompletionLoopEvaluatorModeGate(t *testing.T) {
	provider := todo.New(nil)
	opts := sessionOpts()
	seedTodos(t, provider, opts, "plan the feature")

	inMode := todo.NewCompletionLoopEvaluator(provider, todo.CompletionLoopEvaluatorConfig{
		Modes:        []string{"planning"},
		ModeProvider: fixedModeProvider("planning"),
	})
	if evaluation := evaluate(t, inMode, opts); !evaluation.ShouldReinvoke {
		t.Error("expected the loop to continue while in a gated mode with todos remaining")
	}

	outOfMode := todo.NewCompletionLoopEvaluator(provider, todo.CompletionLoopEvaluatorConfig{
		Modes:        []string{"planning"},
		ModeProvider: fixedModeProvider("executing"),
	})
	if evaluation := evaluate(t, outOfMode, opts); evaluation.ShouldReinvoke {
		t.Error("expected the loop to stop while outside every gated mode")
	}
}

func TestCompletionLoopEvaluatorWithoutSessionStops(t *testing.T) {
	provider := todo.New(nil)
	evaluator := todo.NewCompletionLoopEvaluator(provider, todo.CompletionLoopEvaluatorConfig{})
	if evaluation := evaluate(t, evaluator, nil); evaluation.ShouldReinvoke {
		t.Error("expected the loop to stop when the run has no session")
	}
}
