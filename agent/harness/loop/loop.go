// Copyright (c) Microsoft. All rights reserved.

// Package loop provides middleware that re-invokes an agent until one of its
// evaluators decides no more work is needed.
package loop

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const (
	// DefaultMaxIterations is the default safety cap for loop runs.
	DefaultMaxIterations = 10

	// CompletionMarkerPlaceholder is replaced by the configured completion
	// marker in completion-marker feedback templates.
	CompletionMarkerPlaceholder = "{completion_marker}"

	// LastResponsePlaceholder is replaced by the latest response text in
	// completion-marker feedback templates.
	LastResponsePlaceholder = "{last_response}"

	// DefaultCompletionMarkerFeedbackTemplate is used when a completion-marker
	// evaluator does not receive a custom feedback template.
	DefaultCompletionMarkerFeedbackTemplate = "Continue working on the request. When you have fully completed the task, end your response with the marker '" +
		CompletionMarkerPlaceholder + "' to indicate completion."
)

// Config configures loop middleware.
type Config struct {
	// Evaluators decides after each iteration whether the wrapped agent should
	// be re-invoked. Evaluators are checked in order; the first evaluator that
	// asks to continue wins, and the loop stops only when all evaluators stop.
	Evaluators []Evaluator

	// MaxIterations is the absolute safety cap for a single run. When zero,
	// DefaultMaxIterations is used.
	MaxIterations int

	// OnBehalfOfAuthorName stamps loop-synthesized feedback messages.
	OnBehalfOfAuthorName string

	// ExcludeOnBehalfOfMessages prevents loop-synthesized feedback messages
	// from being yielded to the caller. They are still sent to the agent.
	ExcludeOnBehalfOfMessages bool
}

// Context contains per-run state passed to evaluators.
type Context struct {
	// InitialMessages are the messages passed to the first iteration.
	InitialMessages []*message.Message

	// LastResponse is the response produced by the most recent iteration.
	LastResponse *agent.Response

	// Options are the options supplied to the agent run.
	Options []agent.Option

	// Iteration is the number of completed agent runs so far.
	Iteration int

	// Feedback contains one entry for each reinvocation requested by an
	// evaluator. Entries are empty when the reinvocation supplied no feedback
	// string or used explicit messages.
	Feedback []string

	// AdditionalProperties is a mutable bag for evaluator-specific per-run
	// state.
	AdditionalProperties map[string]any
}

// Evaluation is the result of an evaluator decision.
type Evaluation struct {
	// ShouldReinvoke indicates whether the agent should run another iteration.
	ShouldReinvoke bool

	// Feedback is sent as a user message on the next iteration when Messages is
	// empty.
	Feedback string

	// Messages, when non-empty, are sent verbatim on the next iteration instead
	// of the loop building a feedback message.
	Messages []*message.Message
}

// Stop returns an evaluation that stops the loop.
func Stop() Evaluation {
	return Evaluation{}
}

// Continue returns an evaluation that re-invokes the agent with optional
// feedback.
func Continue(feedback string) Evaluation {
	return Evaluation{ShouldReinvoke: true, Feedback: strings.TrimSpace(feedback)}
}

// ContinueWithMessages returns an evaluation that re-invokes the agent with
// explicit messages.
func ContinueWithMessages(messages []*message.Message) Evaluation {
	return Evaluation{ShouldReinvoke: true, Messages: cloneMessages(messages)}
}

// Evaluator decides whether a loop should continue after an agent iteration.
type Evaluator interface {
	Evaluate(ctx context.Context, loop *Context) (Evaluation, error)
}

// EvaluatorFunc adapts a function to the Evaluator interface.
type EvaluatorFunc func(ctx context.Context, loop *Context) (Evaluation, error)

// Evaluate implements Evaluator.
func (f EvaluatorFunc) Evaluate(ctx context.Context, loop *Context) (Evaluation, error) {
	if f == nil {
		return Stop(), errors.New("loop: evaluator function is nil")
	}
	if loop == nil {
		return Stop(), errors.New("loop: context cannot be nil")
	}
	return f(ctx, loop)
}

