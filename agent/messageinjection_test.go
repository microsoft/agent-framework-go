// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestMessageInjection_OnlyEnabledWhenConfigured(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%t/stream=%t", enabled, stream), func(t *testing.T) {
				injection := &agent.MessageInjector{}
				session := &agent.Session{}
				if err := injection.EnqueueMessages(session, message.NewText("queued")); err != nil {
					t.Fatal(err)
				}
				var captured []*message.Message
				runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					captured = slices.Clone(messages)
				}).AddText("done").Build()}
				var configured *agent.MessageInjector
				if enabled {
					configured = injection
				}
				a := newMessageInjectingAgent(runner.Run, toolautocall.Config{}, nil, configured)
				if _, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(stream)).Collect(); err != nil {
					t.Fatal(err)
				}

				wantMessages := []string{"original"}
				wantPending := []string{"queued"}
				if enabled {
					wantMessages = append(wantMessages, "queued")
					wantPending = nil
				}
				if got := messageTexts(captured); !slices.Equal(got, wantMessages) {
					t.Errorf("provider messages = %v, want %v", got, wantMessages)
				}
				pending, err := injection.PendingMessages(session)
				if err != nil {
					t.Fatal(err)
				}
				if got := messageTexts(pending); !slices.Equal(got, wantPending) {
					t.Errorf("pending messages = %v, want %v", got, wantPending)
				}
			})
		}
	}
}

func TestMessageInjection_IncludesMessagesQueuedBeforeRun(t *testing.T) {
	var captured []*message.Message
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
		captured = slices.Clone(messages)
	}).AddText("response").Build()}
	injection := &agent.MessageInjector{}
	a := agent.New(agent.ProviderConfig{
		Run:         runner.Run,
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{
		MessageInjector:                         injection,
		HistoryProvider:                         agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "test"}),
		RequirePerServiceCallHistoryPersistence: true,
	})
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := injection.EnqueueMessages(session, message.NewText("injected message")); err != nil {
		t.Fatal(err)
	}

	if _, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(false)).Collect(); err != nil {
		t.Fatal(err)
	}
	if got := messageTexts(captured); !slices.Equal(got, []string{"original", "injected message"}) {
		t.Fatalf("provider messages = %v, want original and injected messages", got)
	}
}

