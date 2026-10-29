// Copyright (c) Microsoft. All rights reserved.

package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
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
	if got, want := messageTexts(capture.messagesPerCall[1]), []string{"go", "iteration 1", "custom follow-up"}; !slices.Equal(got, want) {
		t.Fatalf("second call input = %v, want %v", got, want)
	}
}

func TestLoop_DefaultHistoryWithoutExplicitSession(t *testing.T) {
	for _, tc := range []struct {
		name            string
		stream          bool
		serviceID       string
		assignServiceID bool
		historyProvider agent.HistoryProvider
	}{
		{name: "local history"},
		{name: "streaming local history", stream: true},
		{name: "service history", serviceID: "conversation-1"},
		{name: "service history assigned during run", assignServiceID: true},
		{name: "configured history", historyProvider: agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
				updates := textUpdates("draft")
				if tc.serviceID != "" {
					updates[0].ConversationID = new(tc.serviceID)
				}
				if tc.assignServiceID {
					updates[0].ConversationID = new("conversation-1")
				}
				return updates
			})
			provider := capture.provider()
			a := agent.New(provider, agent.Config{
				HistoryProvider: tc.historyProvider,
				Middlewares: []agent.Middleware{loop.New(loop.Config{
					Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
						if ctx.Iteration == 1 {
							return loop.Continue("make it shorter"), nil
						}
						return loop.Stop(), nil
					})},
				})},
			})
			for _, prompt := range []string{"first request", "independent request"} {
				capture.messagesPerCall = nil
				if _, err := a.RunText(t.Context(), prompt, agent.Stream(tc.stream), agent.WithServiceID(tc.serviceID)).Collect(); err != nil {
					t.Fatal(err)
				}
				if len(capture.messagesPerCall) != 2 {
					t.Fatalf("provider calls = %d, want 2", len(capture.messagesPerCall))
				}
				if got := messageTexts(capture.messagesPerCall[0]); !slices.Equal(got, []string{prompt}) {
					t.Fatalf("first input = %v, want [%s]", got, prompt)
				}
				want := []string{prompt, "draft", "make it shorter"}
				if tc.serviceID != "" || tc.assignServiceID {
					want = []string{"make it shorter"}
				}
				if got := messageTexts(capture.messagesPerCall[1]); !slices.Equal(got, want) {
					t.Fatalf("second input = %v, want %v", got, want)
				}
			}
		})
	}
}

func TestLoop_RefreshesConversationIDAfterEachInvocation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run("stream="+strconv.FormatBool(stream), func(t *testing.T) {
			var receivedIDs []string
			a := agent.New(agent.ProviderConfig{Run: func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					id, _ := agent.GetOption(opts, agent.WithServiceID)
					receivedIDs = append(receivedIDs, id)
					nextID := "conversation-" + strconv.Itoa(len(receivedIDs))
					yield(&agent.ResponseUpdate{ConversationID: &nextID, Contents: message.Contents{&message.TextContent{Text: "draft"}}}, nil)
				}
			}}, agent.Config{Middlewares: []agent.Middleware{loop.New(loop.Config{
				Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
					if ctx.Iteration == 1 {
						return loop.Continue("refine"), nil
					}
					return loop.Stop(), nil
				})},
			})}})
			session := &agent.Session{}
			session.SetServiceID("conversation-0")
			if _, err := a.RunText(t.Context(), "hello", agent.WithSession(session), agent.Stream(stream)).Collect(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(receivedIDs, []string{"conversation-0", "conversation-1"}) || session.ServiceID() != "conversation-2" {
				t.Fatalf("provider IDs = %v, session ID = %q", receivedIDs, session.ServiceID())
			}
		})
	}
}

func TestLoop_ContextProviderDoesNotStoreReplayedHistory(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run("stream="+strconv.FormatBool(stream), func(t *testing.T) {
			source := message.Source{ID: "caller"}
			prompt := message.NewText("first request").WithSource(source)
			feedback := message.NewText("make it shorter").WithSource(source)
			var provided, storedRequests, storedResponses []string
			contextProvider := agent.NewContextProvider(agent.ContextProviderConfig{
				SourceID: "memory",
				Provide: func(_ context.Context, ctx agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
					provided = append(provided, messageTexts(ctx.Messages)...)
					return nil, nil, nil
				},
				Store: func(_ context.Context, ctx agent.InvokedContext) error {
					storedRequests = append(storedRequests, messageTexts(ctx.RequestMessages)...)
					storedResponses = append(storedResponses, messageTexts(ctx.ResponseMessages)...)
					return nil
				},
			})
			capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
				return textUpdates("draft " + strconv.Itoa(call))
			})
			a := agent.New(capture.provider(), agent.Config{
				ContextProviders: []agent.ContextProvider{contextProvider},
				Middlewares: []agent.Middleware{loop.New(loop.Config{
					Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
						if ctx.Iteration == 1 {
							return loop.ContinueWithMessages([]*message.Message{feedback}), nil
						}
						return loop.Stop(), nil
					})},
				})},
			})
			resp, err := a.Run(t.Context(), []*message.Message{prompt}, agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			wantRequests := []string{"first request", "make it shorter"}
			if !slices.Equal(provided, wantRequests) {
				t.Errorf("context retrieval inputs = %v, want %v", provided, wantRequests)
			}
			if !slices.Equal(storedRequests, wantRequests) {
				t.Errorf("stored requests = %v, want %v", storedRequests, wantRequests)
			}
			if want := []string{"draft 1", "draft 2"}; !slices.Equal(storedResponses, want) {
				t.Errorf("stored responses = %v, want %v", storedResponses, want)
			}
			if len(capture.messagesPerCall) != 2 {
				t.Fatalf("provider calls = %d, want 2", len(capture.messagesPerCall))
			}
			if got, want := messageTexts(capture.messagesPerCall[1]), []string{"first request", "draft 1", "make it shorter"}; !slices.Equal(got, want) {
				t.Errorf("second input = %v, want %v", got, want)
			}
			if prompt.Source != source || feedback.Source != source {
				t.Error("caller message sources changed")
			}
			for _, msg := range resp.Messages {
				if msg.Role != message.RoleAssistant {
					continue
				}
				if msg.Source != (message.Source{}) {
					t.Errorf("source of %q = %v, want unchanged external source", msg.String(), msg.Source)
				}
			}
		})
	}
}

