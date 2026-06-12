// Copyright (c) Microsoft. All rights reserved.

// Package loop provides middleware that re-invokes an agent until one of its
// evaluators decides no more work is needed.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const (
	// defaultMaxIterations is the default safety cap for loop runs.
	defaultMaxIterations = 10

	// completionMarkerPlaceholder is replaced by the configured completion
	// marker in completion-marker feedback templates.
	completionMarkerPlaceholder = "{completion_marker}"

	// lastResponsePlaceholder is replaced by the latest response text in
	// completion-marker feedback templates.
	lastResponsePlaceholder = "{last_response}"

	// defaultCompletionMarkerFeedbackTemplate is used when a completion-marker
	// evaluator does not receive a custom feedback template.
	defaultCompletionMarkerFeedbackTemplate = "Continue working on the request. When you have fully completed the task, end your response with the marker '" +
		completionMarkerPlaceholder + "' to indicate completion."
)

// Config configures loop middleware.
type Config struct {
	// Evaluators decide after each iteration whether the wrapped agent should
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

	// FreshContextPerIteration restarts each reinvocation from the original
	// input messages plus an aggregated feedback log, and resets the session
	// to a pristine snapshot taken before the first iteration.
	FreshContextPerIteration bool

	// NonStreamingReturnsLastResponseOnly causes non-streaming runs (those
	// without agent.Stream(true)) to surface only the final iteration response.
	NonStreamingReturnsLastResponseOnly bool

	// SessionCreatedCallback is invoked when the loop creates a fresh session
	// for reinvocation.
	SessionCreatedCallback func(context.Context, *agent.Session) error
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
			maxIterations = defaultMaxIterations
		}
		if maxIterations < 1 {
			yield(nil, fmt.Errorf("loop: MaxIterations must be at least 1, got %d", cfg.MaxIterations))
			return
		}

		initialMessages := cloneMessages(messages)
		currentMessages := cloneMessages(messages)
		currentOpts := slices.Clone(opts)
		stream, _ := agent.GetOption(opts, agent.Stream)
		returnLastResponseOnly := cfg.NonStreamingReturnsLastResponseOnly && !stream
		initialSession, hasSession := agent.GetOption(opts, agent.WithSession)
		var initialSessionSnapshot []byte
		loopCtx := &Context{
			InitialMessages:      initialMessages,
			Options:              slices.Clone(opts),
			AdditionalProperties: make(map[string]any),
		}
		if cfg.FreshContextPerIteration && hasSession {
			var err error
			initialSessionSnapshot, err = snapshotSession(initialSession)
			if err != nil {
				yield(nil, err)
				return
			}
		}

		for {
			var resp agent.Response
			var iterationUpdates []*agent.ResponseUpdate
			for update, err := range next(ctx, currentMessages, currentOpts...) {
				if update != nil {
					resp.Update(update)
					if returnLastResponseOnly {
						iterationUpdates = append(iterationUpdates, cloneResponseUpdate(update))
					}
				}
				if err != nil {
					yield(update, err)
					return
				}
				if returnLastResponseOnly {
					continue
				}
				if !yield(update, nil) {
					return
				}
			}
			resp.Coalesce()
			loopCtx.Iteration++
			loopCtx.LastResponse = &resp

			if hasPendingApprovalRequests(&resp) || loopCtx.Iteration >= maxIterations {
				if returnLastResponseOnly {
					if !yieldResponseUpdates(yield, iterationUpdates) {
						return
					}
				}
				return
			}

			evaluation, ok, err := evaluate(ctx, cfg.Evaluators, loopCtx)
			if err != nil {
				yield(nil, err)
				return
			}
			if !ok {
				if returnLastResponseOnly {
					if !yieldResponseUpdates(yield, iterationUpdates) {
						return
					}
				}
				return
			}

			loopCtx.Feedback = append(loopCtx.Feedback, evaluation.Feedback)
			var surfacedMessages []*message.Message
			currentMessages, surfacedMessages = nextMessages(cfg, loopCtx, evaluation)
			if cfg.FreshContextPerIteration && hasSession {
				nextSession, err := sessionFromSnapshot(initialSessionSnapshot)
				if err != nil {
					yield(nil, err)
					return
				}
				if cfg.SessionCreatedCallback != nil {
					if err := cfg.SessionCreatedCallback(ctx, nextSession); err != nil {
						yield(nil, err)
						return
					}
				}
				currentOpts = append(slices.Clone(opts), agent.WithSession(nextSession))
			}
			if !returnLastResponseOnly && !cfg.ExcludeOnBehalfOfMessages {
				for i, msg := range surfacedMessages {
					if !yield(messageToUpdate(msg, fmt.Sprintf("loop-message-%d-%d", loopCtx.Iteration, i)), nil) {
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

func nextMessages(cfg Config, loopCtx *Context, evaluation Evaluation) (messages []*message.Message, surfaced []*message.Message) {
	if len(evaluation.Messages) > 0 {
		cloned := cloneMessages(evaluation.Messages)
		return cloned, cloned
	}
	if cfg.FreshContextPerIteration {
		nextMessages := cloneMessages(loopCtx.InitialMessages)
		feedbackMessage := aggregatedFeedbackMessage(loopCtx.Feedback, cfg.OnBehalfOfAuthorName)
		if feedbackMessage == nil {
			return nextMessages, nil
		}
		nextMessages = append(nextMessages, feedbackMessage)
		return nextMessages, []*message.Message{feedbackMessage}
	}
	if evaluation.Feedback == "" {
		return nil, nil
	}
	msg := message.NewText(evaluation.Feedback)
	msg.AuthorName = cfg.OnBehalfOfAuthorName
	return []*message.Message{msg}, []*message.Message{msg}
}

func messageToUpdate(msg *message.Message, syntheticID string) *agent.ResponseUpdate {
	if msg == nil {
		return nil
	}
	cloned := msg.Clone()
	if cloned.ID == "" {
		cloned.ID = syntheticID
	}
	return (&agent.Response{Messages: []*message.Message{cloned}}).ToUpdates()[0]
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

func cloneResponseUpdate(update *agent.ResponseUpdate) *agent.ResponseUpdate {
	if update == nil {
		return nil
	}
	out := *update
	out.Contents = slices.Clone(update.Contents)
	return &out
}

func yieldResponseUpdates(yield func(*agent.ResponseUpdate, error) bool, updates []*agent.ResponseUpdate) bool {
	for _, update := range updates {
		if !yield(update, nil) {
			return false
		}
	}
	return true
}

func snapshotSession(session *agent.Session) ([]byte, error) {
	if session == nil {
		return nil, nil
	}
	data, err := json.Marshal(session)
	if err != nil {
		return nil, errors.New("loop: snapshot session")
	}
	return data, nil
}

func sessionFromSnapshot(data []byte) (*agent.Session, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var cloned agent.Session
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, errors.New("loop: restore session snapshot")
	}
	return &cloned, nil
}

func aggregatedFeedbackMessage(feedback []string, authorName string) *message.Message {
	var builder strings.Builder
	builder.WriteString("## Feedback\n")
	for _, entry := range feedback {
		if entry == "" {
			continue
		}
		builder.WriteString("\n- ")
		builder.WriteString(entry)
	}
	if builder.Len() == len("## Feedback\n") {
		return nil
	}
	msg := message.NewText(builder.String())
	msg.AuthorName = authorName
	return msg
}