func TestMessageInjection_UpdatesConversationIDBetweenProviderCalls(t *testing.T) {
	for _, tc := range []struct {
		name             string
		initialSessionID string
		initialOptionID  string
		responseIDs      []string
		wantIDs          []string
		streams          []bool
	}{
		{name: "new conversation", responseIDs: []string{"conv-1", "conv-2", "conv-3"}, wantIDs: []string{"", "conv-1", "conv-2"}},
		{name: "session advances", initialSessionID: "conv-0", responseIDs: []string{"conv-1", "conv-2", "conv-3"}, wantIDs: []string{"conv-0", "conv-1", "conv-2"}},
		{name: "run option advances", initialOptionID: "conv-0", responseIDs: []string{"conv-1", "conv-2", "conv-3"}, wantIDs: []string{"conv-0", "conv-1", "conv-2"}},
		{name: "run option cleared", initialOptionID: "conv-0", responseIDs: []string{"", "conv-2", "conv-3"}, wantIDs: []string{"conv-0", "", "conv-2"}},
		{name: "stored becomes stateless", responseIDs: []string{"conv-1", "", "conv-3"}, wantIDs: []string{"", "conv-1", ""}},
		{name: "stateless", responseIDs: []string{"", "", ""}, wantIDs: []string{"", "", ""}},
		{name: "two calls with terminal ID absent", responseIDs: []string{"conv-123", ""}, wantIDs: []string{"", "conv-123"}, streams: []bool{false}},
	} {
		streams := tc.streams
		if streams == nil {
			streams = []bool{false, true}
		}
		for _, stream := range streams {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				injection := &agent.MessageInjector{}
				session := &agent.Session{}
				session.SetServiceID(tc.initialSessionID)
				var calls int
				var capturedIDs []string
				run := func(_ context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						if calls >= len(tc.wantIDs) {
							t.Fatal("unexpected extra provider call")
						}
						id, _ := agent.GetOption(opts, agent.WithServiceID)
						capturedIDs = append(capturedIDs, id)
						if id != tc.wantIDs[calls] {
							t.Errorf("provider call %d: conversation ID = %q, want %q", calls+1, id, tc.wantIDs[calls])
						}
						wantText := "original"
						if calls > 0 {
							wantText = fmt.Sprintf("injected-%d", calls)
						}
						if got := messageTexts(messages); !slices.Equal(got, []string{wantText}) {
							t.Errorf("provider call %d: messages = %v, want [%s]", calls+1, got, wantText)
						}
						if got := session.ServiceID(); got != tc.initialSessionID {
							t.Errorf("session ID changed during the run: %q, want %q", got, tc.initialSessionID)
						}
						responseID := tc.responseIDs[calls]
						calls++
						if calls < len(tc.wantIDs) {
							if err := injection.EnqueueMessages(session, message.NewText(fmt.Sprintf("injected-%d", calls))); err != nil {
								t.Fatal(err)
							}
						}
						if responseID != "" {
							if !yield(&agent.ResponseUpdate{ConversationID: new(responseID)}, nil) {
								return
							}
						}
						// A later update without an ID must not clear this call's ID.
						yield(&agent.ResponseUpdate{
							MessageID: fmt.Sprintf("message-%d", calls), Role: message.RoleAssistant,
							Contents: message.Contents{&message.TextContent{Text: "reply"}},
						}, nil)
					}
				}
				a := newMessageInjectingAgent(run, toolautocall.Config{}, nil, injection)
				opts := []agent.Option{agent.WithSession(session), agent.Stream(stream)}
				if tc.initialOptionID != "" {
					opts = append(opts, agent.WithServiceID(tc.initialOptionID))
				}
				response, err := a.RunText(t.Context(), "original", opts...).Collect()
				if err != nil {
					t.Fatal(err)
				}
				if calls != len(tc.wantIDs) {
					t.Errorf("provider calls = %d, want %d", calls, len(tc.wantIDs))
				}
				if !slices.Equal(capturedIDs, tc.wantIDs) {
					t.Errorf("provider conversation IDs = %q, want %q", capturedIDs, tc.wantIDs)
				}
				wantFinalID := tc.responseIDs[len(tc.responseIDs)-1]
				if got := session.ServiceID(); got != wantFinalID {
					t.Errorf("session ID = %q, want %q", got, wantFinalID)
				}
				if wantFinalID == "" {
					if response.ConversationID != nil {
						t.Errorf("response conversation ID = %q, want none", *response.ConversationID)
					}
				} else if response.ConversationID == nil || *response.ConversationID != wantFinalID {
					t.Errorf("response conversation ID = %v, want %q", response.ConversationID, wantFinalID)
				}
				if id, _ := agent.GetOption(opts, agent.WithServiceID); id != tc.initialOptionID {
					t.Errorf("caller option changed to %q, want %q", id, tc.initialOptionID)
				}
			})
		}
	}
}

func TestMessageInjection_DeliversMessageQueuedBeforeRunOnce(t *testing.T) {
	var captured []*message.Message
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
		captured = slices.Clone(messages)
	}).AddText("done").Build()}
	injection := &agent.MessageInjector{}
	injectingAgent := newMessageInjectingAgent(runner.Run, toolautocall.Config{}, nil, injection)
	session := &agent.Session{}
	if err := injection.EnqueueMessages(session, message.NewText("queued before run")); err != nil {
		t.Fatal(err)
	}

	if _, err := injectingAgent.RunText(t.Context(), "original", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if got := messageTexts(captured); !slices.Equal(got, []string{"original", "queued before run"}) {
		t.Fatalf("provider messages = %v", got)
	}
	pending, err := injection.PendingMessages(session)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending messages = %d, want 0", len(pending))
	}
}

func TestMessageInjection_DeliversQueuedMessageAfterSessionRoundTrip(t *testing.T) {
	var captured []*message.Message
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
		captured = slices.Clone(messages)
	}).AddText("done").Build()}
	injection := &agent.MessageInjector{}
	injectingAgent := newMessageInjectingAgent(runner.Run, toolautocall.Config{}, nil, injection)
	session := &agent.Session{}
	if err := injection.EnqueueMessages(session, message.NewText("queued before serialization")); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	if _, err := injectingAgent.RunText(t.Context(), "original", agent.WithSession(&restored)).Collect(); err != nil {
		t.Fatal(err)
	}
	if got := messageTexts(captured); !slices.Equal(got, []string{"original", "queued before serialization"}) {
		t.Fatalf("provider messages = %v", got)
	}
}