func TestLoop_AutoApprovalPreservesHistoryWithoutDuplicatingInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream bool
		cap    *int
	}{
		{name: "non-streaming"},
		{name: "streaming", stream: true},
		{name: "approval iteration cap", cap: new(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var toolCalls int
			capture := newCaptureAgent(func(call int, _ []*message.Message) []*agent.ResponseUpdate {
				if call == 1 {
					return []*agent.ResponseUpdate{{Role: message.RoleAssistant, Contents: message.Contents{
						&message.FunctionCallContent{CallID: "call-1", Name: "lookup", Arguments: `{}`},
					}}}
				}
				return textUpdates("answer")
			})
			provider := capture.provider()
			provider.Middlewares = []agent.Middleware{toolautocall.New(toolautocall.Config{})}
			a := agent.New(provider, agent.Config{
				Tools: []tool.Tool{tool.ApprovalRequiredFunc(functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
					toolCalls++
					return "found", nil
				}))},
				Middlewares: []agent.Middleware{
					loop.New(loop.Config{Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
						if ctx.Iteration == 1 {
							return loop.Continue("make it shorter"), nil
						}
						return loop.Stop(), nil
					})}}),
					toolapproval.New(toolapproval.Config{
						MaxAutoApprovalIterations: tc.cap,
						AutoApprovalRules: []toolapproval.AutoApprovalRule{func(context.Context, *toolapproval.ToolAutoApprovalRuleContext) (bool, error) {
							return true, nil
						}},
					}),
				},
			})
			if _, err := a.RunText(t.Context(), "lookup order 42", agent.Stream(tc.stream)).Collect(); err != nil {
				t.Fatal(err)
			}
			if capture.callCount != 3 || toolCalls != 1 {
				t.Fatalf("provider calls = %d, tool calls = %d, want 3 and 1", capture.callCount, toolCalls)
			}
			for i, messages := range capture.messagesPerCall {
				var prompts, results int
				for _, msg := range messages {
					if msg.String() == "lookup order 42" {
						prompts++
					}
					for _, content := range msg.Contents {
						if result, ok := content.(*message.FunctionResultContent); ok && result.CallID == "call-1" && result.Result == "found" {
							results++
						}
					}
				}
				if prompts != 1 || (i > 0 && results != 1) {
					t.Errorf("provider call %d: prompt copies = %d, tool results = %d, want 1 each after approval", i+1, prompts, results)
				}
			}
			if got := capture.messagesPerCall[2]; got[len(got)-1].String() != "make it shorter" {
				t.Fatal("next loop iteration lost evaluator feedback")
			}
		})
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
			MaxIterations: new(3),
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
	if got, want := messageTexts(capture.messagesPerCall[1]), []string{"go", "iteration 1", "from first"}; !slices.Equal(got, want) {
		t.Fatalf("second call input = %v, want %v", got, want)
	}
	if got, want := messageTexts(capture.messagesPerCall[2]), []string{"go", "iteration 1", "from first", "iteration 2", "from second"}; !slices.Equal(got, want) {
		t.Fatalf("third call input = %v, want %v", got, want)
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
			MaxIterations: new(2),
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
	if got, want := messageTexts(capture.messagesPerCall[1]), []string{"go", "ack", "explicit"}; !slices.Equal(got, want) {
		t.Fatalf("second call input = %v, want %v", got, want)
	}
	if got := capture.messagesPerCall[1][2].Role; got != message.RoleSystem {
		t.Fatalf("role = %q, want %q", got, message.RoleSystem)
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
			MaxIterations:            new(3),
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

func TestLoop_FreshContextPerIteration_SnapshotErrorPreservesCause(t *testing.T) {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("unused")
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			FreshContextPerIteration: true,
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
				return loop.Stop(), nil
			})},
		})},
	})
	session := &agent.Session{}
	session.Set("unsupported", func() {})

	_, err := a.RunText(t.Context(), "go", agent.WithSession(session)).Collect()
	if _, ok := errors.AsType[*json.UnsupportedTypeError](err); !ok {
		t.Fatalf("RunText() error = %v, want wrapped json.UnsupportedTypeError", err)
	}
	if !strings.Contains(err.Error(), "loop: snapshot session") {
		t.Fatalf("RunText() error = %v, want snapshot context", err)
	}
	if capture.callCount != 0 {
		t.Fatalf("provider call count = %d, want 0", capture.callCount)
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
			MaxIterations:            new(3),
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
	if capture.callCount != 3 {
		t.Fatalf("callCount = %d, want 3", capture.callCount)
	}
	if slices.Contains(createdSessions, initialSession) {
		t.Fatal("session-created callback should not report the caller session")
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
			MaxIterations:            new(3),
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
			MaxIterations:                       new(2),
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
		FeedbackMessageTemplate: new("Previous: {last_response}. Finish with {completion_marker}."),
	})

	evaluation, err := evaluator.Evaluate(context.Background(), contextWithResponse("candidate name: NoteNest"))
	if err != nil {
		t.Fatal(err)
	}
	if !evaluation.ShouldReinvoke {
		t.Fatal("expected missing marker to continue")
	}
	want := "Previous: candidate name: NoteNest. Finish with FINISHED."
	if evaluation.Feedback != want {
		t.Fatalf("feedback = %q, want %q", evaluation.Feedback, want)
	}
}

