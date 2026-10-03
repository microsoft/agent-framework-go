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

// judgeReturning builds an agent whose every run returns the given fixed text,
// for use as an AIJudgeEvaluator judge.
func judgeReturning(text string) *agent.Agent {
	capture := newCaptureAgent(func(int, []*message.Message) []*agent.ResponseUpdate {
		return textUpdates(text)
	})
	return agent.New(capture.provider(), agent.Config{})
}

func TestAIJudgeEvaluator_StructuredVerdict(t *testing.T) {
	// Answered -> stop.
	stop, err := loop.NewAIJudgeEvaluator(judgeReturning(`{"answered":true}`), loop.AIJudgeConfig{}).
		Evaluate(context.Background(), contextWithResponse("done"))
	if err != nil {
		t.Fatal(err)
	}
	if stop.ShouldReinvoke {
		t.Fatal("answered verdict should stop the loop")
	}

	// Not answered with gap analysis -> continue with the gap in the feedback.
	cont, err := loop.NewAIJudgeEvaluator(judgeReturning(`Here is my verdict: {"answered":false,"gapAnalysis":"missing the summary"}`), loop.AIJudgeConfig{}).
		Evaluate(context.Background(), contextWithResponse("partial"))
	if err != nil {
		t.Fatal(err)
	}
	if !cont.ShouldReinvoke {
		t.Fatal("unanswered verdict should continue the loop")
	}
	if !strings.Contains(cont.Feedback, "missing the summary") || strings.Contains(cont.Feedback, "{gap_analysis}") {
		t.Fatalf("feedback = %q, want the gap analysis substituted", cont.Feedback)
	}
}

func TestAIJudgeEvaluator_TextMarkerFallback(t *testing.T) {
	// DONE marker (no structured JSON) -> stop.
	stop, err := loop.NewAIJudgeEvaluator(judgeReturning("The task looks complete. "+loop.AIJudgeDoneMarker), loop.AIJudgeConfig{}).
		Evaluate(context.Background(), contextWithResponse("x"))
	if err != nil {
		t.Fatal(err)
	}
	if stop.ShouldReinvoke {
		t.Fatal("DONE marker should stop the loop")
	}

	// MORE marker -> continue; and ambiguous (both markers) -> MORE wins.
	for _, text := range []string{
		"Not there yet. " + loop.AIJudgeMoreMarker,
		loop.AIJudgeDoneMarker + " but also " + loop.AIJudgeMoreMarker,
		"no verdict at all",
	} {
		cont, err := loop.NewAIJudgeEvaluator(judgeReturning(text), loop.AIJudgeConfig{}).
			Evaluate(context.Background(), contextWithResponse("x"))
		if err != nil {
			t.Fatal(err)
		}
		if !cont.ShouldReinvoke {
			t.Fatalf("text %q should continue the loop (MORE wins when ambiguous/absent)", text)
		}
	}
}

func TestAIJudgeEvaluator_CriteriaRenderedIntoInstructions(t *testing.T) {
	// Capture the system instructions the judge receives on its run.
	var gotInstructions string
	judge := agent.New(agent.ProviderConfig{
		Run: func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			gotInstructions, _ = agent.GetOption(opts, agent.WithInstructions)
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(textUpdates(`{"answered":true}`)[0], nil)
			}
		},
	}, agent.Config{})

	_, err := loop.NewAIJudgeEvaluator(judge, loop.AIJudgeConfig{Criteria: []string{"cite sources", "  ", "use markdown"}}).
		Evaluate(context.Background(), contextWithResponse("x"))
	if err != nil {
		t.Fatal(err)
	}
	// Blank criteria are skipped; the non-blank ones are rendered as a bullet list.
	if !strings.Contains(gotInstructions, "The response must satisfy all of the following criteria:") ||
		!strings.Contains(gotInstructions, "\n- cite sources") ||
		!strings.Contains(gotInstructions, "\n- use markdown") ||
		strings.Contains(gotInstructions, "{criteria}") {
		t.Fatalf("judge instructions = %q, want rendered criteria and no placeholder", gotInstructions)
	}
}

func TestNewAIJudgeEvaluator_PanicsWithNilJudge(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	loop.NewAIJudgeEvaluator(nil, loop.AIJudgeConfig{})
}