func TestMessageInjection_LoopsWhenNoActionableFunctionCallsAndPendingMessages(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			injection := &agent.MessageInjector{}
			session := &agent.Session{}
			var calls int
			run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					calls++
					switch calls {
					case 1:
						if err := injection.EnqueueMessages(session, message.NewText("injected during first call")); err != nil {
							t.Fatal(err)
						}
					case 2:
						if !slices.Contains(messageTexts(messages), "injected during first call") {
							t.Fatalf("follow-up request lost injected message: %v", messageTexts(messages))
						}
					default:
						t.Fatal("unexpected extra provider call")
					}
					yield(&agent.ResponseUpdate{
						MessageID: fmt.Sprintf("message-%d", calls), Role: message.RoleAssistant,
						Contents: message.Contents{&message.TextContent{Text: fmt.Sprintf("response %d", calls)}},
					}, nil)
				}
			}
			a := agent.New(agent.ProviderConfig{
				Run:         run,
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				MessageInjector:                         injection,
				HistoryProvider:                         agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "test"}),
				RequirePerServiceCallHistoryPersistence: true,
			})
			response, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			wantText := "response 2"
			if stream {
				wantText = "response 1\nresponse 2"
			}
			if calls != 2 || response.String() != wantText {
				t.Fatalf("provider calls=%d response=%q, want 2/%q", calls, response.String(), wantText)
			}
		})
	}
}

func TestMessageInjection_DoesNotLoopWhenActionableFunctionCallsPresent(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			injection := &agent.MessageInjector{}
			session := &agent.Session{}
			var events []string
			var calls int
			run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					calls++
					events = append(events, fmt.Sprintf("provider-%d", calls))
					switch calls {
					case 1:
						if err := injection.EnqueueMessages(session, message.NewText("injected")); err != nil {
							t.Fatal(err)
						}
						yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
							&message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: `{}`},
						}}, nil)
					case 2:
						if !slices.Contains(messageTexts(messages), "injected") {
							t.Fatalf("follow-up request lost injected message: %v", messageTexts(messages))
						}
						yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
							&message.TextContent{Text: "final"},
						}}, nil)
					default:
						t.Fatal("unexpected extra provider call")
					}
				}
			}
			fn := functool.MustNew(functool.Config{Name: "myTool"}, func(context.Context, struct{}) (string, error) {
				events = append(events, "tool")
				return "tool result", nil
			})
			a := agent.New(agent.ProviderConfig{
				Run:         run,
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				MessageInjector:                         injection,
				HistoryProvider:                         agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "test"}),
				RequirePerServiceCallHistoryPersistence: true,
				Tools:                                   []tool.Tool{fn},
			})
			response, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"provider-1", "tool", "provider-2"}; !slices.Equal(events, want) {
				t.Errorf("execution order = %v, want %v", events, want)
			}
			if calls != 2 || response.String() != "final" {
				t.Fatalf("provider calls=%d response=%q, want 2/final", calls, response.String())
			}
		})
	}
}

func TestMessageInjection_ActionableFunctionCallOnLaterIterationAggregatesUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			injection := &agent.MessageInjector{}
			session := &agent.Session{}
			var events []string
			var calls int
			run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					calls++
					events = append(events, fmt.Sprintf("provider-%d", calls))
					var content message.Content
					switch calls {
					case 1:
						if err := injection.EnqueueMessages(session, message.NewText("injected")); err != nil {
							t.Fatal(err)
						}
						content = &message.TextContent{Text: "queued"}
					case 2:
						content = &message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: `{}`}
					case 3:
						content = &message.TextContent{Text: "final"}
					default:
						t.Fatal("unexpected extra provider call")
					}
					yield(&agent.ResponseUpdate{
						MessageID: fmt.Sprintf("message-%d", calls), Role: message.RoleAssistant,
						Contents: message.Contents{content, &message.UsageContent{Details: messageInjectionUsageForCall(calls)}},
					}, nil)
				}
			}
			fn := functool.MustNew(functool.Config{Name: "myTool"}, func(context.Context, struct{}) (string, error) {
				events = append(events, "tool")
				return "tool result", nil
			})
			a := agent.New(agent.ProviderConfig{
				Run:         run,
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				MessageInjector:                         injection,
				RequirePerServiceCallHistoryPersistence: true,
				Tools:                                   []tool.Tool{fn},
			})
			response, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"provider-1", "provider-2", "tool", "provider-3"}; !slices.Equal(events, want) {
				t.Errorf("execution order = %v, want %v", events, want)
			}
			wantText := "final"
			if stream {
				wantText = "queued\nfinal"
			}
			if calls != 3 || response.String() != wantText {
				t.Errorf("provider calls=%d response=%q, want 3/%q", calls, response.String(), wantText)
			}
			if got := response.Usage(); got.InputTokenCount != 42 || got.OutputTokenCount != 15 || got.TotalTokenCount != 57 {
				t.Errorf("usage = %+v, want 42 input, 15 output, 57 total tokens", got)
			}
		})
	}
}

