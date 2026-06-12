// Copyright (c) Microsoft. All rights reserved.

package loop_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/message"
)

func TestLoop_StopsImmediately_InvokesOnce(t *testing.T) {
	capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + string(rune('0'+call)))
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Stop(), nil
			})},
		})},
	})

	resp, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if capture.callCount != 1 {
		t.Fatalf("callCount = %d, want 1", capture.callCount)
	}
	if got := resp.String(); got != "iteration 1" {
		t.Fatalf("response = %q, want %q", got, "iteration 1")
	}
}

func TestLoop_ContinuesUntilEvaluatorStops(t *testing.T) {
	capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + string(rune('0'+call)))
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
				if ctx.LastResponse.String() == "iteration 3" {
					return loop.Stop(), nil
				}
				return loop.Continue("custom follow-up"), nil
			})},
		})},
	})

	resp, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if capture.callCount != 3 {
		t.Fatalf("callCount = %d, want 3", capture.callCount)
	}
	got := messageTexts(resp.Messages)
	want := []string{"iteration 1", "custom follow-up", "iteration 2", "custom follow-up", "iteration 3"}
	if !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
	if got := capture.messagesPerCall[0][0].String(); got != "go" {
		t.Fatalf("first call input = %q, want %q", got, "go")
	}
	if got := capture.messagesPerCall[1][0].String(); got != "custom follow-up" {
		t.Fatalf("second call input = %q, want %q", got, "custom follow-up")
	}
}

func TestLoop_MultipleEvaluators_FirstContinueWins(t *testing.T) {
	capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + string(rune('0'+call)))
	})
	firstCalls := 0
	secondCalls := 0
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			Evaluators: []loop.Evaluator{
				loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
					firstCalls++
					if ctx.Iteration < 2 {
						return loop.Continue("from first"), nil
					}
					return loop.Stop(), nil
				}),
				loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
					secondCalls++
					return loop.Continue("from second"), nil
				}),
			},
			MaxIterations: 3,
		})},
	})

	_, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if firstCalls != 2 {
		t.Fatalf("firstCalls = %d, want 2", firstCalls)
	}
	if secondCalls != 1 {
		t.Fatalf("secondCalls = %d, want 1", secondCalls)
	}
	if got := capture.messagesPerCall[1][0].String(); got != "from first" {
		t.Fatalf("second call input = %q, want from first", got)
	}
	if got := capture.messagesPerCall[2][0].String(); got != "from second" {
		t.Fatalf("third call input = %q, want from second", got)
	}
}

func TestLoop_MaxIterationsCapsContinuation(t *testing.T) {
	capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + string(rune('0'+call)))
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Continue("again"), nil
			})},
			MaxIterations: 2,
		})},
	})

	_, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if capture.callCount != 2 {
		t.Fatalf("callCount = %d, want 2", capture.callCount)
	}
}

func TestLoop_ContinueWithMessagesSendsMessagesVerbatim(t *testing.T) {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	explicit := message.NewText("explicit")
	explicit.Role = message.RoleSystem
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
				if ctx.Iteration == 1 {
					return loop.ContinueWithMessages([]*message.Message{explicit}), nil
				}
				return loop.Stop(), nil
			})},
		})},
	})

	_, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if got := capture.messagesPerCall[1][0].Role; got != message.RoleSystem {
		t.Fatalf("role = %q, want %q", got, message.RoleSystem)
	}
	if got := capture.messagesPerCall[1][0].String(); got != "explicit" {
		t.Fatalf("message = %q, want explicit", got)
	}
}

func TestLoop_ContinueWithMessages_SeparateMessagesInCollectedResponse(t *testing.T) {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	first := message.NewText("first")
	second := message.NewText("second")
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
				if ctx.Iteration == 1 {
					return loop.ContinueWithMessages([]*message.Message{first, second}), nil
				}
				return loop.Stop(), nil
			})},
		})},
	})

	resp, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	got := messageTexts(resp.Messages)
	want := []string{"ack", "first", "second", "ack"}
	if !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
}