func TestCompletionMarkerEvaluator_PreservesMarkerWhitespace(t *testing.T) {
	evaluator := loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{
		Marker:                  "\nDONE\n",
		FeedbackMessageTemplate: new("emit=<{completion_marker}>"),
	})

	cont, err := evaluator.Evaluate(context.Background(), contextWithResponse("NOT_DONE_YET"))
	if err != nil {
		t.Fatal(err)
	}
	if !cont.ShouldReinvoke {
		t.Fatal("expected a response without the full marker to continue")
	}
	if want := "emit=<\nDONE\n>"; cont.Feedback != want {
		t.Fatalf("feedback = %q, want %q", cont.Feedback, want)
	}

	stop, err := evaluator.Evaluate(context.Background(), contextWithResponse("finished\nDONE\n"))
	if err != nil {
		t.Fatal(err)
	}
	if stop.ShouldReinvoke {
		t.Fatal("expected the full marker to stop the loop")
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

func TestCompletionMarkerEvaluator_CustomTemplateHonored(t *testing.T) {
	e := loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{
		Marker: "FINISHED", FeedbackMessageTemplate: new("Keep going and finish with {completion_marker} when done."),
	})
	got, err := e.Evaluate(t.Context(), contextWithResponse("still working"))
	if err != nil || !got.ShouldReinvoke || got.Feedback != "Keep going and finish with FINISHED when done." {
		t.Fatalf("evaluation = %+v, %v", got, err)
	}
}

func TestCompletionMarkerEvaluator_DefaultDoesNotIncludeLastResponse(t *testing.T) {
	e := loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{Marker: "DONE"})
	got, err := e.Evaluate(t.Context(), contextWithResponse("candidate name: NoteNest"))
	want := "Continue working on the request. When you have fully completed the task, end your response with the marker 'DONE' to indicate completion."
	if err != nil || !got.ShouldReinvoke || got.Feedback != want || strings.Contains(got.Feedback, "NoteNest") {
		t.Fatalf("evaluation = %+v, %v, want feedback %q", got, err, want)
	}
}

func TestCompletionMarkerEvaluator_MarkerPresentStops(t *testing.T) {
	e := loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{Marker: "DONE"})
	got, err := e.Evaluate(t.Context(), contextWithResponse("all DONE here"))
	if err != nil || got.ShouldReinvoke {
		t.Fatalf("evaluation = %+v, %v", got, err)
	}
}

func TestCompletionMarkerEvaluator_NilContext(t *testing.T) {
	e := loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{Marker: "DONE"})
	if _, err := e.Evaluate(t.Context(), nil); err == nil {
		t.Fatal("expected a nil-context error")
	}
}

func TestDelegateEvaluator_NilContext(t *testing.T) {
	e := loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) { return loop.Stop(), nil })
	if _, err := e.Evaluate(t.Context(), nil); err == nil {
		t.Fatal("expected a nil-context error")
	}
}

func TestDelegateEvaluator_InvokesAndReturnsEvaluation(t *testing.T) {
	invoked := false
	want := loop.Continue("feedback")
	e := loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
		invoked = true
		return want, nil
	})
	got, err := e.Evaluate(t.Context(), contextWithResponse("response"))
	if err != nil || !invoked || got.ShouldReinvoke != want.ShouldReinvoke || got.Feedback != want.Feedback || !slices.Equal(got.Messages, want.Messages) {
		t.Fatalf("invoked=%v, evaluation=%+v, err=%v", invoked, got, err)
	}
}

func TestDelegateEvaluator_PassesSameLoopContext(t *testing.T) {
	ctx := contextWithResponse("response")
	var received *loop.Context
	e := loop.EvaluatorFunc(func(_ context.Context, lc *loop.Context) (loop.Evaluation, error) {
		received = lc
		return loop.Stop(), nil
	})
	if _, err := e.Evaluate(t.Context(), ctx); err != nil || received != ctx {
		t.Fatalf("received context = %p, want %p, err=%v", received, ctx, err)
	}
}

func TestDelegateEvaluator_ForwardsCancellationContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var received context.Context
	e := loop.EvaluatorFunc(func(ctx context.Context, _ *loop.Context) (loop.Evaluation, error) {
		received = ctx
		return loop.Stop(), nil
	})
	if _, err := e.Evaluate(ctx, contextWithResponse("response")); err != nil || received != ctx {
		t.Fatalf("received cancellation context = %v, err=%v", received, err)
	}
}

func TestLoopContext_FeedbackSettable(t *testing.T) {
	ctx := contextWithResponse("response")
	ctx.Feedback = []string{"first", ""}
	if !slices.Equal(ctx.Feedback, []string{"first", ""}) {
		t.Fatalf("feedback = %q", ctx.Feedback)
	}
}

func TestLoopContext_PublicConstructionSupportsEvaluator(t *testing.T) {
	ctx := &loop.Context{InitialMessages: []*message.Message{message.NewText("go")}, LastResponse: contextWithResponse("done").LastResponse}
	e := loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.LastResponse.String() == "done" {
			return loop.Stop(), nil
		}
		return loop.Continue(""), nil
	})
	if got, err := e.Evaluate(t.Context(), ctx); err != nil || got.ShouldReinvoke {
		t.Fatalf("evaluation = %+v, %v", got, err)
	}
}

func TestLoopEvaluation_Stop(t *testing.T) {
	if got := loop.Stop(); got.ShouldReinvoke || got.Feedback != "" {
		t.Fatalf("stop = %+v", got)
	}
}

func TestLoopEvaluation_ContinueWithoutFeedback(t *testing.T) {
	if got := loop.Continue(""); !got.ShouldReinvoke || got.Feedback != "" {
		t.Fatalf("continue = %+v", got)
	}
}