// New creates loop middleware.
func New(cfg Config) agent.Middleware {
	return agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return run(cfg, next, ctx, messages, opts...)
	})
}

func run(cfg Config, next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		if len(cfg.Evaluators) == 0 {
			yield(nil, errors.New("loop: at least one evaluator is required"))
			return
		}
		for _, evaluator := range cfg.Evaluators {
			if evaluator == nil {
				yield(nil, errors.New("loop: evaluator cannot be nil"))
				return
			}
		}
		maxIterations := cfg.MaxIterations
		if maxIterations == 0 {
			maxIterations = DefaultMaxIterations
		}
		if maxIterations < 1 {
			yield(nil, fmt.Errorf("loop: MaxIterations must be at least 1, got %d", cfg.MaxIterations))
			return
		}

		initialMessages := cloneMessages(messages)
		currentMessages := cloneMessages(messages)
		loopCtx := &Context{
			InitialMessages:      initialMessages,
			Options:              slices.Clone(opts),
			AdditionalProperties: make(map[string]any),
		}

		for {
			var resp agent.Response
			for update, err := range next(ctx, currentMessages, opts...) {
				if update != nil {
					resp.Update(update)
				}
				if err != nil {
					yield(update, err)
					return
				}
				if !yield(update, nil) {
					return
				}
			}
			resp.Coalesce()
			loopCtx.Iteration++
			loopCtx.LastResponse = &resp

			if hasPendingApprovalRequests(&resp) || loopCtx.Iteration >= maxIterations {
				return
			}

			evaluation, ok, err := evaluate(ctx, cfg.Evaluators, loopCtx)
			if err != nil {
				yield(nil, err)
				return
			}
			if !ok {
				return
			}

			currentMessages = nextMessages(evaluation, cfg.OnBehalfOfAuthorName)
			loopCtx.Feedback = append(loopCtx.Feedback, evaluation.Feedback)
			if !cfg.ExcludeOnBehalfOfMessages {
				for _, msg := range currentMessages {
					if !yield(messageToUpdate(msg), nil) {
						return
					}
				}
			}
		}
	}
}

func evaluate(ctx context.Context, evaluators []Evaluator, loopCtx *Context) (Evaluation, bool, error) {
	for _, evaluator := range evaluators {
		evaluation, err := evaluator.Evaluate(ctx, loopCtx)
		if err != nil {
			return Stop(), false, err
		}
		if evaluation.ShouldReinvoke {
			return evaluation, true, nil
		}
	}
	return Stop(), false, nil
}

func nextMessages(evaluation Evaluation, authorName string) []*message.Message {
	if len(evaluation.Messages) > 0 {
		return cloneMessages(evaluation.Messages)
	}
	if evaluation.Feedback == "" {
		return nil
	}
	msg := message.NewText(evaluation.Feedback)
	msg.AuthorName = authorName
	return []*message.Message{msg}
}

func messageToUpdate(msg *message.Message) *agent.ResponseUpdate {
	if msg == nil {
		return nil
	}
	return &agent.ResponseUpdate{
		AdditionalProperties: msg.AdditionalProperties,
		MessageID:            msg.ID,
		AuthorName:           msg.AuthorName,
		Role:                 msg.Role,
		CreatedAt:            msg.CreatedAt,
		Contents:             slices.Clone(msg.Contents),
		RawRepresentation:    msg.RawRepresentation,
	}
}

func hasPendingApprovalRequests(resp *agent.Response) bool {
	if resp == nil {
		return false
	}
	for content := range resp.Contents() {
		if _, ok := content.(*message.ToolApprovalRequestContent); ok {
			return true
		}
	}
	return false
}

func cloneMessages(messages []*message.Message) []*message.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]*message.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Clone())
	}
	return out
}