func TestLoop_FreshContextPerIteration_RebuildsFromInitialAndAggregatedFeedback(t *testing.T) {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			FreshContextPerIteration: true,
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
				if ctx.Iteration >= 3 {
					return loop.Stop(), nil
				}
				return loop.Continue("fb " + strconv.Itoa(ctx.Iteration)), nil
			})},
		})},
	})

	_, err := a.RunText(context.Background(), "original").Collect()
	if err != nil {
		t.Fatal(err)
	}

	secondCall := messageTexts(capture.messagesPerCall[1])
	if len(secondCall) != 2 || secondCall[0] != "original" {
		t.Fatalf("second call messages = %v, want original + feedback", secondCall)
	}
	if !strings.Contains(secondCall[1], "## Feedback") || !strings.Contains(secondCall[1], "fb 1") {
		t.Fatalf("second call feedback = %q", secondCall[1])
	}

	thirdCall := messageTexts(capture.messagesPerCall[2])
	if len(thirdCall) != 2 || thirdCall[0] != "original" {
		t.Fatalf("third call messages = %v, want original + feedback", thirdCall)
	}
	if !strings.Contains(thirdCall[1], "fb 1") || !strings.Contains(thirdCall[1], "fb 2") {
		t.Fatalf("third call feedback = %q", thirdCall[1])
	}
}

func TestLoop_FreshContextPerIteration_RecreatesSession(t *testing.T) {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			FreshContextPerIteration: true,
			MaxIterations:            3,
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Continue("again"), nil
			})},
		})},
	})

	_, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.sessionsPerCall) != 3 {
		t.Fatalf("sessions = %d, want 3", len(capture.sessionsPerCall))
	}
	if capture.sessionsPerCall[0] == nil || capture.sessionsPerCall[1] == nil || capture.sessionsPerCall[2] == nil {
		t.Fatalf("expected sessions on every call, got %v", capture.sessionsPerCall)
	}
	if capture.sessionsPerCall[0] == capture.sessionsPerCall[1] {
		t.Fatal("first and second call should use different sessions")
	}
	if capture.sessionsPerCall[1] == capture.sessionsPerCall[2] {
		t.Fatal("second and third call should use different sessions")
	}
}

func TestLoop_FreshContextPerIteration_SessionCreatedCallback(t *testing.T) {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	initialSession := &agent.Session{}
	var createdSessions []*agent.Session
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			FreshContextPerIteration: true,
			MaxIterations:            3,
			SessionCreatedCallback: func(_ context.Context, s *agent.Session) error {
				createdSessions = append(createdSessions, s)
				return nil
			},
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Continue("again"), nil
			})},
		})},
	})

	_, err := a.RunText(context.Background(), "go", agent.WithSession(initialSession)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(createdSessions) != 2 {
		t.Fatalf("created sessions = %d, want 2", len(createdSessions))
	}
	if createdSessions[0] != capture.sessionsPerCall[1] {
		t.Fatal("first callback session should match second call session")
	}
	if createdSessions[1] != capture.sessionsPerCall[2] {
		t.Fatal("second callback session should match third call session")
	}
}

func TestLoop_FreshContextPerIteration_SessionCreatedCallbackErrorStopsRun(t *testing.T) {
	wantErr := errors.New("session callback")
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			FreshContextPerIteration: true,
			MaxIterations:            3,
			SessionCreatedCallback: func(context.Context, *agent.Session) error {
				return wantErr
			},
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Continue("again"), nil
			})},
		})},
	})

	_, err := a.RunText(context.Background(), "go", agent.WithSession(&agent.Session{})).Collect()
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if capture.callCount != 1 {
		t.Fatalf("callCount = %d, want 1", capture.callCount)
	}
}

func TestLoop_NonStreamingReturnsLastResponseOnly(t *testing.T) {
	capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + strconv.Itoa(call))
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			NonStreamingReturnsLastResponseOnly: true,
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
				if ctx.LastResponse.String() == "iteration 3" {
					return loop.Stop(), nil
				}
				return loop.Continue("follow-up"), nil
			})},
		})},
	})

	resp, err := a.RunText(context.Background(), "go").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if capture.callCount != 3 {
		t.Fatalf("callCount = %d, want 3", capture.callCount)
	}
	got := messageTexts(resp.Messages)
	want := []string{"iteration 3"}
	if !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
}