func TestLoopEvaluation_WhitespaceFeedback(t *testing.T) {
	for _, feedback := range []string{"", "   ", "\t\n"} {
		t.Run(strconv.Quote(feedback), func(t *testing.T) {
			if got := loop.Continue(feedback); !got.ShouldReinvoke || got.Feedback != "" {
				t.Fatalf("continue = %+v", got)
			}
		})
	}
}

func loopWhile(fn func(*loop.Context) bool) loop.Evaluator {
	return loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if fn(ctx) {
			return loop.Continue(""), nil
		}
		return loop.Stop(), nil
	})
}

func collectLoopPort(t *testing.T, capture *captureAgent, cfg loop.Config, prompt string, opts ...agent.Option) *agent.Response {
	t.Helper()
	run := capture.provider().Run
	inner := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for update, err := range run(ctx, messages, options...) {
				if update != nil {
					cloned := *update
					if cloned.MessageID == "" {
						cloned.MessageID = "iteration-" + strconv.Itoa(capture.callCount)
					}
					update = &cloned
				}
				if !yield(update, err) {
					return
				}
			}
		}
	}
	if _, ok := agent.GetOption(opts, agent.WithSession); !ok {
		opts = append(slices.Clone(opts), agent.WithSession(&agent.Session{}))
	}
	response, err := agent.ResponseStream(loop.New(cfg).Run(inner, t.Context(), []*message.Message{message.NewText(prompt)}, opts...)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestLoopConstructor_ValidArguments(t *testing.T) {
	if middleware := loop.New(loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return false })}}); middleware == nil {
		t.Fatal("valid constructor returned nil")
	}
}

func TestLoopRun_PredicateAggregatesAllIterations(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + strconv.Itoa(i))
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool {
		return ctx.LastResponse.String() != "iteration 3"
	})}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 3 || !slices.Equal(messageTexts(resp.Messages), []string{"iteration 1", "iteration 2", "iteration 3"}) {
		t.Fatalf("calls=%d, transcript=%v", c.callCount, messageTexts(resp.Messages))
	}
}

func TestLoopRun_TranscriptWithoutUsage(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + strconv.Itoa(i))
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool {
		return ctx.LastResponse.String() != "iteration 3"
	})}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 3 || !slices.Equal(messageTexts(resp.Messages), []string{"iteration 1", "iteration 2", "iteration 3"}) {
		t.Fatalf("calls=%d, transcript=%v", c.callCount, messageTexts(resp.Messages))
	}
	if usage := resp.Usage(); usage.InputTokenCount != 0 || usage.OutputTokenCount != 0 || usage.TotalTokenCount != 0 || len(usage.AdditionalCounts) != 0 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestLoopRun_LastResponseOnly(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("iteration " + strconv.Itoa(i))
	})
	resp := collectLoopPort(t, c, loop.Config{
		NonStreamingReturnsLastResponseOnly: true,
		Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool {
			return ctx.LastResponse.String() != "iteration 3"
		})},
	}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 3 || len(resp.Messages) != 1 || resp.String() != "iteration 3" {
		t.Fatalf("calls=%d, response=%v", c.callCount, messageTexts(resp.Messages))
	}
}

func TestLoopRun_ContinueWithoutFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool {
		return ctx.Iteration < 2
	})}}, "original", agent.WithSession(&agent.Session{}))
	if c.callCount != 2 || !slices.Equal(messageTexts(c.messagesPerCall[0]), []string{"original"}) || len(c.messagesPerCall[1]) != 0 {
		t.Fatalf("calls=%d, inputs=%v", c.callCount, c.messagesPerCall)
	}
}

func TestLoopRun_EvaluatorFeedbackInjectedVerbatim(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			return loop.Continue("custom follow-up"), nil
		}
		return loop.Stop(), nil
	})}}, "original", agent.WithSession(&agent.Session{}))
	if c.callCount != 2 || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"custom follow-up"}) {
		t.Fatalf("calls=%d, next input=%v", c.callCount, c.messagesPerCall)
	}
}

func TestLoopRun_ExplicitMessagesAndAlignedFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	var feedback []string
	collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			sys := message.NewText("sys")
			sys.Role = message.RoleSystem
			return loop.ContinueWithMessages([]*message.Message{sys, message.NewText("explicit")}), nil
		}
		feedback = slices.Clone(ctx.Feedback)
		return loop.Stop(), nil
	})}}, "original", agent.WithSession(&agent.Session{}))
	if c.callCount != 2 || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"sys", "explicit"}) || c.messagesPerCall[1][0].Role != message.RoleSystem || !slices.Equal(feedback, []string{""}) {
		t.Fatalf("calls=%d, feedback=%q, inputs=%v", c.callCount, feedback, c.messagesPerCall)
	}
}

func TestLoopRun_GlobalCap(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("working") })
	resp := collectLoopPort(t, c, loop.Config{MaxIterations: new(3), Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return true })}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 3 || !slices.Equal(messageTexts(resp.Messages), []string{"working", "working", "working"}) {
		t.Fatalf("calls=%d, response=%v", c.callCount, messageTexts(resp.Messages))
	}
}

func TestLoopRun_PendingApprovalStops(t *testing.T) {
	request := &message.ToolApprovalRequestContent{RequestID: "req1", ToolCall: &message.FunctionCallContent{CallID: "call1", Name: "MyTool"}}
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return []*agent.ResponseUpdate{{Role: message.RoleAssistant, Contents: message.Contents{request}}}
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return true })}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 1 {
		t.Fatalf("calls=%d, want 1", c.callCount)
	}
	found := false
	for content := range resp.Contents() {
		if _, ok := content.(*message.ToolApprovalRequestContent); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("pending approval was lost")
	}
}