func TestMessageInjection_LoopsWhenOnlyInformationalFunctionCallsAndPendingMessages(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			injection := &agent.MessageInjector{}
			session := &agent.Session{}
			var calls int
			run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					calls++
					switch calls {
					case 1:
						if err := injection.EnqueueMessages(session, message.NewText("injected")); err != nil {
							t.Fatal(err)
						}
						yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
							&message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: `{}`, InformationalOnly: true},
						}}, nil)
					case 2:
						if !slices.Contains(messageTexts(messages), "injected") {
							t.Fatalf("follow-up request lost injected message: %v", messageTexts(messages))
						}
						yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
							&message.TextContent{Text: "final"},
						}}, nil)
					default:
						t.Fatal("unexpected extra provider call")
					}
				}
			}
			a := agent.New(agent.ProviderConfig{
				Run:         run,
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				MessageInjector:                         injection,
				HistoryProvider:                         agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "test"}),
				RequirePerServiceCallHistoryPersistence: true,
			})
			response, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || response.String() != "final" {
				t.Fatalf("provider calls=%d response=%q, want 2/final", calls, response.String())
			}
		})
	}
}

func TestMessageInjection_DeliversPendingMessageAfterRunAndSessionRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			injection := &agent.MessageInjector{}
			session := &agent.Session{}
			firstRun := true
			var captured []*message.Message
			run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					if firstRun {
						if err := injection.EnqueueMessages(session, message.NewText("injected before serialization")); err != nil {
							t.Fatal(err)
						}
						// The tool loop reaches its limit, leaving the last injected message pending.
						yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
							&message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: `{}`},
						}}, nil)
						return
					}
					captured = slices.Clone(messages)
					yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
						&message.TextContent{Text: "final response"},
					}}, nil)
				}
			}
			fn := functool.MustNew(functool.Config{Name: "myTool"}, func(context.Context, struct{}) (string, error) {
				return "tool result", nil
			})
			a := agent.New(agent.ProviderConfig{
				Run:         run,
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				MessageInjector:                         injection,
				HistoryProvider:                         agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "test"}),
				RequirePerServiceCallHistoryPersistence: true,
				Tools:                                   []tool.Tool{fn},
			})
			if _, err := a.RunText(t.Context(), "first run message", agent.WithSession(session), agent.Stream(stream)).Collect(); err != nil {
				t.Fatal(err)
			}
			pending, err := injection.PendingMessages(session)
			if err != nil {
				t.Fatal(err)
			}
			if got := messageTexts(pending); !slices.Equal(got, []string{"injected before serialization"}) {
				t.Fatalf("pending messages before serialization = %v", got)
			}

			data, err := json.Marshal(session)
			if err != nil {
				t.Fatal(err)
			}
			var restored agent.Session
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			firstRun = false
			response, err := a.RunText(t.Context(), "second run message", agent.WithSession(&restored), agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			if got := messageTexts(captured); !slices.Equal(got, []string{"second run message", "injected before serialization"}) {
				t.Errorf("restored provider messages = %v", got)
			}
			if response.String() != "final response" {
				t.Errorf("response = %q, want final response", response.String())
			}
			pending, err = injection.PendingMessages(&restored)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				t.Errorf("pending messages after restored run = %d, want 0", len(pending))
			}
		})
	}
}

func TestMessageInjection_AggregatesUsageAcrossIterations(t *testing.T) {
	testMessageInjectionUsageAcrossIterations(t, false)
}

func TestMessageInjection_StreamingSurfacesUsageFromEveryIteration(t *testing.T) {
	testMessageInjectionUsageAcrossIterations(t, true)
}