func TestLoop_NonStreamingReturnsLastResponseOnly_IgnoredForStreaming(t *testing.T) {
	capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + strconv.Itoa(call))
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			NonStreamingReturnsLastResponseOnly: true,
			MaxIterations:                       2,
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Continue("follow-up"), nil
			})},
		})},
	})

	resp, err := a.RunText(context.Background(), "go", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	got := messageTexts(resp.Messages)
	want := []string{"iteration 1", "follow-up", "iteration 2"}
	if !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
}

func TestLoop_EvaluatorErrorStopsRun(t *testing.T) {
	wantErr := errors.New("boom")
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Stop(), wantErr
			})},
		})},
	})

	_, err := a.RunText(context.Background(), "go").Collect()
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if capture.callCount != 1 {
		t.Fatalf("callCount = %d, want 1", capture.callCount)
	}
}

func TestCompletionMarkerEvaluator(t *testing.T) {
	evaluator := loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{Marker: "DONE"})
	stopResponse := "all work finished DONE "
	stop, err := evaluator.Evaluate(context.Background(), contextWithResponse(stopResponse))
	if err != nil {
		t.Fatal(err)
	}
	if stop.ShouldReinvoke {
		t.Fatal("expected marker to stop the loop")
	}

	stopMid, err := evaluator.Evaluate(context.Background(), contextWithResponse("mentioning DONE but still working"))
	if err != nil {
		t.Fatal(err)
	}
	if stopMid.ShouldReinvoke {
		t.Fatal("expected marker in middle to stop the loop")
	}

	cont, err := evaluator.Evaluate(context.Background(), contextWithResponse("still working"))
	if err != nil {
		t.Fatal(err)
	}
	if !cont.ShouldReinvoke {
		t.Fatal("expected missing marker to continue")
	}
	if !strings.Contains(cont.Feedback, "DONE") || strings.Contains(cont.Feedback, "{completion_marker}") {
		t.Fatalf("feedback = %q", cont.Feedback)
	}
}

func TestCompletionMarkerEvaluator_CustomTemplateSubstitutesLastResponse(t *testing.T) {
	evaluator := loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{
		Marker:                  "FINISHED",
		FeedbackMessageTemplate: "Previous: {last_response}. Finish with {completion_marker}.",
	})

	evaluation, err := evaluator.Evaluate(context.Background(), contextWithResponse("candidate name: NoteNest"))
	if err != nil {
		t.Fatal(err)
	}
	want := "Previous: candidate name: NoteNest. Finish with FINISHED."
	if evaluation.Feedback != want {
		t.Fatalf("feedback = %q, want %q", evaluation.Feedback, want)
	}
}

func TestCompletionMarkerEvaluator_EmptyMarkerPanics(t *testing.T) {
	testCases := []loop.CompletionMarkerConfig{
		{},
		{Marker: "   "},
	}

	for _, config := range testCases {
		t.Run("marker="+strconv.Quote(config.Marker), func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic for empty marker")
				}
			}()
			_ = loop.NewCompletionMarkerEvaluator(config)
		})
	}
}

type captureAgent struct {
	run             func(call int, messages []*message.Message) []*agent.ResponseUpdate
	callCount       int
	messagesPerCall [][]*message.Message
	sessionsPerCall []*agent.Session
}

func newCaptureAgent(run func(call int, messages []*message.Message) []*agent.ResponseUpdate) *captureAgent {
	return &captureAgent{run: run}
}

func (c *captureAgent) provider() agent.ProviderConfig {
	return agent.ProviderConfig{
		Run: func(_ context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				c.callCount++
				c.messagesPerCall = append(c.messagesPerCall, cloneMessages(messages))
				session, _ := agent.GetOption(opts, agent.WithSession)
				c.sessionsPerCall = append(c.sessionsPerCall, session)
				for _, update := range c.run(c.callCount, messages) {
					if !yield(update, nil) {
						return
					}
				}
			}
		},
	}
}

func textUpdates(text string) []*agent.ResponseUpdate {
	return []*agent.ResponseUpdate{{
		Role:     message.RoleAssistant,
		Contents: []message.Content{&message.TextContent{Text: text}},
	}}
}

func messageTexts(messages []*message.Message) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.String())
	}
	return out
}

func contextWithResponse(text string) *loop.Context {
	var resp agent.Response
	resp.Update(textUpdates(text)[0])
	return &loop.Context{LastResponse: &resp}
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