func TestLoopRun_CreatesSessionWhenNoneSupplied(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("done") })
	provider := c.provider()
	created := 0
	provider.CreateSession = func(context.Context, *agent.Session, ...agent.Option) error { created++; return nil }
	a := agent.New(provider, agent.Config{Middlewares: []agent.Middleware{loop.New(loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return false })}})}})
	resp, err := a.RunText(t.Context(), "go").Collect()
	if err != nil || resp.String() != "done" || created != 1 {
		t.Fatalf("response=%v, created=%d, err=%v", resp, created, err)
	}
}

func TestLoopRun_NonFreshLatestFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("partial") })
	collectLoopPort(t, c, loop.Config{MaxIterations: new(2), Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
		return loop.Continue("fix it"), nil
	})}}, "original", agent.WithSession(&agent.Session{}))
	if c.callCount != 2 || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"fix it"}) {
		t.Fatalf("calls=%d, inputs=%v", c.callCount, c.messagesPerCall)
	}
}

func TestLoopRun_EmptyFeedbackDoesNotReuseStaleFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("partial") })
	collectLoopPort(t, c, loop.Config{MaxIterations: new(3), Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration == 1 {
			return loop.Continue("feedback 1"), nil
		}
		return loop.Continue(""), nil
	})}}, "original", agent.WithSession(&agent.Session{}))
	if c.callCount != 3 || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"feedback 1"}) || len(c.messagesPerCall[2]) != 0 {
		t.Fatalf("calls=%d, inputs=%v", c.callCount, c.messagesPerCall)
	}
}

func TestLoopRun_FeedbackSharedAcrossEvaluators(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("partial") })
	var observed []int
	collectLoopPort(t, c, loop.Config{MaxIterations: new(5), Evaluators: []loop.Evaluator{
		loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
			if ctx.Iteration < 3 {
				return loop.Continue("fb " + strconv.Itoa(ctx.Iteration)), nil
			}
			return loop.Stop(), nil
		}),
		loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
			observed = append(observed, len(ctx.Feedback))
			return loop.Stop(), nil
		}),
	}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 3 || !slices.Equal(observed, []int{2}) {
		t.Fatalf("calls=%d, observed feedback lengths=%v", c.callCount, observed)
	}
}

func TestLoopRun_ExplicitMessagesRecordEmptyFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("partial") })
	var log []string
	collectLoopPort(t, c, loop.Config{MaxIterations: new(5), Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		switch ctx.Iteration {
		case 1:
			return loop.Continue("needs work"), nil
		case 2:
			return loop.ContinueWithMessages([]*message.Message{message.NewText("explicit")}), nil
		default:
			log = slices.Clone(ctx.Feedback)
			return loop.Stop(), nil
		}
	})}}, "go", agent.WithSession(&agent.Session{}))
	if !slices.Equal(log, []string{"needs work", ""}) {
		t.Fatalf("feedback log=%q", log)
	}
}

func TestLoopRun_NonFreshReusesCreatedSession(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("x") })
	provider := c.provider()
	var created *agent.Session
	provider.CreateSession = func(_ context.Context, s *agent.Session, _ ...agent.Option) error { created = s; return nil }
	a := agent.New(provider, agent.Config{Middlewares: []agent.Middleware{loop.New(loop.Config{MaxIterations: new(3), Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
		return loop.Continue("more"), nil
	})}})}})
	if _, err := a.RunText(t.Context(), "go").Collect(); err != nil {
		t.Fatal(err)
	}
	if c.callCount != 3 || created == nil || !slices.Equal(c.sessionsPerCall, []*agent.Session{created, created, created}) {
		t.Fatalf("created=%p, calls=%d, sessions=%v", created, c.callCount, c.sessionsPerCall)
	}
}

func TestLoopRun_FreshRebuildsInitialAndFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("partial") })
	collectLoopPort(t, c, loop.Config{FreshContextPerIteration: true, MaxIterations: new(3), Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		return loop.Continue("fb " + strconv.Itoa(ctx.Iteration)), nil
	})}}, "original task")
	if c.callCount != 3 {
		t.Fatalf("calls=%d", c.callCount)
	}
	for i := 1; i < 3; i++ {
		got := messageTexts(c.messagesPerCall[i])
		if len(got) != 2 || got[0] != "original task" || !strings.Contains(got[1], "## Feedback") || !strings.Contains(got[1], "fb 1") || i == 2 && !strings.Contains(got[1], "fb 2") {
			t.Fatalf("iteration %d input=%q", i+1, got)
		}
	}
}

func TestLoopRun_FreshRecreatesSession(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("x") })
	collectLoopPort(t, c, loop.Config{FreshContextPerIteration: true, MaxIterations: new(3), Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
		return loop.Continue("more"), nil
	})}}, "go")
	if c.callCount != 3 || c.sessionsPerCall[0] == c.sessionsPerCall[1] || c.sessionsPerCall[1] == c.sessionsPerCall[2] {
		t.Fatalf("calls=%d, sessions=%v", c.callCount, c.sessionsPerCall)
	}
}

func TestLoopRun_FreshExplicitMessagesRecreateSession(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("x") })
	collectLoopPort(t, c, loop.Config{FreshContextPerIteration: true, MaxIterations: new(3), Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
		return loop.ContinueWithMessages([]*message.Message{message.NewText("explicit")}), nil
	})}}, "go")
	if c.callCount != 3 || c.sessionsPerCall[0] == c.sessionsPerCall[1] || c.sessionsPerCall[1] == c.sessionsPerCall[2] {
		t.Fatalf("calls=%d, sessions=%v", c.callCount, c.sessionsPerCall)
	}
	for _, input := range c.messagesPerCall[1:] {
		if !slices.Equal(messageTexts(input), []string{"explicit"}) {
			t.Fatalf("explicit input=%v", input)
		}
	}
}