func testMessageInjectionUsageAcrossIterations(t *testing.T, stream bool) {
	t.Helper()
	injection := &agent.MessageInjector{}
	session := &agent.Session{}
	var calls int
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			calls++
			if calls > 3 {
				t.Fatal("unexpected extra provider call")
			}
			if calls < 3 {
				if err := injection.EnqueueMessages(session, message.NewText(fmt.Sprintf("injected %d", calls))); err != nil {
					t.Fatal(err)
				}
			}
			if !yield(&agent.ResponseUpdate{
				MessageID: fmt.Sprintf("message-%d", calls), Role: message.RoleAssistant,
				Contents: message.Contents{&message.TextContent{Text: fmt.Sprintf("response %d", calls)}},
			}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{
				MessageID: fmt.Sprintf("message-%d", calls), Role: message.RoleAssistant,
				Contents: message.Contents{&message.UsageContent{Details: messageInjectionUsageForCall(calls)}},
			}, nil)
		}
	}
	a := newMessageInjectingAgent(run, toolautocall.Config{}, nil, injection)
	var response agent.Response
	var streamedUsage [][3]int64
	for update, err := range a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(stream)) {
		if err != nil {
			t.Fatal(err)
		}
		response.Update(update)
		if stream && update != nil {
			for _, content := range update.Contents {
				if usage, ok := content.(*message.UsageContent); ok {
					streamedUsage = append(streamedUsage, [3]int64{usage.Details.InputTokenCount, usage.Details.OutputTokenCount, usage.Details.TotalTokenCount})
				}
			}
		}
	}
	if calls != 3 {
		t.Errorf("provider calls = %d, want 3", calls)
	}
	if got := response.Usage(); got.InputTokenCount != 42 || got.OutputTokenCount != 15 || got.TotalTokenCount != 57 {
		t.Errorf("usage = %+v, want 42 input, 15 output, 57 total tokens", got)
	}
	wantText := "response 3"
	if stream {
		wantText = "response 1\nresponse 2\nresponse 3"
		if want := [][3]int64{{2, 3, 5}, {11, 5, 16}, {29, 7, 36}}; !slices.Equal(streamedUsage, want) {
			t.Errorf("streamed usage = %v, want %v", streamedUsage, want)
		}
	}
	if response.String() != wantText {
		t.Errorf("response = %q, want %q", response.String(), wantText)
	}
}

func TestMessageInjection_PreservesProviderResponseWhileAggregatingUsage(t *testing.T) {
	type providerResponse struct {
		state string
		usage message.UsageDetails
	}
	injection := &agent.MessageInjector{}
	session := &agent.Session{}
	var serviceResponses []*providerResponse
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			call := len(serviceResponses) + 1
			if call > 3 {
				t.Fatal("unexpected extra provider call")
			}
			if call < 3 {
				if err := injection.EnqueueMessages(session, message.NewText(fmt.Sprintf("injected %d", call))); err != nil {
					t.Fatal(err)
				}
			}
			raw := &providerResponse{
				state: fmt.Sprintf("call %d", call),
				usage: message.UsageDetails{InputTokenCount: int64(call), OutputTokenCount: int64(call * 10)},
			}
			serviceResponses = append(serviceResponses, raw)
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, RawRepresentation: raw, Contents: message.Contents{
				&message.TextContent{Text: fmt.Sprintf("response %d", call)},
				&message.UsageContent{Details: raw.usage},
			}}, nil)
		}
	}
	a := newMessageInjectingAgent(run, toolautocall.Config{}, nil, injection)
	response, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(serviceResponses) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(serviceResponses))
	}
	raw, ok := response.RawRepresentation.(*providerResponse)
	if !ok {
		t.Fatalf("raw response type = %T, want *providerResponse", response.RawRepresentation)
	}
	if raw != serviceResponses[2] || raw.state != "call 3" {
		t.Errorf("raw response = %+v, want the original final provider response", raw)
	}
	if got := response.Usage(); got.InputTokenCount != 6 || got.OutputTokenCount != 60 {
		t.Errorf("usage = %+v, want 6 input and 60 output tokens", got)
	}
	// Raw provider objects are preserved, not rewritten to carry aggregate usage.
	for i, serviceResponse := range serviceResponses {
		if serviceResponse.usage.InputTokenCount != int64(i+1) || serviceResponse.usage.OutputTokenCount != int64((i+1)*10) {
			t.Errorf("provider response %d usage was mutated: %+v", i+1, serviceResponse.usage)
		}
	}
}

