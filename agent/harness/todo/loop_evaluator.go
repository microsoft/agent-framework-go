// Copyright (c) Microsoft. All rights reserved.

package todo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
)

const (
	// remainingTodosPlaceholder is replaced by the formatted list of remaining
	// todo items in completion-loop feedback templates.
	remainingTodosPlaceholder = "{remaining_todos}"

	// defaultCompletionLoopFeedbackTemplate is used when a completion-loop
	// evaluator does not receive a custom feedback template.
	defaultCompletionLoopFeedbackTemplate = "The following todo items are not complete yet:\n" +
		remainingTodosPlaceholder + "\nContinue working on the remaining items, and mark each one complete as you finish it."
)

// ModeProvider reports a session's current agent mode. *agentmode.Provider
// implements it; the interface keeps this package free of a dependency on the
// agentmode package.
type ModeProvider interface {
	Mode(session *agent.Session) string
}

// CompletionLoopEvaluatorConfig configures NewCompletionLoopEvaluator.
type CompletionLoopEvaluatorConfig struct {
	// FeedbackMessageTemplate is sent as feedback while todo items remain.
	// When nil, a default template is used. The {remaining_todos} token is
	// replaced with the formatted list of remaining items on each evaluation.
	FeedbackMessageTemplate *string

	// Modes optionally gates the evaluator to a set of agent modes: the loop
	// is continued only while the session's current mode is one of these.
	// When empty, the evaluator applies in every mode.
	Modes []string

	// ModeProvider reads the session's current mode. Required when Modes is
	// non-empty, unused otherwise.
	ModeProvider ModeProvider
}

// CompletionLoopEvaluator re-invokes the agent while the todo provider still
// holds incomplete items, feeding the remaining items back to the agent. It is
// the Go counterpart of .NET's TodoCompletionLoopEvaluator; the provider is
// passed explicitly because Go agents have no service-resolution equivalent of
// GetService<TodoProvider>().
type CompletionLoopEvaluator struct {
	provider                *Provider
	feedbackMessageTemplate string
	modes                   []string
	modeProvider            ModeProvider
}

var _ loop.Evaluator = (*CompletionLoopEvaluator)(nil)

// NewCompletionLoopEvaluator creates an evaluator that keeps the loop running
// until provider has no remaining todo items.
func NewCompletionLoopEvaluator(provider *Provider, config CompletionLoopEvaluatorConfig) *CompletionLoopEvaluator {
	if provider == nil {
		panic("todo: completion loop evaluator requires a provider")
	}
	if len(config.Modes) > 0 && config.ModeProvider == nil {
		panic("todo: completion loop evaluator requires a ModeProvider when Modes is set")
	}
	template := defaultCompletionLoopFeedbackTemplate
	if config.FeedbackMessageTemplate != nil {
		template = *config.FeedbackMessageTemplate
	}
	return &CompletionLoopEvaluator{
		provider:                provider,
		feedbackMessageTemplate: template,
		modes:                   slices.Clone(config.Modes),
		modeProvider:            config.ModeProvider,
	}
}

// Evaluate implements loop.Evaluator. It stops the loop when the run has no
// session (there is no todo list to consult), when a mode gate is configured
// and the session is not in one of those modes, or when no todo items remain.
func (e *CompletionLoopEvaluator) Evaluate(_ context.Context, loopCtx *loop.Context) (loop.Evaluation, error) {
	if loopCtx == nil {
		return loop.Stop(), errors.New("todo: loop context cannot be nil")
	}
	session, ok := agent.GetOption(loopCtx.Options, agent.WithSession)
	if !ok || session == nil {
		return loop.Stop(), nil
	}
	if len(e.modes) > 0 && !slices.Contains(e.modes, e.modeProvider.Mode(session)) {
		return loop.Stop(), nil
	}
	remaining := e.provider.RemainingTodos(session)
	if len(remaining) == 0 {
		return loop.Stop(), nil
	}
	return loop.Continue(formatCompletionLoopFeedback(e.feedbackMessageTemplate, remaining)), nil
}

func formatCompletionLoopFeedback(template string, remaining []Item) string {
	var sb strings.Builder
	for _, item := range remaining {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "- %d: %s", item.ID, item.Title)
		if item.Description != "" {
			fmt.Fprintf(&sb, " (%s)", item.Description)
		}
	}
	return strings.ReplaceAll(template, remainingTodosPlaceholder, sb.String())
}