func TestLoopRun_CallerSessionDoesNotTriggerCallback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("x") })
	var observed []*agent.Session
	collectLoopPort(t, c, loop.Config{MaxIterations: new(3), SessionCreatedCallback: func(_ context.Context, s *agent.Session) error {
		observed = append(observed, s)
		return nil
	}, Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) {
		return loop.Continue("more"), nil
	})}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 3 || len(observed) != 0 {
		t.Fatalf("calls=%d, callbacks=%v", c.callCount, observed)
	}
}

func TestLoopRun_FirstReinvokeShortCircuitsEvaluators(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	first, second := 0, 0
	collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{
		loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
			first++
			if ctx.Iteration < 2 {
				return loop.Continue("from first"), nil
			}
			return loop.Stop(), nil
		}),
		loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) { second++; return loop.Stop(), nil }),
	}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 2 || first != 2 || second != 1 || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"from first"}) {
		t.Fatalf("calls=%d, first=%d, second=%d, inputs=%v", c.callCount, first, second, c.messagesPerCall)
	}
}

func TestLoopRun_LaterEvaluatorCanContinue(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{
		loopWhile(func(*loop.Context) bool { return false }),
		loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
			if ctx.Iteration < 2 {
				return loop.Continue("from second"), nil
			}
			return loop.Stop(), nil
		}),
	}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 2 || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"from second"}) {
		t.Fatalf("calls=%d, inputs=%v", c.callCount, c.messagesPerCall)
	}
}

func TestLoopRun_AllEvaluatorsStop(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("done") })
	collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return false }), loopWhile(func(*loop.Context) bool { return false })}}, "go", agent.WithSession(&agent.Session{}))
	if c.callCount != 1 {
		t.Fatalf("calls=%d, want 1", c.callCount)
	}
}

func TestLoopRun_AggregatesFeedbackAndResponses(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	resp := collectLoopPort(t, c, loop.Config{OnBehalfOfAuthorName: "loop", Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			return loop.Continue("fix it"), nil
		}
		return loop.Stop(), nil
	})}}, "original", agent.WithSession(&agent.Session{}))
	if !slices.Equal(messageTexts(resp.Messages), []string{"ack", "fix it", "ack"}) || resp.Messages[1].Role != message.RoleUser || resp.Messages[1].AuthorName != "loop" || c.messagesPerCall[1][0].AuthorName != "loop" {
		t.Fatalf("response=%v, feedback=%+v", messageTexts(resp.Messages), resp.Messages)
	}
}

func TestLoopRun_ExplicitAuthorPreserved(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	resp := collectLoopPort(t, c, loop.Config{OnBehalfOfAuthorName: "loop", Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			explicit := message.NewText("explicit")
			explicit.AuthorName = "evaluator"
			return loop.ContinueWithMessages([]*message.Message{explicit}), nil
		}
		return loop.Stop(), nil
	})}}, "original", agent.WithSession(&agent.Session{}))
	if !slices.Equal(messageTexts(resp.Messages), []string{"ack", "explicit", "ack"}) || resp.Messages[1].AuthorName != "evaluator" {
		t.Fatalf("response=%v", resp.Messages)
	}
}

func TestLoopRun_FreshSurfacesOnlyAggregatedFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	resp := collectLoopPort(t, c, loop.Config{FreshContextPerIteration: true, OnBehalfOfAuthorName: "loop", Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			return loop.Continue("fix it"), nil
		}
		return loop.Stop(), nil
	})}}, "original")
	if len(resp.Messages) != 3 || resp.Messages[1].AuthorName != "loop" || !strings.Contains(resp.Messages[1].String(), "fix it") {
		t.Fatalf("response=%v", resp.Messages)
	}
	if slices.Contains(messageTexts(resp.Messages), "original") || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"original", resp.Messages[1].String()}) {
		t.Fatalf("response=%v, next input=%v", messageTexts(resp.Messages), messageTexts(c.messagesPerCall[1]))
	}
}

func TestLoopRun_ExcludedFeedbackStillSentToAgent(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	resp := collectLoopPort(t, c, loop.Config{ExcludeOnBehalfOfMessages: true, Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			return loop.Continue("fix it"), nil
		}
		return loop.Stop(), nil
	})}}, "original", agent.WithSession(&agent.Session{}))
	if !slices.Equal(messageTexts(resp.Messages), []string{"ack", "ack"}) || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"fix it"}) {
		t.Fatalf("response=%v, inputs=%v", messageTexts(resp.Messages), c.messagesPerCall)
	}
}

func loopUsageForCall(call int) message.UsageDetails {
	input := []int64{2, 11, 29}
	output := []int64{3, 5, 7}
	return message.UsageDetails{InputTokenCount: input[call-1], OutputTokenCount: output[call-1], TotalTokenCount: input[call-1] + output[call-1]}
}

func assertLoopUsage(t *testing.T, response *agent.Response, input, output, total int64) {
	t.Helper()
	got := response.Usage()
	if got.InputTokenCount != input || got.OutputTokenCount != output || got.TotalTokenCount != total {
		t.Fatalf("usage=%+v, want input=%d output=%d total=%d", got, input, output, total)
	}
}

func TestLoopRun_AggregatesUsageAcrossIterations(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("iteration " + strconv.Itoa(i))
		u[0].Contents = append(u[0].Contents, &message.UsageContent{Details: message.UsageDetails{InputTokenCount: int64(i * 10), OutputTokenCount: int64(i), TotalTokenCount: int64(i * 11)}})
		return u
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool { return ctx.Iteration < 3 })}}, "go")
	if c.callCount != 3 {
		t.Fatalf("calls=%d", c.callCount)
	}
	assertLoopUsage(t, resp, 60, 6, 66)
}