func TestMessageInjection_SingleServiceCallPreservesProviderUsage(t *testing.T) {
	serviceUsage := &message.UsageContent{Details: message.UsageDetails{
		InputTokenCount: 12, OutputTokenCount: 3, TotalTokenCount: 15,
		CachedInputTokenCount: 2, ReasoningTokenCount: 1, AdditionalCounts: map[string]int64{"custom": 4},
	}}
	var calls int
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(context.Context, []*message.Message, ...agent.Option) {
		calls++
	}).Add(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
		&message.TextContent{Text: "done"}, serviceUsage,
	}}).Build()}
	a := newMessageInjectingAgent(runner.Run, toolautocall.Config{}, nil, &agent.MessageInjector{})
	response, err := a.RunText(t.Context(), "original", agent.WithSession(&agent.Session{}), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || response.String() != "done" {
		t.Fatalf("provider calls=%d response=%q, want 1/done", calls, response.String())
	}
	got := response.Usage()
	if got.InputTokenCount != 12 || got.OutputTokenCount != 3 || got.TotalTokenCount != 15 ||
		got.CachedInputTokenCount != 2 || got.ReasoningTokenCount != 1 || !maps.Equal(got.AdditionalCounts, map[string]int64{"custom": 4}) {
		t.Fatalf("usage = %+v, want the original provider usage", got)
	}
	got.AdditionalCounts["custom"] = 100
	if got := serviceUsage.Details; got.InputTokenCount != 12 || got.OutputTokenCount != 3 || got.TotalTokenCount != 15 ||
		got.CachedInputTokenCount != 2 || got.ReasoningTokenCount != 1 || !maps.Equal(got.AdditionalCounts, map[string]int64{"custom": 4}) {
		t.Errorf("provider usage was mutated: %+v", got)
	}
}

func messageInjectionUsageForCall(call int) message.UsageDetails {
	return [...]message.UsageDetails{
		{InputTokenCount: 2, OutputTokenCount: 3, TotalTokenCount: 5},
		{InputTokenCount: 11, OutputTokenCount: 5, TotalTokenCount: 16},
		{InputTokenCount: 29, OutputTokenCount: 7, TotalTokenCount: 36},
	}[call-1]
}

func TestMessageInjection_ConcurrentEnqueuesDoNotLoseMessages(t *testing.T) {
	injection := &agent.MessageInjector{}
	session := &agent.Session{}
	const goroutineCount = 16
	const messagesPerGoroutine = 100

	start := make(chan struct{})
	var ready, waitGroup sync.WaitGroup
	ready.Add(goroutineCount)
	waitGroup.Add(goroutineCount)
	for goroutineIndex := range goroutineCount {
		go func() {
			defer waitGroup.Done()
			ready.Done()
			<-start
			for messageIndex := range messagesPerGoroutine {
				if err := injection.EnqueueMessages(session, message.NewText(fmt.Sprintf("%d-%d", goroutineIndex, messageIndex))); err != nil {
					t.Errorf("EnqueueMessages error: %v", err)
					return
				}
			}
		}()
	}
	ready.Wait()
	close(start)
	waitGroup.Wait()

	pending, err := injection.PendingMessages(session)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(pending), goroutineCount*messagesPerGoroutine; got != want {
		t.Fatalf("pending messages = %d, want %d", got, want)
	}
	counts := make(map[string]int, len(pending))
	for _, msg := range pending {
		if msg == nil || msg.Role != message.RoleUser {
			t.Fatalf("pending message = %#v, want a user message", msg)
		}
		counts[msg.String()]++
	}
	for goroutineIndex := range goroutineCount {
		for messageIndex := range messagesPerGoroutine {
			text := fmt.Sprintf("%d-%d", goroutineIndex, messageIndex)
			if got := counts[text]; got != 1 {
				t.Errorf("message %q occurred %d times, want 1", text, got)
			}
		}
	}
}

func newMessageInjectingAgent(run agent.RunFunc, config toolautocall.Config, tools []tool.Tool, injection *agent.MessageInjector) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		Run:         run,
		Middlewares: []agent.Middleware{toolautocall.New(config)},
	}, agent.Config{
		MessageInjector: injection,
		Tools:           tools,
	})
}

func messageTexts(messages []*message.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg != nil {
			texts = append(texts, msg.String())
		}
	}
	return texts
}
