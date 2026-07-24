// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to re-invoke an agent automatically with the loop harness
// middleware (agent/harness/loop). The loop middleware runs the wrapped agent, hands
// the latest response to a set of evaluators, and re-invokes the agent with feedback
// until an evaluator decides the work is done or a MaxIterations safety cap is reached.
//
// It demonstrates the two evaluator patterns supported by the Go SDK, matching the
// .NET/Python loop harness semantics (the .NET AI-judge evaluator is intentionally not
// shown because Go does not provide one):
//
//  1. loop.NewCompletionMarkerEvaluator refines a draft until the response contains a
//     completion marker, combined with FreshContextPerIteration so every attempt starts
//     from the original request plus an aggregated feedback log.
//  2. A loop.EvaluatorFunc returning loop.Continue(feedback) / loop.Stop() drives a
//     custom review-and-retry policy, and MaxIterations guarantees the loop terminates
//     even when the evaluator is never satisfied.
//
// To keep the sample deterministic and runnable without credentials, it uses a small
// scripted provider instead of a hosted model. The same loop.New(...) middleware works
// unchanged in front of any real provider agent (foundryprovider, openaiprovider, ...).

package main

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
)

var logger = demo.NewLogger(
	"Loop Reinvocation",
	"Demonstrates the agent/harness/loop middleware re-invoking an agent until an evaluator stops it.",
	"Provider", "scripted-demo",
)

func main() {
	completionMarkerRefinement()
	customEvaluatorStops()
	customEvaluatorHitsSafetyCap()
}

// completionMarkerRefinement re-invokes the agent until its response contains a
// completion marker. FreshContextPerIteration restarts every attempt from the original
// request plus an aggregated feedback log rather than accumulating raw chat history.
func completionMarkerRefinement() {
	const marker = "TASK_COMPLETE"

	// The first two drafts are unfinished; the third completes the task and appends the
	// marker, so the loop stops on the third iteration (before the MaxIterations cap).
	drafts := []string{
		`Draft slogan: "Notes."`,
		`Revised slogan: "NoteNest keeps your notes."`,
		`Final slogan: "NoteNest - your notes, beautifully in order." ` + marker,
	}
	provider := newScriptedProvider(func(call int) string {
		return drafts[min(call, len(drafts))-1]
	})

	a := agent.New(provider.config(), agent.Config{
		Name: "SloganWriter",
		Middlewares: []agent.Middleware{
			loop.New(loop.Config{
				MaxIterations:            5,
				FreshContextPerIteration: true,
				Evaluators: []loop.Evaluator{
					loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{Marker: marker}),
				},
			}),
			logger, // logs each re-invocation of the wrapped agent
		},
	})

	resp, err := a.RunText(context.Background(), "Write a catchy slogan for a note-taking app named NoteNest.").Collect()
	demo.Response(resp, err)
	fmt.Printf("Completion-marker loop finished after %d agent iteration(s) (cap 5).\n\n", provider.calls)
}

// customEvaluatorStops uses a loop.EvaluatorFunc that stops as soon as the response is
// approved, otherwise it re-invokes the agent with feedback. The scripted reviewer
// approves on the second attempt, so the loop stops before the MaxIterations cap.
func customEvaluatorStops() {
	provider := newScriptedProvider(func(call int) string {
		if call >= 2 {
			return "Revision 2: concise summary of the quarterly report. APPROVED"
		}
		return "Revision 1: an overly long and rambling summary of the quarterly report."
	})

	a := agent.New(provider.config(), agent.Config{
		Name: "ReportSummarizer",
		Middlewares: []agent.Middleware{
			loop.New(loop.Config{
				MaxIterations: 4,
				Evaluators:    []loop.Evaluator{approvalEvaluator()},
			}),
			logger,
		},
	})

	resp, err := a.RunText(context.Background(), "Summarize the quarterly report in one short sentence.").Collect()
	demo.Response(resp, err)
	fmt.Printf("Custom-evaluator loop stopped on approval after %d iteration(s) (cap 4).\n\n", provider.calls)
}

// customEvaluatorHitsSafetyCap uses the same evaluator against a reviewer that never
// approves. Without MaxIterations the loop would run forever; the safety cap stops it.
func customEvaluatorHitsSafetyCap() {
	provider := newScriptedProvider(func(call int) string {
		return fmt.Sprintf("Revision %d: still too long, and the reviewer keeps asking for changes.", call)
	})

	const maxIterations = 3
	a := agent.New(provider.config(), agent.Config{
		Name: "StubbornSummarizer",
		Middlewares: []agent.Middleware{
			loop.New(loop.Config{
				MaxIterations: maxIterations,
				Evaluators:    []loop.Evaluator{approvalEvaluator()},
			}),
			logger,
		},
	})

	resp, err := a.RunText(context.Background(), "Summarize the quarterly report in one short sentence.").Collect()
	demo.Response(resp, err)
	fmt.Printf("Custom-evaluator loop stopped at the safety cap after %d iteration(s) (cap %d).\n\n", provider.calls, maxIterations)
}

// approvalEvaluator stops the loop once the latest response is approved, otherwise it
// re-invokes the agent with revision feedback.
func approvalEvaluator() loop.Evaluator {
	return loop.EvaluatorFunc(func(_ context.Context, lc *loop.Context) (loop.Evaluation, error) {
		if strings.Contains(lc.LastResponse.String(), "APPROVED") {
			return loop.Stop(), nil
		}
		return loop.Continue("The reviewer did not approve yet. Make it shorter and resubmit."), nil
	})
}

// scriptedProvider is a minimal deterministic provider that returns a canned response
// per invocation. It stands in for a hosted-model provider so the sample stays runnable
// without credentials while still exercising real loop re-invocation.
type scriptedProvider struct {
	reply func(call int) string
	calls int
}

func newScriptedProvider(reply func(call int) string) *scriptedProvider {
	return &scriptedProvider{reply: reply}
}

func (s *scriptedProvider) config() agent.ProviderConfig {
	return agent.ProviderConfig{
		ProviderName: "scripted-demo",
		Run: func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				s.calls++
				yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.TextContent{Text: s.reply(s.calls)}},
				}, nil)
			}
		},
	}
}