func TestLoopRun_GlobalCapAggregatesUsage(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("iteration " + strconv.Itoa(i))
		u[0].Contents = append(u[0].Contents, &message.UsageContent{Details: loopUsageForCall(i)})
		return u
	})
	resp := collectLoopPort(t, c, loop.Config{MaxIterations: new(3), Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return true })}}, "go")
	if c.callCount != 3 {
		t.Fatalf("calls=%d", c.callCount)
	}
	assertLoopUsage(t, resp, 42, 15, 57)
}

func TestLoopRun_LaterApprovalPreservesUsage(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("iteration " + strconv.Itoa(i))
		if i == 3 {
			u[0].Contents = message.Contents{&message.ToolApprovalRequestContent{RequestID: "req1", ToolCall: &message.FunctionCallContent{CallID: "call1", Name: "MyTool"}}}
		}
		u[0].Contents = append(u[0].Contents, &message.UsageContent{Details: loopUsageForCall(i)})
		return u
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return true })}}, "go")
	if c.callCount != 3 {
		t.Fatalf("calls=%d", c.callCount)
	}
	found := false
	for content := range resp.Contents() {
		if _, ok := content.(*message.ToolApprovalRequestContent); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("pending approval was lost")
	}
	assertLoopUsage(t, resp, 42, 15, 57)
}

func TestLoopRun_SkipsIterationsWithoutUsage(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("iteration " + strconv.Itoa(i))
		if i != 2 {
			u[0].Contents = append(u[0].Contents, &message.UsageContent{Details: message.UsageDetails{InputTokenCount: 7, TotalTokenCount: 7}})
		}
		return u
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool { return ctx.Iteration < 3 })}}, "go")
	if c.callCount != 3 {
		t.Fatalf("calls=%d", c.callCount)
	}
	assertLoopUsage(t, resp, 14, 0, 14)
}

func TestLoopRun_UsageAggregationDoesNotMutateInnerUsage(t *testing.T) {
	var inner []*message.UsageContent
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		usage := &message.UsageContent{Details: message.UsageDetails{InputTokenCount: 10, OutputTokenCount: 5, TotalTokenCount: 15}}
		inner = append(inner, usage)
		u := textUpdates("iteration " + strconv.Itoa(i))
		u[0].Contents = append(u[0].Contents, usage)
		return u
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool { return ctx.Iteration < 3 })}}, "go")
	aggregate := resp.Usage()
	aggregate.InputTokenCount = 0
	for _, usage := range inner {
		if usage.Details.InputTokenCount != 10 || usage.Details.OutputTokenCount != 5 || usage.Details.TotalTokenCount != 15 {
			t.Fatalf("inner usage was mutated: %+v", usage.Details)
		}
	}
}

func TestLoopRun_AggregatesAllProviderCounters(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("iteration " + strconv.Itoa(i))
		u[0].Contents = append(u[0].Contents, &message.UsageContent{Details: message.UsageDetails{
			InputTokenCount: 100, CachedInputTokenCount: 3, ReasoningTokenCount: 11, AdditionalCounts: map[string]int64{"cost": 2},
		}})
		return u
	})
	resp := collectLoopPort(t, c, loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool { return ctx.Iteration < 3 })}}, "go")
	usage := resp.Usage()
	if c.callCount != 3 || usage.InputTokenCount != 300 || usage.CachedInputTokenCount != 9 || usage.ReasoningTokenCount != 33 || usage.AdditionalCounts["cost"] != 6 {
		t.Fatalf("calls=%d, usage=%+v", c.callCount, usage)
	}
}

func TestLoopStreaming_UsageFromEveryIteration(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		return append(textUpdates("chunk "+strconv.Itoa(i)), &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.UsageContent{Details: loopUsageForCall(i)}}})
	})
	var response agent.Response
	cfg := loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool { return ctx.Iteration < 3 })}}
	for update, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("go")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		response.Update(update)
	}
	if c.callCount != 3 {
		t.Fatalf("calls=%d", c.callCount)
	}
	assertLoopUsage(t, &response, 42, 15, 57)
}

func TestLoopStreaming_StreamsEveryIteration(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("chunk " + strconv.Itoa(i))
	})
	var texts []string
	cfg := loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool { return ctx.Iteration < 3 })}}
	for update, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("go")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, update.Contents.Text())
	}
	if c.callCount != 3 || !slices.Equal(texts, []string{"chunk 1", "chunk 2", "chunk 3"}) {
		t.Fatalf("calls=%d, texts=%q", c.callCount, texts)
	}
}

func TestLoopStreaming_GlobalCap(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("chunk " + strconv.Itoa(i))
	})
	cfg := loop.Config{MaxIterations: new(4), Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return true })}}
	for _, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("go")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.callCount != 4 {
		t.Fatalf("calls=%d, want 4", c.callCount)
	}
}

func TestLoopStreaming_ContinueWithoutFeedback(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	cfg := loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool { return ctx.Iteration < 2 })}}
	for _, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("original")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.callCount != 2 || !slices.Equal(messageTexts(c.messagesPerCall[0]), []string{"original"}) || len(c.messagesPerCall[1]) != 0 {
		t.Fatalf("calls=%d, inputs=%v", c.callCount, c.messagesPerCall)
	}
}

func TestLoopStreaming_PendingApprovalStops(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return []*agent.ResponseUpdate{{Role: message.RoleAssistant, Contents: message.Contents{&message.ToolApprovalRequestContent{RequestID: "req1", ToolCall: &message.FunctionCallContent{CallID: "call1", Name: "MyTool"}}}}}
	})
	cfg := loop.Config{Evaluators: []loop.Evaluator{loopWhile(func(*loop.Context) bool { return true })}}
	for _, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("go")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.callCount != 1 {
		t.Fatalf("calls=%d, want 1", c.callCount)
	}
}

func TestLoopStreaming_ExcludedFeedbackStillSent(t *testing.T) {
	c := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate { return textUpdates("ack") })
	cfg := loop.Config{ExcludeOnBehalfOfMessages: true, Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			return loop.Continue("fix it"), nil
		}
		return loop.Stop(), nil
	})}}
	var texts []string
	for update, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("original")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, update.Contents.Text())
	}
	if !slices.Equal(texts, []string{"ack", "ack"}) || !slices.Equal(messageTexts(c.messagesPerCall[1]), []string{"fix it"}) {
		t.Fatalf("texts=%q, inputs=%v", texts, c.messagesPerCall)
	}
}

func TestLoopRun_LastResponseOnlyStillAggregatesUsage(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("iteration " + strconv.Itoa(i))
		u[0].Contents = append(u[0].Contents, &message.UsageContent{Details: loopUsageForCall(i)})
		return u
	})
	response := collectLoopPort(t, c, loop.Config{
		NonStreamingReturnsLastResponseOnly: true,
		Evaluators: []loop.Evaluator{loopWhile(func(ctx *loop.Context) bool {
			return ctx.Iteration < 3
		})},
	}, "go")
	if c.callCount != 3 || len(response.Messages) != 1 || response.String() != "iteration 3" {
		t.Fatalf("calls=%d, messages=%v", c.callCount, response.Messages)
	}
	assertLoopUsage(t, response, 42, 15, 57)
}

func TestLoopStreaming_SurfacesFeedbackBeforeReinvocationWithIDs(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("ack")
		u[0].ResponseID, u[0].AgentID = "resp-"+strconv.Itoa(i), "agent-"+strconv.Itoa(i)
		return u
	})
	cfg := loop.Config{OnBehalfOfAuthorName: "loop", Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			return loop.Continue("fix it"), nil
		}
		return loop.Stop(), nil
	})}}
	var updates []*agent.ResponseUpdate
	for update, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("original")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}
	if len(updates) != 3 || updates[0].Contents.Text() != "ack" || updates[1].Contents.Text() != "fix it" || updates[2].Contents.Text() != "ack" {
		t.Fatalf("updates=%v", updates)
	}
	feedback := updates[1]
	if feedback.Role != message.RoleUser || feedback.AuthorName != "loop" || feedback.ResponseID != "resp-2" || feedback.AgentID != "" || feedback.MessageID == "" {
		t.Fatalf("feedback update=%+v", feedback)
	}
}

func TestLoopStreaming_ExplicitMessagesGenerateMessageIDAndInheritResponseID(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		u := textUpdates("ack")
		u[0].ResponseID, u[0].AgentID = "resp-"+strconv.Itoa(i), "agent-"+strconv.Itoa(i)
		return u
	})
	cfg := loop.Config{Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			msg := message.NewText("explicit")
			msg.AuthorName = "evaluator"
			return loop.ContinueWithMessages([]*message.Message{msg}), nil
		}
		return loop.Stop(), nil
	})}}
	var updates []*agent.ResponseUpdate
	for update, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("original")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}
	if len(updates) != 3 || updates[0].Contents.Text() != "ack" || updates[1].Contents.Text() != "explicit" || updates[2].Contents.Text() != "ack" {
		t.Fatalf("updates=%v", updates)
	}
	feedback := updates[1]
	if feedback.AuthorName != "evaluator" || feedback.MessageID == "" || feedback.ResponseID != "resp-2" || feedback.AgentID != "" {
		t.Fatalf("explicit update=%+v", feedback)
	}
}

func TestLoopStreaming_EmptyInnerIterationGeneratesFeedbackResponseID(t *testing.T) {
	c := newCaptureAgent(func(i int, _ []*message.Message) []*agent.ResponseUpdate {
		if i < 2 {
			return textUpdates("ack")
		}
		return nil
	})
	cfg := loop.Config{OnBehalfOfAuthorName: "loop", Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
		if ctx.Iteration < 2 {
			return loop.Continue("fix it"), nil
		}
		return loop.Stop(), nil
	})}}
	var updates []*agent.ResponseUpdate
	for update, err := range loop.New(cfg).Run(c.provider().Run, t.Context(), []*message.Message{message.NewText("original")}, agent.WithSession(&agent.Session{}), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}
	if len(updates) != 2 || updates[0].Contents.Text() != "ack" || updates[1].Contents.Text() != "fix it" {
		t.Fatalf("updates=%v", updates)
	}
	feedback := updates[1]
	if feedback.AuthorName != "loop" || feedback.ResponseID == "" || feedback.AgentID != "" || feedback.MessageID == "" {
		t.Fatalf("feedback update=%+v", feedback)
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

func TestLoop_FreshContextPerIteration_ContinueWithMessagesOverridesVerbatim(t *testing.T) {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates("ack")
	})
	a := agent.New(capture.provider(), agent.Config{
		Middlewares: []agent.Middleware{loop.New(loop.Config{
			FreshContextPerIteration: true,
			Evaluators: []loop.Evaluator{loop.EvaluatorFunc(func(_ context.Context, ctx *loop.Context) (loop.Evaluation, error) {
				if ctx.Iteration == 1 {
					return loop.ContinueWithMessages([]*message.Message{message.NewText("explicit")}), nil
				}
				return loop.Stop(), nil
			})},
		})},
	})

	if _, err := a.RunText(context.Background(), "original").Collect(); err != nil {
		t.Fatal(err)
	}
	secondCall := messageTexts(capture.messagesPerCall[1])
	// Explicit ContinueWithMessages is a verbatim override even in fresh mode:
	// the original input must not be re-seeded onto the explicit messages. Fresh
	// mode still resets the session and governs the default-feedback path only.
	if !slices.Equal(secondCall, []string{"explicit"}) {
		t.Fatalf("second call = %v, want exactly [explicit] (verbatim override, no original)", secondCall)
	}
}
