// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

type stubTool struct {
	name string
}

func TestFunctionInvocationMiddleware_Composition(t *testing.T) {
	toolFailure := errors.New("tool failed")
	for _, tc := range []struct {
		name      string
		short     bool
		replace   bool
		toolError error
	}{
		{name: "arguments and result replacement"},
		{name: "function replacement", replace: true},
		{name: "error propagation", toolError: toolFailure},
		{name: "short circuit", short: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			fn := functool.MustNew(functool.Config{Name: "lookup", Description: "Look up a value"}, func(_ context.Context, args struct {
				Value string `json:"value"`
			},
			) (string, error) {
				order = append(order, "tool")
				if tc.replace {
					t.Fatal("original tool invoked after middleware replaced it")
				}
				if args.Value != "changed" {
					t.Errorf("tool argument = %q, want changed", args.Value)
				}
				return args.Value, tc.toolError
			})
			replacement := functool.MustNew(functool.Config{Name: "lookup", Description: "Look up a value"}, func(_ context.Context, args struct {
				Value string `json:"value"`
			},
			) (string, error) {
				order = append(order, "replacement tool")
				if args.Value != "changed" {
					t.Errorf("replacement tool argument = %q, want changed", args.Value)
				}
				return "replacement " + args.Value, tc.toolError
			})
			first := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
				order = append(order, "first before")
				if invocation.Function != fn || invocation.Arguments != `{"value":"original"}` || invocation.CallID != "" {
					t.Errorf("unexpected direct invocation: %#v", invocation)
				}
				if tc.short {
					return "cached", nil
				}
				if tc.replace {
					invocation.Function = replacement
				}
				invocation.Arguments = `{"value":"changed"}`
				result, err := next(ctx, invocation)
				order = append(order, "first after")
				if tc.replace && invocation.Function != replacement {
					t.Errorf("function restored before middleware returned: got %v, want replacement", invocation.Function)
				}
				if err != nil {
					return nil, err
				}
				return "wrapped " + result.(string), nil
			})
			second := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
				order = append(order, "second before")
				if tc.replace {
					t.Error("later middleware invoked for replacement")
				}
				result, err := next(ctx, invocation)
				order = append(order, "second after")
				return result, err
			})
			other := stubTool{name: "hosted"}
			options := []agent.Option{agent.WithTool(fn), agent.WithTool(other), agent.WithTool(nil)}
			provider := agent.NewContextProvider(agent.ContextProviderConfig{SourceID: "tools", Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
				return nil, options, nil
			}})
			checkTools := func(_ context.Context, _ []*message.Message, opts ...agent.Option) {
				tools := slices.Collect(agent.AllOptions(opts, agent.WithTool))
				if len(tools) != 2 || tools[0] != fn || tools[1] != other {
					t.Fatalf("provider tools changed: %v", tools)
				}
			}
			var runner agenttest.Runner
			middlewares := []agent.FunctionInvocationMiddleware{first, nil, second}
			a := agent.New(agent.ProviderConfig{
				Run: runner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				ContextProviders:    []agent.ContextProvider{provider},
				FunctionMiddlewares: middlewares,
			})
			// Registration owns a copy; later changes to the caller's slice must not affect it.
			middlewares[0] = nil
			for range 2 {
				order = nil
				runner = agenttest.Runner{Responses: agenttest.NewResponseBuilder(checkTools).
					AddFunctionCall("", "lookup", `{"value":"original"}`).
					NewTurn(checkTools).AddText("done").Build()}
				var results []*message.FunctionResultContent
				for update, err := range a.RunText(t.Context(), "start") {
					if err != nil {
						t.Fatal(err)
					}
					for _, content := range update.Contents {
						if result, ok := content.(*message.FunctionResultContent); ok {
							results = append(results, result)
						}
					}
				}
				if len(results) != 1 || !errors.Is(results[0].Error, tc.toolError) {
					t.Fatalf("results = %v, want one result with error %v", results, tc.toolError)
				}
				wantResult := "wrapped changed"
				wantOrder := []string{"first before", "second before", "tool", "second after", "first after"}
				if tc.replace {
					wantResult = "wrapped replacement changed"
					wantOrder = []string{"first before", "replacement tool", "first after"}
				}
				if tc.short {
					wantResult = "cached"
					wantOrder = []string{"first before"}
				}
				if tc.toolError == nil && results[0].Result != wantResult {
					t.Errorf("result = %v, want %q", results[0].Result, wantResult)
				}
				if !slices.Equal(order, wantOrder) {
					t.Errorf("callback order = %v, want %v", order, wantOrder)
				}
			}
			if original, _ := agent.GetOption(options[:1], agent.WithTool); original != fn {
				t.Error("middleware mutated the context provider's options")
			}
		})
	}
}

func TestFunctionInvocationMiddleware_RejectsNilContinuationBeforeInnerMiddleware(t *testing.T) {
	for _, tc := range []struct {
		name       string
		nilInvoke  bool
		wantErrMsg string
	}{
		{name: "nil invocation", nilInvoke: true, wantErrMsg: "agent: function invocation middleware called next with nil invocation"},
		{name: "nil function", wantErrMsg: "agent: function invocation middleware called next with nil function"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
				t.Fatal("original tool invoked despite invalid continuation")
				return "", nil
			})
			innerCalled := false
			outer := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
				if tc.nilInvoke {
					return next(ctx, nil)
				}
				invocation.Function = nil
				return next(ctx, invocation)
			})
			inner := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
				innerCalled = true
				return next(ctx, invocation)
			})
			var runner agenttest.Runner
			a := agent.New(agent.ProviderConfig{
				Run: runner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				RunOptions:          []agent.Option{agent.WithTool(fn)},
				FunctionMiddlewares: []agent.FunctionInvocationMiddleware{outer, inner},
			})
			runner = agenttest.Runner{Responses: agenttest.NewResponseBuilder(nil).
				AddFunctionCall("", "lookup", `{}`).
				NewTurn(nil).AddText("done").Build()}
			var results []*message.FunctionResultContent
			for update, err := range a.RunText(t.Context(), "start") {
				if err != nil {
					t.Fatal(err)
				}
				for _, content := range update.Contents {
					if result, ok := content.(*message.FunctionResultContent); ok {
						results = append(results, result)
					}
				}
			}
			if innerCalled {
				t.Error("inner middleware was invoked despite invalid continuation from outer middleware")
			}
			if len(results) != 1 || results[0].Error == nil || results[0].Error.Error() != tc.wantErrMsg {
				t.Fatalf("results = %v, want one result with error %q", results, tc.wantErrMsg)
			}
		})
	}
}

func (t stubTool) Name() string {
	return t.name
}

func (t stubTool) Description() string {
	return t.name
}

type prependMiddleware struct {
	prependMessages []*message.Message
	instructions    string
	runCalls        int
	lastSession     *agent.Session
}

func (m *prependMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	m.runCalls++
	if session, ok := agent.GetOption(opts, agent.WithSession); ok {
		m.lastSession = session
	}
	msgForNext := make([]*message.Message, 0, len(m.prependMessages)+1+len(messages))
	msgForNext = append(msgForNext, m.prependMessages...)
	if m.instructions != "" {
		msgForNext = append(msgForNext, &message.Message{
			Role: message.RoleSystem,
			Contents: []message.Content{
				&message.TextContent{Text: m.instructions},
			},
		})
	}
	msgForNext = append(msgForNext, messages...)
	return next(ctx, msgForNext, opts...)
}

type errorMiddleware struct {
	err error
}

func (m *errorMiddleware) Run(_ agent.RunFunc, _ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(nil, m.err)
	}
}

type trackingMiddleware struct {
	runCalls int
	lastErr  error
}

func (m *trackingMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	m.runCalls++
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		for update, err := range next(ctx, messages, opts...) {
			if err != nil {
				m.lastErr = err
			}
			if !yield(update, err) {
				return
			}
		}
	}
}

func failRunFunc(runErr error) func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, runErr)
		}
	}
}

func messageStrings(messages []*message.Message) []string {
	strings := make([]string, 0, len(messages))
	for _, msg := range messages {
		strings = append(strings, msg.String())
	}
	return strings
}

func updateStrings(updates []*agent.ResponseUpdate) []string {
	strings := make([]string, 0, len(updates))
	for _, update := range updates {
		strings = append(strings, update.String())
	}
	return strings
}

func newGenericTestAgent(runFn func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error], middlewares []agent.Middleware, runOptions ...agent.Option) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		Run: runFn,
	}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		Middlewares: middlewares,
		RunOptions:  runOptions,
	})
}

func TestNew_IgnoresNilMiddleware(t *testing.T) {
	var providerCalls, agentMiddlewareCalls, providerMiddlewareCalls int
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		providerCalls++
		return func(func(*agent.ResponseUpdate, error) bool) {}
	}
	agentMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		agentMiddlewareCalls++
		return next(ctx, messages, options...)
	})
	providerMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		providerMiddlewareCalls++
		return next(ctx, messages, options...)
	})

	a := agent.New(agent.ProviderConfig{
		Run:         run,
		Middlewares: []agent.Middleware{nil, providerMiddleware, nil},
	}, agent.Config{
		Middlewares: []agent.Middleware{nil, agentMiddleware, nil},
	})
	if _, err := a.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("RunText() error = %v", err)
	}
	if providerCalls != 1 || agentMiddlewareCalls != 1 || providerMiddlewareCalls != 1 {
		t.Fatalf("calls = provider:%d agent middleware:%d provider middleware:%d, want all 1", providerCalls, agentMiddlewareCalls, providerMiddlewareCalls)
	}
}

func TestAgent_MessageInjectionRunsInsideProviderMiddleware(t *testing.T) {
	injection := &agent.MessageInjector{}
	session := &agent.Session{}
	var providerMiddlewareCalls int
	var calls [][]string
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		texts := make([]string, 0, len(messages))
		for _, msg := range messages {
			texts = append(texts, msg.String())
		}
		calls = append(calls, texts)
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if len(calls) == 1 {
				if err := injection.EnqueueMessages(session, message.NewText("during run")); err != nil {
					yield(nil, err)
					return
				}
				yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: "working"}}}, nil)
				return
			}
			yield(&agent.ResponseUpdate{Contents: message.Contents{
				&message.FunctionCallContent{CallID: "call-1", Name: "tool"},
			}}, nil)
		}
	}
	providerMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		providerMiddlewareCalls++
		return next(ctx, messages, options...)
	})
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{providerMiddleware}}, agent.Config{
		MessageInjector:                         injection,
		RequirePerServiceCallHistoryPersistence: true,
	})
	if err := injection.EnqueueMessages(session, message.NewText("before run")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunText(t.Context(), "original", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}

	if providerMiddlewareCalls != 1 {
		t.Fatalf("provider middleware calls = %d, want 1", providerMiddlewareCalls)
	}
	if len(calls) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(calls))
	}
	if want := []string{"original", "before run"}; !slices.Equal(calls[0], want) {
		t.Fatalf("first provider messages = %v, want %v", calls[0], want)
	}
	if want := []string{"original", "before run", "working", "during run"}; !slices.Equal(calls[1], want) {
		t.Fatalf("second provider messages = %v, want %v", calls[1], want)
	}
}

func TestAgent_PerServiceCallHistory_FunctionLoop(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, withSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/session=%t", stream, withSession), func(t *testing.T) {
				var calls, contextInvoking int
				var notifications []agent.InvokedContext
				contextProvider := contextProviderFunc{
					invoking: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
						contextInvoking++
						return append(slices.Clone(invoking.Messages), message.NewText("context")), invoking.Options, nil
					},
					invoked: func(_ context.Context, invoked agent.InvokedContext) error {
						notifications = append(notifications, invoked)
						return nil
					},
				}
				fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
					if len(notifications) != 1 {
						t.Errorf("tool executed before the first service call was persisted")
					}
					return "found", nil
				})
				a := agent.New(agent.ProviderConfig{
					Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
					Run: func(_ context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
						return func(yield func(*agent.ResponseUpdate, error) bool) {
							calls++
							if id, _ := agent.GetOption(opts, agent.WithServiceID); id != "" {
								t.Errorf("local history ID reached the service: %q", id)
							}
							switch calls {
							case 1:
								if got := messageStrings(messages); !slices.Equal(got, []string{"start", "context"}) {
									t.Fatalf("first request = %v", got)
								}
								yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
									&message.FunctionCallContent{CallID: "call-1", Name: "lookup", Arguments: `{}`},
								}}, nil)
								return
							case 2:
								if len(messages) != 4 || messages[2].Role != message.RoleAssistant || messages[3].Role != message.RoleTool {
									t.Fatalf("follow-up lost or duplicated history: %+v", messages)
								}
								call, callOK := messages[2].Contents[0].(*message.FunctionCallContent)
								result, resultOK := messages[3].Contents[0].(*message.FunctionResultContent)
								if !callOK || !resultOK || call.CallID != result.CallID || result.Result != "found" {
									t.Fatalf("tool call/result pair was not preserved: %+v", messages)
								}
							case 3:
								if got := messageStrings(messages); !slices.Equal(got, []string{"start", "context", "", "", "done", "next", "context"}) {
									t.Fatalf("restored session request = %v", got)
								}
							default:
								t.Fatal("unexpected extra provider call")
							}
							yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
						}
					},
				}, agent.Config{
					RequirePerServiceCallHistoryPersistence: true,
					ContextProviders:                        []agent.ContextProvider{contextProvider},
					Tools:                                   []tool.Tool{fn},
				})
				session := &agent.Session{}
				opts := []agent.Option{agent.Stream(stream)}
				if withSession {
					opts = append(opts, agent.WithSession(session))
				}
				response, err := a.RunText(t.Context(), "start", opts...).Collect()
				if err != nil {
					t.Fatal(err)
				}
				if calls != 2 || contextInvoking != 1 || len(notifications) != 2 {
					t.Fatalf("calls/context loads/notifications = %d/%d/%d, want 2/1/2", calls, contextInvoking, len(notifications))
				}
				if len(notifications[0].RequestMessages) != 2 || len(notifications[1].RequestMessages) != 1 || notifications[1].RequestMessages[0].Role != message.RoleTool {
					t.Fatal("completion notifications replayed messages from an earlier call")
				}
				if response.String() != "done" || response.ConversationID == nil || *response.ConversationID == "" {
					t.Fatalf("response did not report retained history: %+v", response)
				}
				if withSession {
					if session.ServiceID() != *response.ConversationID {
						t.Fatal("session and response history IDs differ")
					}
					data, err := json.Marshal(session)
					if err != nil {
						t.Fatal(err)
					}
					var restored agent.Session
					if err := json.Unmarshal(data, &restored); err != nil {
						t.Fatal(err)
					}
					if _, err := a.RunText(t.Context(), "next", agent.WithSession(&restored), agent.Stream(stream)).Collect(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestAgent_MessageInjection_HistoryPersistenceAndFinalResponse(t *testing.T) {
	for _, perCall := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("per-call=%t/stream=%t", perCall, stream), func(t *testing.T) {
				injection := &agent.MessageInjector{}
				session := &agent.Session{}
				var calls int
				usage := &message.UsageContent{Details: message.UsageDetails{InputTokenCount: 2, AdditionalCounts: map[string]int64{"custom": 3}}}
				a := agent.New(agent.ProviderConfig{Run: func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						calls++
						if calls == 1 {
							if err := injection.EnqueueMessages(session, message.NewText("injected")); err != nil {
								t.Fatal(err)
							}
						} else {
							want := []string{"injected"}
							if perCall {
								want = []string{"original", "reply-1", "injected"}
							}
							if got := messageStrings(messages); !slices.Equal(got, want) {
								t.Fatalf("follow-up request = %v, want %v", got, want)
							}
						}
						yield(&agent.ResponseUpdate{
							ResponseID: fmt.Sprintf("response-%d", calls), MessageID: fmt.Sprintf("message-%d", calls), Role: message.RoleAssistant,
							Contents: message.Contents{&message.TextContent{Text: fmt.Sprintf("reply-%d", calls)}, usage},
						}, nil)
					}
				}}, agent.Config{MessageInjector: injection, RequirePerServiceCallHistoryPersistence: perCall})
				response, err := a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(stream)).Collect()
				if err != nil {
					t.Fatal(err)
				}
				wantText := "reply-2"
				if stream {
					wantText = "reply-1\nreply-2"
				}
				if calls != 2 || response.ID != "response-2" || response.String() != wantText {
					t.Fatalf("calls=%d response ID=%q text=%q, want 2/response-2/%q", calls, response.ID, response.String(), wantText)
				}
				if got := response.Usage(); got.InputTokenCount != 4 || got.AdditionalCounts["custom"] != 6 {
					t.Errorf("usage = %+v, want totals from both calls", got)
				}
				if usage.Details.InputTokenCount != 2 || usage.Details.AdditionalCounts["custom"] != 3 {
					t.Fatal("usage aggregation mutated the provider's usage")
				}
				if perCall != (response.ConversationID != nil) {
					t.Errorf("retained history ID present=%t, want %t", response.ConversationID != nil, perCall)
				}
			})
		}
	}
}

func TestAgent_MessageInjection_NonStreamingUsesFinalConversationID(t *testing.T) {
	injection := &agent.MessageInjector{}
	session := &agent.Session{}
	var calls int
	a := agent.New(agent.ProviderConfig{Run: func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			calls++
			if calls == 1 {
				if err := injection.EnqueueMessages(session, message.NewText("injected")); err != nil {
					t.Fatal(err)
				}
				yield(&agent.ResponseUpdate{ConversationID: new("conversation-first"), Contents: message.Contents{&message.TextContent{Text: "first"}}}, nil)
				return
			}
			if id, _ := agent.GetOption(opts, agent.WithServiceID); id != "conversation-first" {
				t.Errorf("follow-up request ID = %q, want conversation-first", id)
			}
			yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: "final"}}}, nil)
		}
	}}, agent.Config{MessageInjector: injection})
	response, err := a.RunText(t.Context(), "start", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || response.String() != "final" || response.ConversationID != nil || session.ServiceID() != "" {
		t.Fatalf("calls=%d text=%q conversation=%v session=%q; want only the final stateless response", calls, response.String(), response.ConversationID, session.ServiceID())
	}
}

func TestAgent_PerServiceCallHistory_PauseAndFailure(t *testing.T) {
	serviceErr := errors.New("service failed")
	for _, mode := range []string{"failure", "pause", "cancelled pause"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var saved []*message.Message
			history := agent.NewHistoryProvider(agent.HistoryProviderConfig{
				SourceID: "history",
				Provide:  func(context.Context, agent.InvokingContext) ([]*message.Message, error) { return saved, nil },
				Store: func(_ context.Context, invoked agent.InvokedContext) error {
					saved = append(saved, invoked.RequestMessages...)
					saved = append(saved, invoked.ResponseMessages...)
					return nil
				},
			})
			var notifications []agent.InvokedContext
			observer := contextProviderFunc{invoked: func(ctx context.Context, invoked agent.InvokedContext) error {
				notifications = append(notifications, invoked)
				if mode == "cancelled pause" && len(notifications) == 2 {
					if ctx.Err() != nil {
						t.Error("early-exit persistence received an already cancelled context")
					}
					return errors.New("cleanup failed")
				}
				return nil
			}}
			var calls, closed int
			fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) { return "found", nil })
			a := agent.New(agent.ProviderConfig{
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
				Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						calls++
						defer func() { closed++ }()
						if calls == 1 {
							yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
								&message.FunctionCallContent{CallID: "call-1", Name: "lookup", Arguments: `{}`},
							}}, nil)
							return
						}
						if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "partial"}}}, nil) {
							return
						}
						yield(nil, serviceErr)
					}
				},
			}, agent.Config{
				RequirePerServiceCallHistoryPersistence: true,
				HistoryProvider:                         history,
				ContextProviders:                        []agent.ContextProvider{observer},
				Tools:                                   []tool.Tool{fn},
			})
			var runErr error
			for update, err := range a.RunText(ctx, "start", agent.WithSession(&agent.Session{}), agent.Stream(true)) {
				if err != nil {
					runErr = err
					break
				}
				if mode != "failure" && update != nil && update.String() == "partial" {
					if mode == "cancelled pause" {
						cancel()
					}
					break
				}
			}
			if calls != 2 || closed != 2 || len(notifications) != 2 {
				t.Fatalf("calls/closed/notifications = %d/%d/%d, want 2/2/2", calls, closed, len(notifications))
			}
			last := notifications[1]
			if len(last.RequestMessages) != 1 || last.RequestMessages[0].Role != message.RoleTool || len(last.ResponseMessages) != 0 {
				t.Fatalf("second notification lost the input or retained an incomplete response: %+v", last)
			}
			if mode == "failure" {
				if !errors.Is(runErr, serviceErr) || !errors.Is(last.Err, serviceErr) || len(saved) != 2 {
					t.Fatalf("failed call error=%v notification=%v saved=%d; want original failure without persisting the failed call", runErr, last.Err, len(saved))
				}
			} else if runErr != nil || last.Err != nil || len(saved) != 3 || saved[2].Role != message.RoleTool {
				t.Fatalf("pause error=%v notification=%v saved=%+v; want the pending tool result persisted", runErr, last.Err, saved)
			}
		})
	}
}

func TestAgent_PerServiceCallHistory_RealConversationID(t *testing.T) {
	serviceErr := errors.New("second service call failed")
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			initialID string
			fail      bool
		}{
			{name: "new conversation"},
			{name: "existing conversation", initialID: "conversation-0"},
			{name: "failure retains last completed call", initialID: "conversation-0", fail: true},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				simulatedStream := stream && tc.initialID == ""
				session := &agent.Session{}
				session.SetServiceID(tc.initialID)
				var calls int
				var notifications []agent.InvokedContext
				observer := contextProviderFunc{invoked: func(_ context.Context, invoked agent.InvokedContext) error {
					notifications = append(notifications, invoked)
					return nil
				}}
				fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
					if session.ServiceID() != "conversation-1" || len(notifications) != 1 {
						t.Fatal("tool ran before the completed service call's session update and notification")
					}
					return "found", nil
				})
				a := agent.New(agent.ProviderConfig{
					Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
					Run: func(_ context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
						return func(yield func(*agent.ResponseUpdate, error) bool) {
							calls++
							wantID := tc.initialID
							if calls == 2 {
								wantID = "conversation-1"
								wantMessages := 1
								if simulatedStream {
									// ID-less updates on an initially local stream keep
									// advertising local persistence for the next call.
									wantID = ""
									wantMessages = 3
								}
								if len(messages) != wantMessages || messages[len(messages)-1].Role != message.RoleTool {
									t.Fatalf("follow-up messages=%+v, want %d messages ending in the new tool result", messages, wantMessages)
								}
							}
							if id, _ := agent.GetOption(opts, agent.WithServiceID); id != wantID {
								t.Errorf("request ID = %q, want %q", id, wantID)
							}
							if calls == 2 && tc.fail {
								yield(nil, serviceErr)
								return
							}
							if !yield(&agent.ResponseUpdate{ConversationID: new(fmt.Sprintf("conversation-%d", calls))}, nil) {
								return
							}
							contents := message.Contents{&message.TextContent{Text: "done"}}
							if calls == 1 {
								contents = message.Contents{&message.FunctionCallContent{CallID: "call-1", Name: "lookup", Arguments: `{}`}}
							}
							yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: contents}, nil)
						}
					},
				}, agent.Config{
					RequirePerServiceCallHistoryPersistence: true,
					ContextProviders:                        []agent.ContextProvider{observer},
					Tools:                                   []tool.Tool{fn},
				})
				response, err := a.RunText(t.Context(), "start", agent.WithSession(session), agent.Stream(stream)).Collect()
				if calls != 2 || len(notifications) != 2 {
					t.Fatalf("calls/notifications = %d/%d, want 2/2", calls, len(notifications))
				}
				if tc.fail {
					if !errors.Is(err, serviceErr) || !errors.Is(notifications[1].Err, serviceErr) || session.ServiceID() != "conversation-1" {
						t.Fatalf("failure error=%v notification=%v session=%q", err, notifications[1].Err, session.ServiceID())
					}
				} else if err != nil {
					t.Fatal(err)
				} else {
					if response.ConversationID == nil || *response.ConversationID == "" || session.ServiceID() != "conversation-2" {
						t.Fatalf("response/session ID = %v/%q, want a retained-history response ID and conversation-2 on the session", response.ConversationID, session.ServiceID())
					}
					if gotRealID := *response.ConversationID == "conversation-2"; gotRealID == simulatedStream {
						t.Errorf("response has real service ID=%t, want %t", gotRealID, !simulatedStream)
					}
				}
			})
		}
	}
}

func TestAgent_PerServiceCallHistory_ConflictsAfterNotifyingProviders(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, withResponse := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/response=%t", stream, withResponse), func(t *testing.T) {
				var notifications []agent.InvokedContext
				history := agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "history", Store: func(_ context.Context, invoked agent.InvokedContext) error {
					notifications = append(notifications, invoked)
					return nil
				}})
				a := agent.New(agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						update := &agent.ResponseUpdate{ConversationID: new("service-conversation")}
						if withResponse {
							update.Role = message.RoleAssistant
							update.Contents = message.Contents{&message.TextContent{Text: "response"}}
						}
						yield(update, nil)
					}
				}}, agent.Config{HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true})
				session := &agent.Session{}
				_, err := a.RunText(t.Context(), "start", agent.WithSession(session), agent.Stream(stream)).Collect()
				if err == nil || !strings.Contains(err.Error(), "HistoryProvider") || len(notifications) != 1 || session.ServiceID() != "" {
					t.Fatalf("error=%v notifications=%d session=%q; want conflict after one notification without committing the ID", err, len(notifications), session.ServiceID())
				}
				if notifications[0].Err != nil {
					t.Fatalf("notification error = %v, want successful service call before conflict", notifications[0].Err)
				}
				if got := messageStrings(notifications[0].RequestMessages); !slices.Equal(got, []string{"start"}) {
					t.Fatalf("stored requests = %v, want [start]", got)
				}
				if withResponse {
					if got := messageStrings(notifications[0].ResponseMessages); !slices.Equal(got, []string{"response"}) {
						t.Fatalf("stored responses = %v, want [response]", got)
					}
				}
			})
		}
	}
}

func TestAgent_PerServiceCallHistory_ApprovalRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls, toolCalls int
			fn := tool.ApprovalRequiredFunc(functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
				toolCalls++
				return "found", nil
			}))
			a := agent.New(agent.ProviderConfig{
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
				Run: func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						calls++
						if calls == 1 {
							yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
								&message.FunctionCallContent{CallID: "call-1", Name: "lookup", Arguments: `{}`},
							}}, nil)
							return
						}
						if len(messages) != 3 || messages[0].String() != "start" || messages[1].Role != message.RoleAssistant || messages[2].Role != message.RoleTool {
							t.Fatalf("approved request lost or duplicated history: %+v", messages)
						}
						call, callOK := messages[1].Contents[0].(*message.FunctionCallContent)
						result, resultOK := messages[2].Contents[0].(*message.FunctionResultContent)
						if !callOK || !resultOK || call.CallID != "call-1" || result.CallID != call.CallID {
							t.Fatalf("approval did not preserve the original call/result pair: %+v", messages)
						}
						yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
					}
				},
			}, agent.Config{Tools: []tool.Tool{fn}, RequirePerServiceCallHistoryPersistence: true})
			session := &agent.Session{}
			response, err := a.RunText(t.Context(), "start", agent.WithSession(session), agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			var request *message.ToolApprovalRequestContent
			for content := range response.Contents() {
				if approval, ok := content.(*message.ToolApprovalRequestContent); ok {
					request = approval
				}
			}
			if request == nil || toolCalls != 0 {
				t.Fatal("expected an approval request before executing the tool")
			}
			data, err := json.Marshal(session)
			if err != nil {
				t.Fatal(err)
			}
			var restored agent.Session
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if _, err := a.RunMessage(t.Context(), message.New(request.CreateResponse(true, "")), agent.WithSession(&restored), agent.Stream(stream)).Collect(); err != nil {
				t.Fatal(err)
			}
			if calls != 2 || toolCalls != 1 {
				t.Fatalf("provider/tool calls = %d/%d, want 2/1", calls, toolCalls)
			}
		})
	}
}

func TestAgent_PerServiceCallHistory_BackgroundAndContinuation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, continuation := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/continuation=%t", stream, continuation), func(t *testing.T) {
				var historyLoads, notifications int
				history := agent.NewHistoryProvider(agent.HistoryProviderConfig{
					SourceID: "history",
					Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
						historyLoads++
						return []*message.Message{message.NewText("history")}, nil
					},
					Store: func(_ context.Context, invoked agent.InvokedContext) error {
						notifications++
						if got := messageStrings(invoked.ResponseMessages); !slices.Equal(got, []string{"done"}) {
							t.Errorf("stored response = %v, want [done]", got)
						}
						return nil
					},
				})
				a := agent.New(agent.ProviderConfig{Run: func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						if continuation && len(messages) != 0 || !continuation && (len(messages) != 1 || messages[0].String() != "start") {
							t.Fatalf("unexpected history in background request: %+v", messages)
						}
						yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
					}
				}}, agent.Config{HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true})
				session := &agent.Session{}
				opts := []agent.Option{agent.WithSession(session), agent.Stream(stream)}
				messages := []*message.Message{message.NewText("start")}
				if continuation {
					opts = append(opts, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "response-1")))
					messages = nil
				} else {
					opts = append(opts, agent.AllowBackgroundResponses(true))
				}
				response, err := a.Run(t.Context(), messages, opts...).Collect()
				if err != nil {
					t.Fatal(err)
				}
				if historyLoads != 0 || notifications != 2 || response.ConversationID != nil || session.ServiceID() != "" {
					t.Fatalf("loads=%d notifications=%d response/session ID=%v/%q; want per-call and end-of-run notifications without local history simulation", historyLoads, notifications, response.ConversationID, session.ServiceID())
				}
			})
		}
	}
}

func TestAgent_PerServiceCallHistory_BackgroundPauseAndFailure(t *testing.T) {
	serviceErr := errors.New("background service failed")
	notificationErr := errors.New("notification failed")
	for _, continuation := range []bool{false, true} {
		for _, tc := range []struct {
			mode   string
			stream bool
		}{
			{mode: "failure"},
			{mode: "failure", stream: true},
			{mode: "pause", stream: true},
			{mode: "cancelled pause", stream: true},
		} {
			mode := tc.mode
			t.Run(fmt.Sprintf("stream=%t/continuation=%t/%s", tc.stream, continuation, mode), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var notifications []agent.InvokedContext
				observer := contextProviderFunc{invoked: func(ctx context.Context, invoked agent.InvokedContext) error {
					notifications = append(notifications, invoked)
					if mode == "cancelled pause" && ctx.Err() != nil {
						t.Error("early-exit persistence received a cancelled context")
					}
					return notificationErr
				}}
				closed := false
				a := agent.New(agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						defer func() { closed = true }()
						if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "partial"}}}, nil) {
							return
						}
						yield(nil, serviceErr)
					}
				}}, agent.Config{
					RequirePerServiceCallHistoryPersistence: true,
					ContextProviders:                        []agent.ContextProvider{observer},
				})
				session := &agent.Session{}
				opts := []agent.Option{agent.WithSession(session), agent.Stream(tc.stream)}
				messages := []*message.Message{message.NewText("start")}
				if continuation {
					opts = append(opts, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "response-1")))
					messages = nil
				} else {
					opts = append(opts, agent.AllowBackgroundResponses(true))
				}
				var runErr error
				for update, err := range a.Run(ctx, messages, opts...) {
					if err != nil {
						runErr = err
						break
					}
					if update.ConversationID != nil {
						t.Error("background update advertised simulated local history")
					}
					if mode != "failure" {
						if mode == "cancelled pause" {
							cancel()
						}
						break
					}
				}
				if !closed || session.ServiceID() != "" {
					t.Fatalf("closed=%t session=%q; incomplete calls must release the stream without changing the session ID", closed, session.ServiceID())
				}
				if continuation && mode != "failure" {
					if len(notifications) != 0 {
						t.Fatal("an abandoned continuation without new inputs should not notify providers")
					}
					return
				}
				if len(notifications) != 1 {
					t.Fatalf("notifications=%d, want one per-call notification", len(notifications))
				}
				notification := notifications[0]
				if !slices.Equal(notification.RequestMessages, messages) || len(notification.ResponseMessages) != 0 {
					t.Fatalf("notification must contain only this call's inputs, not its partial response: %+v", notification)
				}
				if mode == "failure" {
					if !errors.Is(runErr, serviceErr) || !errors.Is(runErr, notificationErr) || !errors.Is(notification.Err, serviceErr) {
						t.Fatalf("run error=%v notification error=%v; want original service failure retained with the notification failure", runErr, notification.Err)
					}
				} else if runErr != nil || notification.Err != nil {
					t.Fatalf("run error=%v notification error=%v; a cooperative pause must persist inputs best-effort", runErr, notification.Err)
				}
			})
		}
	}
}

func TestAgent_PerServiceCallHistory_StreamingUsesInitialSimulationMode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		initialID    string
		background   bool
		continuation bool
		wantLocalID  bool
	}{
		{name: "local request", wantLocalID: true},
		{name: "service-managed request", initialID: "conversation-request"},
		{name: "background request", background: true},
		{name: "continuation request", continuation: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := agent.New(agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					if !yield(&agent.ResponseUpdate{ConversationID: new("conversation-response")}, nil) {
						return
					}
					yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
				}
			}}, agent.Config{RequirePerServiceCallHistoryPersistence: true})
			session := &agent.Session{}
			session.SetServiceID(tc.initialID)
			opts := []agent.Option{agent.WithSession(session), agent.Stream(true), agent.AllowBackgroundResponses(tc.background)}
			messages := []*message.Message{message.NewText("start")}
			if tc.continuation {
				opts = append(opts, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "response-1")))
				messages = nil
			}
			var updates []*agent.ResponseUpdate
			for update, err := range a.Run(t.Context(), messages, opts...) {
				if err != nil {
					t.Fatal(err)
				}
				updates = append(updates, update)
			}
			if len(updates) != 2 || updates[0].ConversationID == nil || *updates[0].ConversationID != "conversation-response" {
				t.Fatalf("expected the original service ID on the first of two updates: %+v", updates)
			}
			id := updates[1].ConversationID
			if tc.wantLocalID {
				if id == nil || *id == "" || *id == "conversation-response" {
					t.Errorf("later ID-less update should advertise local persistence for an initially local request, got %v", id)
				}
			} else if id != nil {
				t.Errorf("later ID-less update should remain unchanged when simulation is skipped, got %q", *id)
			}
			if session.ServiceID() != "conversation-response" {
				t.Errorf("session must use the original service response ID, got %q", session.ServiceID())
			}
		})
	}
}

func TestAgent_RunText(t *testing.T) {
	var capturedMessages []*message.Message
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedMessages = messages
		},
	).AddText("Hello, world!")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	resp, err := a.RunText(ctx, "test message").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify message was converted correctly
	if len(capturedMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedMessages))
	}

	if capturedMessages[0].Role != message.RoleUser {
		t.Errorf("expected role %s, got %s", message.RoleUser, capturedMessages[0].Role)
	}

	textContent, ok := capturedMessages[0].Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", capturedMessages[0].Contents[0])
	}

	if textContent.Text != "test message" {
		t.Errorf("expected text 'test message', got %q", textContent.Text)
	}

	// Verify response and author info
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(resp.Messages))
	}

	if resp.Messages[0].Role != message.RoleAssistant {
		t.Errorf("expected role %s, got %s", message.RoleAssistant, resp.Messages[0].Role)
	}

	if resp.AgentID != a.ID() {
		t.Errorf("expected agent ID %q, got %q", a.ID(), resp.AgentID)
	}

	if resp.Messages[0].AuthorName != a.Name() {
		t.Errorf("expected author name %q, got %q", a.Name(), resp.Messages[0].AuthorName)
	}
}

func TestAgent_RunTextRejectsBlankMessage(t *testing.T) {
	providerCalled := false
	a := agent.New(agent.ProviderConfig{
		Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			providerCalled = true
			return func(func(*agent.ResponseUpdate, error) bool) {}
		},
	}, agent.Config{ID: "test-agent"})

	for _, input := range []string{"", " ", "\t"} {
		if _, err := a.RunText(t.Context(), input).Collect(); err == nil {
			t.Errorf("RunText(%q) error = nil, want blank-message error", input)
		}
	}
	if providerCalled {
		t.Fatal("provider was called for blank input")
	}
}

func TestAgent_Run_AppliesAuthorAttributionAfterProviderMiddleware(t *testing.T) {
	var observed bool
	middleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for update, err := range next(ctx, messages, options...) {
				if update != nil {
					observed = true
					if update.AgentID != "" || update.AuthorName != "" {
						t.Errorf("expected attribution after provider middleware, got agent ID %q and author name %q", update.AgentID, update.AuthorName)
					}
				}
				if !yield(update, err) {
					return
				}
			}
		}
	})
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{middleware}}, agent.Config{
		ID:   "test-agent",
		Name: "Test Agent",
	})

	for update, err := range a.RunText(t.Context(), "hello") {
		if err != nil {
			t.Fatal(err)
		}
		if update.AgentID != a.ID() || update.AuthorName != a.Name() {
			t.Fatalf("expected agent attribution, got agent ID %q and author name %q", update.AgentID, update.AuthorName)
		}
	}
	if !observed {
		t.Fatal("expected provider middleware to observe a response update")
	}
}

func TestAgent_RunMessage(t *testing.T) {
	var capturedMessages []*message.Message
	var capturedOptions []agent.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedMessages = messages
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	inputMsg := message.NewText("input")
	customOption := agent.Stream(false)
	resp, err := a.RunMessage(ctx, inputMsg, customOption).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify message was passed through
	if len(capturedMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedMessages))
	}

	if capturedMessages[0] != inputMsg {
		t.Errorf("expected input message to be passed through")
	}

	// Verify options were passed
	if len(capturedOptions) == 0 {
		t.Fatal("expected options to be passed, got none")
	}

	if _, ok := agent.GetOption(capturedOptions, agent.Stream); !ok {
		t.Error("expected Stream option to be present")
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(resp.Messages))
	}
}

func TestAgent_RunMessageRejectsNil(t *testing.T) {
	a := agenttest.New(agenttest.NewResponseBuilder().Build())
	if _, err := a.RunMessage(t.Context(), nil).Collect(); err == nil {
		t.Fatal("RunMessage(nil) error = nil, want error")
	}
}

func TestAgent_Run(t *testing.T) {
	var capturedMessages []*message.Message
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedMessages = messages
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	messages := []*message.Message{
		message.NewText("first"),
		message.NewText("second"),
	}

	ctx := t.Context()
	resp, err := a.Run(ctx, messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(capturedMessages))
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(resp.Messages))
	}
}

func TestAgent_Run_DoesNotMutateInputOptionBackingArray(t *testing.T) {
	a := agenttest.New(agenttest.NewResponseBuilder().AddText("response").Build())
	options := make([]agent.Option, 1, 3)
	options[0] = agent.Stream(false)
	firstSentinel := agent.WithInstructions("first sentinel")
	secondSentinel := agent.WithInstructions("second sentinel")
	backing := options[:cap(options)]
	backing[1] = firstSentinel
	backing[2] = secondSentinel

	if _, err := a.RunText(t.Context(), "input", options...).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backing[1] != firstSentinel || backing[2] != secondSentinel {
		t.Fatal("expected Agent.Run not to modify the input option backing array")
	}
}

func TestAgent_Run_RejectsMessagesWithContinuationToken(t *testing.T) {
	runCalled := false
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			runCalled = true
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	token := agenttest.NewContinuationToken(t, "token-123")
	_, err := a.RunText(ctx, "test", agent.WithContinuationToken(token)).Collect()
	if err == nil {
		t.Fatal("expected error when continuation token and messages are both provided")
	}
	if err.Error() != "messages are not allowed when continuing a background response using a continuation token" {
		t.Fatalf("unexpected error: %v", err)
	}
	if runCalled {
		t.Fatal("expected run function not to be called when validation fails")
	}
}

func TestAgent_Run_RejectsRawContinuationToken(t *testing.T) {
	runCalled := false
	runFn := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent"})

	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken("raw-token")).Collect()
	if err == nil {
		t.Fatal("expected error for raw continuation token")
	}
	if err.Error() != "continuation token is not a valid agent continuation token" {
		t.Fatalf("unexpected error: %v", err)
	}
	if runCalled {
		t.Fatal("expected run function not to be called")
	}
}

func TestAgent_Run_CreatesSession(t *testing.T) {
	var capturedOptions []agent.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that a session was created and passed
	session, ok := agent.GetOption(capturedOptions, agent.WithSession)
	if !ok {
		t.Fatal("expected session to be created")
	}

	if session == nil {
		t.Error("expected session to be non-nil")
	}
}

func TestAgent_Run_RequiresSessionWhenAllowBackgroundResponsesEnabled(t *testing.T) {
	runCalled := false
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			runCalled = true
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agent.AllowBackgroundResponses(true)).Collect()
	if err == nil {
		t.Fatal("expected error when AllowBackgroundResponses is enabled without a session")
	}
	if err.Error() != "a session must be provided when AllowBackgroundResponses is enabled" {
		t.Fatalf("unexpected error: %v", err)
	}
	if runCalled {
		t.Fatal("expected run function not to be called when validation fails")
	}
}

func TestAgent_Run_RejectsNilSessionWhenAllowBackgroundResponsesEnabled(t *testing.T) {
	for _, stream := range []bool{false, true} {
		runCalled := false
		a := agenttest.New(agenttest.NewResponseBuilder(
			func(context.Context, []*message.Message, ...agent.Option) {
				runCalled = true
			},
		).AddText("response").Build())

		_, err := a.RunText(t.Context(), "test", agent.WithSession(nil), agent.AllowBackgroundResponses(true), agent.Stream(stream)).Collect()
		if err == nil || err.Error() != "a session must be provided when AllowBackgroundResponses is enabled" {
			t.Fatalf("RunText() with stream=%t error = %v, want session-required error", stream, err)
		}
		if runCalled {
			t.Fatalf("run function called with a nil background-response session and stream=%t", stream)
		}
	}
}

func TestAgent_Run_UsesProvidedSession(t *testing.T) {
	var capturedOptions []agent.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	providedSession := agenttest.CreateSession()
	_, err := a.RunText(ctx, "test", agent.WithSession(providedSession)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	session, ok := agent.GetOption(capturedOptions, agent.WithSession)
	if !ok {
		t.Fatal("expected session to be present")
	}

	if session != providedSession {
		t.Error("expected provided session to be used")
	}
}

func TestAgent_Run_PrependsAgentOptions(t *testing.T) {
	var capturedOptions []agent.Option
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{{
			Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
				func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
					capturedOptions = opts
				},
			},
			Responses: []agenttest.Response{
				{Response: &agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "response"},
					},
				}},
			},
		}},
	}

	agentOption := agent.Stream(true)
	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		ID:         "test",
		Name:       "test",
		RunOptions: []agent.Option{agentOption},
	})

	ctx := t.Context()
	callOption := agent.Stream(false)
	_, err := a.RunText(ctx, "test", callOption).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent options should be prepended, so call options come after
	if len(capturedOptions) < 2 {
		t.Fatalf("expected at least 2 options, got %d", len(capturedOptions))
	}
}

func TestAgent_New_DoesNotAutomaticallyInvokeTools(t *testing.T) {
	invoked := false
	weatherTool := functool.MustNew(functool.Config{Name: "GetWeather", Description: "Get weather"}, func(context.Context, struct{}) (string, error) {
		invoked = true
		return "sunny", nil
	})

	runFn := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call-1", Name: "GetWeather", Arguments: `{}`},
				},
			}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{})
	resp, err := a.RunText(t.Context(), "weather", agent.WithTool(weatherTool)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invoked {
		t.Fatal("expected agent.New not to invoke tools automatically")
	}
	if len(resp.Messages) != 1 || len(resp.Messages[0].Contents) != 1 {
		t.Fatalf("expected one function-call response message, got %#v", resp.Messages)
	}
	if _, ok := resp.Messages[0].Contents[0].(*message.FunctionCallContent); !ok {
		t.Fatalf("expected function call content to pass through, got %T", resp.Messages[0].Contents[0])
	}
}

func TestAgent_Run_AddsConfigToolsToRunOptions(t *testing.T) {
	var capturedOptions []agent.Option
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{{
			Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
				func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
					capturedOptions = opts
				},
			},
			Responses: []agenttest.Response{{
				Response: &agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "response"},
					},
				},
			}},
		}},
	}

	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{
		ID:    "test",
		Name:  "test",
		Tools: []tool.Tool{stubTool{name: "weather"}, stubTool{name: "time"}},
	})

	_, err := a.RunText(t.Context(), "test").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var names []string
	for configuredTool := range agent.AllOptions(capturedOptions, agent.WithTool) {
		names = append(names, configuredTool.Name())
	}

	if !slices.Equal(names, []string{"weather", "time"}) {
		t.Fatalf("expected configured tools to be added to run options, got %v", names)
	}
}

func TestAgent_Run_StreamingResponses(t *testing.T) {
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("chunk 1").
		AddText("chunk 2").
		AddText("chunk 3")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	updates := []*agent.ResponseUpdate{}
	for update, err := range a.RunText(ctx, "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}
	if got := updates[0].String(); got != "chunk 1" {
		t.Fatalf("first update text = %q, want chunk 1", got)
	}
	if got := updates[1].String(); got != "chunk 2" {
		t.Fatalf("second update text = %q, want chunk 2", got)
	}
	if got := updates[2].String(); got != "chunk 3" {
		t.Fatalf("third update text = %q, want chunk 3", got)
	}
}

func TestAgent_Run_AddsMetadataToContext(t *testing.T) {
	var capturedCtx context.Context
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedCtx = ctx
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actx, ok := agent.AgentFromContext(capturedCtx)
	if !ok {
		t.Fatal("expected metadata in context")
	}

	if a != actx {
		t.Errorf("expected agent %+v, got %+v", a, actx)
	}
}

func TestAgent_Run_InvokesSingleContextMiddleware(t *testing.T) {
	mw := &prependMiddleware{
		prependMessages: []*message.Message{{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "context message"}}}},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}

	a := newGenericTestAgent(runFn, []agent.Middleware{mw})

	ctx := t.Context()
	session := agenttest.CreateSession()
	_, err := a.RunText(ctx, "user input", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mw.runCalls != 1 {
		t.Errorf("expected 1 middleware call, got %d", mw.runCalls)
	}

	foundContext := false
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "context message" {
				foundContext = true
			}
		}
	}
	if !foundContext {
		t.Error("expected context message to be included in messages sent to run function")
	}
}

func TestAgent_Run_MarksMiddlewareAddedMessagesWithSource(t *testing.T) {
	added := message.NewText("middleware")
	var middlewareMessages []*message.Message
	mw := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		messages = append(slices.Clone(messages), added)
		middlewareMessages = messages
		return next(ctx, messages, opts...)
	})

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, []agent.Middleware{mw})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(capturedMessages))
	}
	if capturedMessages[1] == added {
		t.Fatal("expected middleware message to be cloned before source stamping")
	}
	if capturedMessages[1].Source != (message.Source{Type: agent.SourceTypeMiddleware}) {
		t.Fatalf("middleware message source = %#v, want middleware source", capturedMessages[1].Source)
	}
	if middlewareMessages[1] != added || middlewareMessages[1].Source != (message.Source{}) {
		t.Fatal("expected source stamping not to mutate messages passed by middleware")
	}
}

func TestAgent_Run_PreservesMiddlewareAddedMessagesWithSource(t *testing.T) {
	source := message.Source{Type: agent.SourceTypeContextProvider, ID: "ctx"}
	added := message.NewText("context")
	added.Source = source
	mw := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		messages = append(slices.Clone(messages), added)
		return next(ctx, messages, opts...)
	})

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, []agent.Middleware{mw})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(capturedMessages))
	}
	if capturedMessages[1] != added {
		t.Fatal("expected middleware message with existing source to be preserved")
	}
	if capturedMessages[1].Source != source {
		t.Fatalf("middleware message source = %#v, want %#v", capturedMessages[1].Source, source)
	}
}

func TestAgent_Run_ContextMiddlewareReceivesSession(t *testing.T) {
	mw := &prependMiddleware{}
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, []agent.Middleware{mw})

	ctx := t.Context()
	session := agenttest.CreateSession()
	_, err := a.RunText(ctx, "test", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mw.lastSession != session {
		t.Error("expected middleware to receive the session")
	}
}

func TestAgent_Run_ContextMiddlewareCanFailBeforeInvokingNext(t *testing.T) {
	middlewareErr := errors.New("middleware failed")
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, []agent.Middleware{&errorMiddleware{err: middlewareErr}})

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, middlewareErr) {
		t.Fatalf("expected %v, got %v", middlewareErr, err)
	}
}

func TestAgent_Run_MiddlewareObservesRunFailure(t *testing.T) {
	runErr := errors.New("run failed")
	tracker := &trackingMiddleware{}
	a := newGenericTestAgent(failRunFunc(runErr), []agent.Middleware{tracker})

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agent.WithSession(agenttest.CreateSession())).Collect()
	if err == nil {
		t.Fatal("expected error")
	}

	if tracker.runCalls != 1 {
		t.Errorf("expected 1 middleware call, got %d", tracker.runCalls)
	}
	if !errors.Is(tracker.lastErr, runErr) {
		t.Errorf("expected middleware to observe %v, got %v", runErr, tracker.lastErr)
	}
}

func TestAgent_Run_IncludesInstructionsRunOption(t *testing.T) {
	var capturedMessages []*message.Message
	var capturedInstructions []string
	runFn := func(_ context.Context, msgs []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		capturedInstructions = slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, nil, agent.WithInstructions("  You are a helpful assistant.  "))

	ctx := t.Context()
	_, err := a.RunText(ctx, "hello", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := messageStrings(capturedMessages); !slices.Equal(got, []string{"hello"}) {
		t.Fatalf("messages = %v, want [hello]", got)
	}
	if capturedMessages[0].Role != message.RoleUser {
		t.Fatalf("request message role = %q, want %q", capturedMessages[0].Role, message.RoleUser)
	}
	if !slices.Equal(capturedInstructions, []string{"You are a helpful assistant."}) {
		t.Fatalf("instructions = %q, want %q", capturedInstructions, []string{"You are a helpful assistant."})
	}
}

func TestAgent_Run_CombinesInstructionOptions(t *testing.T) {
	var capturedInstructions []string
	runFn := func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedInstructions = slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, nil, agent.WithInstructions(" base instructions "))

	_, err := a.RunText(t.Context(), "hello", agent.WithSession(agenttest.CreateSession()), agent.WithInstructions(" run instructions ")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(capturedInstructions, []string{"base instructions", "run instructions"}) {
		t.Fatalf("instructions = %q, want combined instruction options", capturedInstructions)
	}
}

func TestRun_Collect(t *testing.T) {
	providerCalls := 0
	responseBuilder := agenttest.NewResponseBuilder(func(context.Context, []*message.Message, ...agent.Option) {
		providerCalls++
	}).
		AddText("hello").
		AddText(" world")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	resp, err := a.RunText(ctx, "test").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}
	if resp == nil {
		t.Fatal("expected a collected response")
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message after coalescing, got %d", len(resp.Messages))
	}

	if resp.Messages[0].Role != message.RoleAssistant {
		t.Errorf("expected role %s, got %s", message.RoleAssistant, resp.Messages[0].Role)
	}
	if got := resp.String(); got != "hello world" {
		t.Errorf("collected text = %q, want hello world", got)
	}
}

func TestRun_Collect_WithError(t *testing.T) {
	expectedErr := errors.New("collection error")
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("before error").
		AddError(expectedErr).
		AddText("after error")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test").Collect()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestRun_All(t *testing.T) {
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("chunk 1").
		AddText("chunk 2").
		AddText("chunk 3")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	updates := []*agent.ResponseUpdate{}
	for update, err := range a.RunText(ctx, "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}
}

func TestRun_All_WithError(t *testing.T) {
	expectedErr := errors.New("streaming error")
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("before error").
		AddError(expectedErr).
		AddText("after error")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	updateCount := 0
	var receivedErr error
	for _, err := range a.RunText(ctx, "test", agent.Stream(true)) {
		if err != nil {
			receivedErr = err
			break
		}
		updateCount++
	}

	if receivedErr == nil {
		t.Fatal("expected error, got nil")
	}

	if receivedErr != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, receivedErr)
	}

	if updateCount != 1 {
		t.Errorf("expected 1 update before error, got %d", updateCount)
	}
}

func TestAgent_Run_ContextProvider_RunsWithServiceManagedSession(t *testing.T) {
	provideCalled := false
	var capturedMessages []*message.Message
	var storedResponseMessages []*message.Message

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return []*message.Message{{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "Extra context"}}}}, nil, nil
		},
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			storedResponseMessages = invoked.ResponseMessages
			return nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{ConversationID: new("server-managed"), Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Response"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	session := agenttest.CreateSession()
	session.SetServiceID("server-managed")
	_, err := a.RunText(t.Context(), "Hello", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !provideCalled {
		t.Fatal("expected history provider to be used for service-managed sessions")
	}
	if len(capturedMessages) != 2 {
		t.Fatal("expected provider context to be appended to request")
	}
	if capturedMessages[0].Role != message.RoleUser {
		t.Errorf("request role = %q, want user", capturedMessages[0].Role)
	}
	if capturedMessages[1].Role != message.RoleSystem {
		t.Errorf("context role = %q, want system", capturedMessages[1].Role)
	}
	if capturedMessages[0].String() != "Hello" || capturedMessages[1].String() != "Extra context" {
		t.Fatalf("expected message order [Hello Extra context], got [%s %s]", capturedMessages[0].String(), capturedMessages[1].String())
	}
	if got := messageStrings(storedResponseMessages); !slices.Equal(got, []string{"Response"}) {
		t.Fatalf("stored response messages = %v, want [Response]", got)
	}
}

func TestAgent_Run_ContextProviders_SkipWithContinuationToken(t *testing.T) {
	provideCalled := false
	runCalled := false

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return nil, nil, nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		if len(msgs) != 0 {
			t.Fatalf("expected no messages with continuation token run, got %d", len(msgs))
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	token := agenttest.NewContinuationToken(t, "ct-1")
	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken(token)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !runCalled {
		t.Fatal("expected provider run function to be called")
	}
	if provideCalled {
		t.Fatal("expected context provider to be skipped with continuation token")
	}
}

func TestAgent_Run_StreamingContinuationToken_SavesInputMessagesAndUpdates(t *testing.T) {
	runCalls := 0
	runFn := func(_ context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalls++
		if runCalls == 1 {
			if got := messageStrings(messages); !slices.Equal(got, []string{"Tell me a story"}) {
				t.Fatalf("initial messages = %v, want [Tell me a story]", got)
			}
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Once"}}, ContinuationToken: "inner-1"}, nil)
			}
		}

		if len(messages) != 0 {
			t.Fatalf("resume messages = %v, want none", messageStrings(messages))
		}
		if token, _ := agent.GetOption(options, agent.WithContinuationToken); token != "inner-1" {
			t.Fatalf("resume continuation token = %q, want inner-1", token)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " upon"}}, ContinuationToken: "inner-2"}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " a"}}, ContinuationToken: "inner-3"}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " time"}}, ContinuationToken: "inner-4"}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()

	var firstToken string
	for update, err := range a.RunText(t.Context(), "Tell me a story", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected initial error: %v", err)
		}
		firstToken = update.ContinuationToken
		break
	}
	first := agenttest.DecodeContinuationToken(t, firstToken)
	if first.Type != agenttest.ContinuationTokenType || first.InnerToken != "inner-1" {
		t.Fatalf("first token = %#v, want wrapped inner-1", first)
	}
	if got := messageStrings(first.InputMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("first token input messages = %v, want [Tell me a story]", got)
	}
	if len(first.ResponseUpdates) != 1 || first.ResponseUpdates[0].String() != "Once" {
		t.Fatalf("first token updates = %v, want [Once]", updateStrings(first.ResponseUpdates))
	}

	var tokens []agenttest.ContinuationToken
	for update, err := range a.Run(t.Context(), nil, agent.WithSession(session), agent.WithContinuationToken(firstToken), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected resume error: %v", err)
		}
		token := agenttest.DecodeContinuationToken(t, update.ContinuationToken)
		if token.Type != agenttest.ContinuationTokenType {
			t.Fatalf("resume token type = %q, want %q", token.Type, agenttest.ContinuationTokenType)
		}
		tokens = append(tokens, token)
	}
	if len(tokens) != 3 {
		t.Fatalf("resume token count = %d, want 3", len(tokens))
	}
	last := tokens[len(tokens)-1]
	if last.InnerToken != "inner-4" {
		t.Fatalf("last inner token = %q, want inner-4", last.InnerToken)
	}
	if got := messageStrings(last.InputMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("last token input messages = %v, want [Tell me a story]", got)
	}
	if got := updateStrings(last.ResponseUpdates); !slices.Equal(got, []string{"Once", " upon", " a", " time"}) {
		t.Fatalf("last token updates = %v, want [Once  upon  a  time]", got)
	}
}

func TestAgent_Run_ContinuationToken_PersistsSavedResponseUpdates(t *testing.T) {
	var historyResponseMessages []*message.Message
	var contextResponseMessages []*message.Message
	var historyStoreCalls, contextStoreCalls int
	var historyStoreSawContinuationToken bool
	var contextStoreSawContinuationToken bool
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			historyStoreCalls++
			historyResponseMessages = invoked.ResponseMessages
			_, historyStoreSawContinuationToken = agent.GetOption(invoked.Options, agent.WithContinuationToken)
			return nil
		},
	})
	contextProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			contextStoreCalls++
			contextResponseMessages = invoked.ResponseMessages
			_, contextStoreSawContinuationToken = agent.GetOption(invoked.Options, agent.WithContinuationToken)
			return nil
		},
	})
	runFn := func(_ context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		if len(messages) != 0 {
			t.Fatalf("resume messages = %v, want none", messageStrings(messages))
		}
		if token, _ := agent.GetOption(options, agent.WithContinuationToken); token != "inner" {
			t.Fatalf("provider continuation token = %q, want inner", token)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " upon"}}}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " a"}}}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " time"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		HistoryProvider:  historyProvider,
		ContextProviders: []agent.ContextProvider{contextProvider},
	})
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type:       agenttest.ContinuationTokenType,
		InnerToken: "inner",
		ResponseUpdates: []*agent.ResponseUpdate{
			{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "once"}}},
		},
	})

	_, err := a.Run(t.Context(), nil, agent.WithSession(agenttest.CreateSession()), agent.WithContinuationToken(token), agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if historyStoreCalls != 1 || contextStoreCalls != 1 {
		t.Fatalf("history/context store calls = %d/%d, want 1/1", historyStoreCalls, contextStoreCalls)
	}
	if got := messageStrings(historyResponseMessages); !slices.Equal(got, []string{"once upon a time"}) {
		t.Fatalf("history response messages = %v, want [once upon a time]", got)
	}
	if got := messageStrings(contextResponseMessages); !slices.Equal(got, []string{"once upon a time"}) {
		t.Fatalf("context response messages = %v, want [once upon a time]", got)
	}
	if historyStoreSawContinuationToken {
		t.Fatal("history provider Store saw continuation token option")
	}
	if contextStoreSawContinuationToken {
		t.Fatal("context provider Store saw continuation token option")
	}
}

func TestAgent_Run_ContinuationToken_PersistsSavedInputMessages(t *testing.T) {
	var historyRequestMessages []*message.Message
	var contextRequestMessages []*message.Message
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			historyRequestMessages = invoked.RequestMessages
			return nil
		},
	})
	contextProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			contextRequestMessages = invoked.RequestMessages
			return nil
		},
	})
	runFn := func(_ context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		if len(messages) != 0 {
			t.Fatalf("resume messages = %v, want none", messageStrings(messages))
		}
		if token, _ := agent.GetOption(options, agent.WithContinuationToken); token != "inner" {
			t.Fatalf("provider continuation token = %q, want inner", token)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		HistoryProvider:  historyProvider,
		ContextProviders: []agent.ContextProvider{contextProvider},
	})
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type:          agenttest.ContinuationTokenType,
		InnerToken:    "inner",
		InputMessages: []*message.Message{message.NewText("Tell me a story")},
	})

	_, err := a.Run(t.Context(), nil, agent.WithSession(agenttest.CreateSession()), agent.WithContinuationToken(token), agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := messageStrings(historyRequestMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("history request messages = %v, want [Tell me a story]", got)
	}
	if got := messageStrings(contextRequestMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("context request messages = %v, want [Tell me a story]", got)
	}
}

func TestAgent_Run_UsesConfigContextProvider(t *testing.T) {
	provideCalled := false
	runCalled := false
	contextProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx-provider",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return nil, nil, nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{contextProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !runCalled {
		t.Fatal("expected run function to be called")
	}
	if !provideCalled {
		t.Fatal("expected context provider to be used")
	}
}

func TestAgent_Run_PassesReadOnlyContextSlicesWithoutCloning(t *testing.T) {
	messages := []*message.Message{message.NewText("request")}
	options := []agent.Option{agent.WithSession(agenttest.CreateSession())}
	var invokingMessages []*message.Message
	contextProvider := contextProviderFunc{
		invoking: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			invokingMessages = invoking.Messages
			if &invoking.Options[0] != &options[0] {
				t.Error("expected InvokingContext to share the run option slice")
			}
			return invoking.Messages, invoking.Options, nil
		},
		invoked: func(_ context.Context, invoked agent.InvokedContext) error {
			if &invoked.RequestMessages[0] != &invokingMessages[0] {
				t.Error("expected InvokedContext to share the invocation message slice")
			}
			return nil
		},
	}
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:               "test-agent",
		ContextProviders: []agent.ContextProvider{contextProvider},
	})

	if _, err := a.Run(t.Context(), messages, options...).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_SkipsAutoCreatedSession(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"second"}) {
							t.Fatalf("second turn messages = %v, want [second]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ID: "test-agent", Name: "test-agent"})

	if _, err := a.RunText(t.Context(), "first").Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second").Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_SkipsServiceManagedSession(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{ConversationID: new("server-managed"), Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"second"}) {
							t.Fatalf("second turn messages = %v, want [second]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{ConversationID: new("server-managed"), Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()
	session.SetServiceID("server-managed")

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_SkipsWhenSessionBecomesServiceManaged(t *testing.T) {
	turn := 0
	runFn := func(_ context.Context, msgs []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		turn++
		want := []string{"first"}
		if turn == 2 {
			want = []string{"second"}
		}
		if got := messageStrings(msgs); !slices.Equal(got, want) {
			t.Fatalf("turn %d messages = %v, want %v", turn, got, want)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{ConversationID: new("server-managed"), Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_UsesExplicitLocalSession(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "one", "second"}) {
							t.Fatalf("second turn messages = %v, want [first one second]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_RunsWithContextProviders(t *testing.T) {
	contextProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return []*message.Message{message.NewText("context")}, nil, nil
		},
	})
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "context"}) {
							t.Fatalf("first turn messages = %v, want [first context]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "context", "one", "second", "context"}) {
							t.Fatalf("second turn messages = %v, want [first context one second context]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		ContextProviders: []agent.ContextProvider{contextProvider},
	})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_UsesConfigHistoryProvider(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "one", "second"}) {
							t.Fatalf("second turn messages = %v, want [first one second]", got)
						}
						if messages[0].Source.ID != "in-memory" || messages[1].Source.ID != "in-memory" || messages[2].Source.ID != "" {
							t.Fatalf("unexpected source IDs: [%q %q %q]", messages[0].Source.ID, messages[1].Source.ID, messages[2].Source.ID)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{
		ID:              "test-agent",
		Name:            "test-agent",
		HistoryProvider: agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{}),
	})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_HistoryProvider_ConflictsWhenSessionHasServiceID(t *testing.T) {
	provideCalled := false
	storeCalled := false
	var capturedMessages []*message.Message
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			provideCalled = true
			return []*message.Message{message.NewText("history")}, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			storeCalled = true
			return nil
		},
	})
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{ConversationID: new("server-managed"), Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent", HistoryProvider: historyProvider})
	session := agenttest.CreateSession()
	session.SetServiceID("server-managed")

	_, err := a.RunText(t.Context(), "input", agent.WithSession(session)).Collect()
	if err == nil || !strings.Contains(err.Error(), "HistoryProvider") {
		t.Fatalf("error = %v, want a history provider conflict", err)
	}
	if provideCalled || storeCalled {
		t.Fatal("expected configured history provider to be skipped even though the returned ID conflicts")
	}
	if got := messageStrings(capturedMessages); !slices.Equal(got, []string{"input"}) {
		t.Fatalf("messages = %v, want [input]", got)
	}
}

func TestAgent_Run_ConversationID(t *testing.T) {
	for _, tc := range []struct {
		name             string
		initialID        string
		responseID       string
		wantID           string
		wantMissingIDErr bool
	}{
		{name: "new service session", responseID: "conversation-1", wantID: "conversation-1"},
		{name: "existing service session advances", initialID: "conversation-1", responseID: "conversation-2", wantID: "conversation-2"},
		{name: "existing service session gets no ID", initialID: "conversation-1", wantID: "conversation-1", wantMissingIDErr: true},
		{name: "stateless provider has no session ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &agent.Session{}
			session.SetServiceID(tc.initialID)
			a := agent.New(agent.ProviderConfig{
				Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						update := &agent.ResponseUpdate{
							ResponseID: "response-1", Role: message.RoleAssistant,
							Contents: message.Contents{&message.TextContent{Text: "done"}},
						}
						if tc.responseID != "" {
							update.ConversationID = new(tc.responseID)
						}
						yield(update, nil)
					}
				},
			}, agent.Config{})
			response, err := a.RunText(t.Context(), "start", agent.WithSession(session)).Collect()
			if tc.wantMissingIDErr {
				if err == nil || !strings.Contains(err.Error(), "did not return a valid conversation ID") {
					t.Fatalf("error = %v, want missing conversation ID error", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if response.String() != "done" {
				t.Fatalf("response = %q, want done", response.String())
			}
			if got := session.ServiceID(); got != tc.wantID {
				t.Errorf("session ID = %q, want %q", got, tc.wantID)
			}
		})
	}
}

func TestAgent_Run_ServiceIDOptionsMatchSession(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sessionID string
		optionID  string
		wantID    string
		wantError bool
	}{
		{name: "session ID used by provider", sessionID: "conversation-1", wantID: "conversation-1"},
		{name: "matching IDs", sessionID: "conversation-1", optionID: "conversation-1", wantID: "conversation-1"},
		{name: "run ID stays in options until returned", optionID: "conversation-1", wantID: "conversation-1"},
		{name: "conflicting IDs fail before provider", sessionID: "conversation-1", optionID: "conversation-2", wantID: "conversation-1", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &agent.Session{}
			session.SetServiceID(tc.sessionID)
			called := false
			a := agent.New(agent.ProviderConfig{Run: func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					called = true
					if got := session.ServiceID(); got != tc.sessionID {
						t.Errorf("session changed before the provider returned: %q, want %q", got, tc.sessionID)
					}
					id, ok := agent.GetOption(opts, agent.WithServiceID)
					if !ok || id != tc.wantID {
						t.Errorf("provider service ID = %q (present=%t), want %q", id, ok, tc.wantID)
					}
					yield(&agent.ResponseUpdate{ConversationID: new(tc.wantID)}, nil)
				}
			}}, agent.Config{})
			opts := []agent.Option{agent.WithSession(session)}
			if tc.optionID != "" {
				opts = append(opts, agent.WithServiceID(tc.optionID))
			}
			response, err := a.RunText(t.Context(), "start", opts...).Collect()
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "differs") || called {
					t.Fatalf("error = %v, provider called = %t; want an early ID conflict", err, called)
				}
			} else if err != nil || !called || response == nil {
				t.Fatalf("error = %v, provider called = %t, response = %#v", err, called, response)
			}
			if session.ServiceID() != tc.wantID {
				t.Errorf("session ID = %q, want %q", session.ServiceID(), tc.wantID)
			}
		})
	}
}

func TestAgent_Run_RequestIDDoesNotCommitSession(t *testing.T) {
	providerErr := errors.New("provider failed")
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "stateless response", true: "provider failure"}[fail], func(t *testing.T) {
			session := &agent.Session{}
			called := false
			a := agent.New(agent.ProviderConfig{Run: func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					called = true
					id, _ := agent.GetOption(opts, agent.WithServiceID)
					if id != "request-id" || session.ServiceID() != "" {
						t.Fatalf("request ID = %q, session ID = %q; request state leaked into session", id, session.ServiceID())
					}
					if fail {
						yield(nil, providerErr)
						return
					}
					yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
				}
			}}, agent.Config{})
			stream := a.RunText(t.Context(), "hello", agent.WithSession(session), agent.WithServiceID("request-id"))
			if session.ServiceID() != "" || called {
				t.Fatal("preparing a run changed the session or invoked the provider")
			}
			_, err := stream.Collect()
			if fail && !errors.Is(err, providerErr) || !fail && err != nil {
				t.Fatalf("run error = %v", err)
			}
			if session.ServiceID() != "" {
				t.Errorf("request ID committed without a returned conversation ID: %q", session.ServiceID())
			}
		})
	}
}

func TestAgent_Run_PreservesSparseConversationID(t *testing.T) {
	session := &agent.Session{}
	session.SetServiceID("conversation-old")
	a := agent.New(agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{ResponseID: "response-1", ConversationID: new("conversation-new")}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Contents: message.Contents{&message.UsageContent{Details: message.UsageDetails{TotalTokenCount: 2}}}}, nil)
		}
	}}, agent.Config{})
	response, err := a.RunText(t.Context(), "hello", agent.WithSession(session), agent.Stream(true)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response.ConversationID == nil || *response.ConversationID != "conversation-new" || session.ServiceID() != "conversation-new" {
		t.Fatalf("response/session IDs = %v/%q, want conversation-new", response.ConversationID, session.ServiceID())
	}
}

func TestAgent_Run_ResponseConversationIDConflictsWithConfiguredHistory(t *testing.T) {
	var provided, stored bool
	history := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			provided = true
			return nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			stored = true
			return nil
		},
	})
	a := agent.New(agent.ProviderConfig{
		Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{ResponseID: "response-1", ConversationID: new("conversation-1")}, nil)
			}
		},
	}, agent.Config{
		HistoryProvider:                history,
		ThrowOnHistoryProviderConflict: new(true),
		ClearOnHistoryProviderConflict: new(true),
	})
	session := &agent.Session{}
	_, err := a.RunText(t.Context(), "start", agent.WithSession(session)).Collect()
	if err == nil || err.Error() != "only Session.ServiceID or HistoryProvider may be used, but not both; the service returned an ID indicating service-managed history while the agent has a HistoryProvider configured" {
		t.Fatalf("error = %v, want history provider conflict", err)
	}
	if !provided || stored {
		t.Errorf("history provider was invoked/stored = %t/%t, want true/false", provided, stored)
	}
	if got := session.ServiceID(); got != "" {
		t.Errorf("session ID = %q, want empty after conflict", got)
	}
}

func TestAgent_Run_StreamingPreservesReportedConversationID(t *testing.T) {
	var calls int
	fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
		return "found", nil
	})
	a := agent.New(agent.ProviderConfig{
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
		Run: func(_ context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				calls++
				if calls == 1 {
					yield(&agent.ResponseUpdate{
						ConversationID: new("conversation-1"), Role: message.RoleAssistant,
						Contents: message.Contents{&message.FunctionCallContent{CallID: "call-1", Name: "lookup", Arguments: `{}`}},
					}, nil)
					return
				}
				if len(messages) != 1 || messages[0].Role != message.RoleTool {
					t.Fatalf("second provider call messages = %+v, want only a tool result", messages)
				}
				id, _ := agent.GetOption(opts, agent.WithServiceID)
				if id != "conversation-1" {
					t.Errorf("second provider call service ID = %q, want conversation-1", id)
				}
				yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
			}
		},
	}, agent.Config{Tools: []tool.Tool{fn}})
	session := &agent.Session{}
	response, err := a.RunText(t.Context(), "start", agent.WithSession(session), agent.Stream(true)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || response.String() != "done" || response.ConversationID == nil || *response.ConversationID != "conversation-1" || session.ServiceID() != "conversation-1" {
		t.Fatalf("provider calls = %d, response = %q, conversation = %v, session ID = %q; want the latest reported conversation ID", calls, response.String(), response.ConversationID, session.ServiceID())
	}
}

func TestAgent_Run_BufferingPreservesMessageProperties(t *testing.T) {
	for _, tc := range []struct {
		name      string
		autocall  bool
		injection bool
		history   bool
	}{
		{name: "no buffering"},
		{name: "automatic tools", autocall: true},
		{name: "message injection", injection: true},
		{name: "per-call history", history: true},
		{name: "combined", autocall: true, injection: true, history: true},
	} {
		for _, stream := range []bool{false, true} {
			for _, lastHasProperties := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/last-properties=%t", tc.name, stream, lastHasProperties), func(t *testing.T) {
					properties := []map[string]any{{"first-only": "first-value", "shared": "first"}, nil}
					if lastHasProperties {
						properties[1] = map[string]any{"second-only": "second-value", "shared": "second"}
					}
					wantResponseProperties := map[string]any{"response-only": "response-value", "shared": "response"}
					var calls int
					provider := agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
						return func(yield func(*agent.ResponseUpdate, error) bool) {
							calls++
							for i, props := range properties {
								update := &agent.ResponseUpdate{
									ResponseID: "response-1", ConversationID: new("conversation-1"),
									MessageID: fmt.Sprintf("message-%d", i+1), Role: message.RoleAssistant,
									AdditionalProperties: maps.Clone(props),
									Contents: message.Contents{
										&message.TextContent{Text: fmt.Sprintf("text-%d", i+1)},
										&message.UsageContent{Details: message.UsageDetails{TotalTokenCount: int64(i + 1)}},
									},
								}
								if i == len(properties)-1 {
									update.FinishReason = "stop"
								}
								if !yield(update, nil) {
									return
								}
							}
							yield(&agent.ResponseUpdate{
								AdditionalProperties: maps.Clone(wantResponseProperties),
								ConversationID:       new("conversation-1"),
								ContinuationToken:    "inner-token",
							}, nil)
						}
					}}
					if tc.autocall {
						provider.Middlewares = []agent.Middleware{toolautocall.New(toolautocall.Config{})}
					}
					config := agent.Config{RequirePerServiceCallHistoryPersistence: tc.history}
					if tc.injection {
						config.MessageInjector = &agent.MessageInjector{}
					}
					a := agent.New(provider, config)
					response, err := a.RunText(t.Context(), "start", agent.WithSession(&agent.Session{}), agent.Stream(stream), agent.AllowBackgroundResponses(true)).Collect()
					if err != nil {
						t.Fatal(err)
					}
					if calls != 1 || len(response.Messages) != len(properties) {
						t.Fatalf("provider calls=%d messages=%d, want 1 and 2", calls, len(response.Messages))
					}
					for i, msg := range response.Messages {
						if !maps.Equal(msg.AdditionalProperties, properties[i]) {
							t.Errorf("message %d properties=%v, want %v", i+1, msg.AdditionalProperties, properties[i])
						}
						if msg.ID != fmt.Sprintf("message-%d", i+1) || msg.String() != fmt.Sprintf("text-%d", i+1) || msg.Usage().TotalTokenCount != int64(i+1) {
							t.Errorf("message %d lost its ID, text, or usage: %+v", i+1, msg)
						}
					}
					if !maps.Equal(response.AdditionalProperties, wantResponseProperties) {
						t.Errorf("response properties=%v, want %v", response.AdditionalProperties, wantResponseProperties)
					}
					if response.ID != "response-1" || response.ConversationID == nil || *response.ConversationID != "conversation-1" || response.FinishReason != "stop" {
						t.Errorf("buffering lost response metadata: %+v", response)
					}
					if token := agenttest.DecodeContinuationToken(t, response.ContinuationToken); token.InnerToken != "inner-token" {
						t.Errorf("continuation token lost its provider value: %q", token.InnerToken)
					}
				})
			}
		}
	}
}

func TestAgent_Run_NonStreamingUsesFinalToolResponseConversationID(t *testing.T) {
	for _, tc := range []struct {
		name             string
		initialID        string
		finalID          string
		wantMissingIDErr bool
	}{
		{name: "final response is stateless"},
		{name: "final response is stored", finalID: "conversation-final"},
		{name: "existing session requires a final ID", initialID: "conversation-initial", wantMissingIDErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
				return "found", nil
			})
			a := agent.New(agent.ProviderConfig{
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
				Run: func(_ context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						calls++
						if calls == 1 {
							yield(&agent.ResponseUpdate{
								ResponseID: "response-1", ConversationID: new("conversation-intermediate"), Role: message.RoleAssistant,
								Contents: message.Contents{&message.FunctionCallContent{CallID: "call-1", Name: "lookup", Arguments: `{}`}},
							}, nil)
							return
						}
						if len(messages) != 1 || messages[0].Role != message.RoleTool {
							t.Fatalf("second provider call messages = %+v, want only a tool result", messages)
						}
						if id, _ := agent.GetOption(opts, agent.WithServiceID); id != "conversation-intermediate" {
							t.Errorf("second provider call ID = %q, want conversation-intermediate", id)
						}
						update := &agent.ResponseUpdate{
							ResponseID: "response-2", Role: message.RoleAssistant,
							Contents: message.Contents{&message.TextContent{Text: "done"}},
						}
						if tc.finalID != "" {
							update.ConversationID = new(tc.finalID)
						}
						yield(update, nil)
					}
				},
			}, agent.Config{Tools: []tool.Tool{fn}})
			session := &agent.Session{}
			session.SetServiceID(tc.initialID)
			response, err := a.RunText(t.Context(), "start", agent.WithSession(session)).Collect()
			if calls != 2 {
				t.Fatalf("provider calls = %d, want 2", calls)
			}
			if tc.wantMissingIDErr {
				if err == nil || !strings.Contains(err.Error(), "did not return a valid conversation ID") {
					t.Fatalf("error = %v, want missing conversation ID error", err)
				}
				if session.ServiceID() != tc.initialID {
					t.Errorf("failed run changed session ID to %q, want %q", session.ServiceID(), tc.initialID)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.finalID == "" {
				if response.ConversationID != nil {
					t.Errorf("final stateless response retained conversation ID %q", *response.ConversationID)
				}
			} else if response.ConversationID == nil || *response.ConversationID != tc.finalID {
				t.Errorf("conversation ID = %v, want %q", response.ConversationID, tc.finalID)
			}
			if response.ID != "response-2" || response.String() != "done" || len(response.Messages) != 3 {
				t.Errorf("response ID=%q text=%q messages=%d; want response-2, done, and all 3 messages", response.ID, response.String(), len(response.Messages))
			}
			if session.ServiceID() != tc.finalID {
				t.Errorf("session ID = %q, want %q", session.ServiceID(), tc.finalID)
			}
		})
	}
}

func TestAgent_Run_HistoryProvider_ThrowsWhenServiceIDReturnedByDefault(t *testing.T) {
	provideCalled := false
	storeCalled := false
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			provideCalled = true
			return nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			storeCalled = true
			return nil
		},
	})
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{ConversationID: new("server-managed"), Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent", HistoryProvider: historyProvider})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err == nil {
		t.Fatal("expected history provider conflict error")
	}
	if err.Error() != "only Session.ServiceID or HistoryProvider may be used, but not both; the service returned an ID indicating service-managed history while the agent has a HistoryProvider configured" {
		t.Fatalf("error = %q", err.Error())
	}
	if !provideCalled {
		t.Fatal("expected history provider to run before the service returned an ID")
	}
	if storeCalled {
		t.Fatal("expected history provider store to be skipped on conflict")
	}
}

func TestAgent_Run_HistoryProvider_ClearsWhenThrowDisabledAndClearEnabled(t *testing.T) {
	provideCalls := 0
	storeCalled := false
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			provideCalls++
			return nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			storeCalled = true
			return nil
		},
	})
	runCalls := 0
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalls++
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			update := &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}
			if runCalls == 1 {
				update.ConversationID = new("server-managed")
			}
			yield(update, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:                             "test-agent",
		Name:                           "test-agent",
		HistoryProvider:                historyProvider,
		ThrowOnHistoryProviderConflict: new(false),
	})

	if _, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected first run error: %v", err)
	}
	if storeCalled {
		t.Fatal("expected cleared history provider not to store conflict run")
	}
	if provideCalls != 1 {
		t.Fatalf("provide calls = %d, want 1", provideCalls)
	}
	if _, err := a.RunText(t.Context(), "next", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected second run error: %v", err)
	}
	if provideCalls != 1 {
		t.Fatalf("expected cleared history provider not to run again, got %d calls", provideCalls)
	}
}

func TestAgent_Run_HistoryProvider_KeepsReferenceButSkipsStoreWhenThrowAndClearDisabled(t *testing.T) {
	provideCalls := 0
	storeCalls := 0
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			provideCalls++
			return nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			storeCalls++
			return nil
		},
	})
	runCalls := 0
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalls++
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			update := &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}
			if runCalls == 1 {
				update.ConversationID = new("server-managed")
			}
			yield(update, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:                             "test-agent",
		Name:                           "test-agent",
		HistoryProvider:                historyProvider,
		ThrowOnHistoryProviderConflict: new(false),
		WarnOnHistoryProviderConflict:  new(false),
		ClearOnHistoryProviderConflict: new(false),
	})

	if _, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected first run error: %v", err)
	}
	if provideCalls != 1 || storeCalls != 0 {
		t.Fatalf("after first run provide/store = %d/%d, want 1/0", provideCalls, storeCalls)
	}
	if _, err := a.RunText(t.Context(), "next", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected second run error: %v", err)
	}
	if provideCalls != 2 || storeCalls != 1 {
		t.Fatalf("after second run provide/store = %d/%d, want 2/1", provideCalls, storeCalls)
	}
}

func TestAgent_Run_HistoryProvider_SkipsWithContinuationToken(t *testing.T) {
	provideCalled := false
	runCalled := false
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			provideCalled = true
			return nil, nil
		},
	})
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		if len(msgs) != 0 {
			t.Fatalf("expected no messages with continuation token run, got %d", len(msgs))
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent", HistoryProvider: historyProvider})

	token := agenttest.NewContinuationToken(t, "ct-1")
	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken(token)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !runCalled {
		t.Fatal("expected provider run function to be called")
	}
	if provideCalled {
		t.Fatal("expected history provider to be skipped with continuation token")
	}
}

func TestAgent_Run_UsesHistoryBeforeContextProviders(t *testing.T) {
	sequence := make([]string, 0, 5)
	var storedRequestMessages []*message.Message
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			sequence = append(sequence, "history-before")
			return []*message.Message{message.NewText("history")}, nil
		},
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			sequence = append(sequence, "history-after")
			storedRequestMessages = invoked.RequestMessages
			return nil
		},
	})
	contextProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "ctx",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			sequence = append(sequence, "context-before")
			return []*message.Message{message.NewText("context")}, nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			sequence = append(sequence, "context-after")
			return nil
		},
	})
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		sequence = append(sequence, "run")
		if got := messageStrings(msgs); !slices.Equal(got, []string{"history", "input", "context"}) {
			t.Fatalf("messages = %v, want [history input context]", got)
		}
		if msgs[0].Source.ID != "history" || msgs[1].Source.ID != "" || msgs[2].Source.ID != "ctx" {
			t.Fatalf("unexpected source IDs: [%q %q %q]", msgs[0].Source.ID, msgs[1].Source.ID, msgs[2].Source.ID)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		HistoryProvider:  historyProvider,
		ContextProviders: []agent.ContextProvider{contextProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"history-before", "context-before", "run", "history-after", "context-after"}
	if !slices.Equal(sequence, expected) {
		t.Fatalf("sequence = %v, want %v", sequence, expected)
	}
	if got := messageStrings(storedRequestMessages); !slices.Equal(got, []string{"input", "context"}) {
		t.Fatalf("stored request messages = %v, want [input context]", got)
	}
}

func TestAgent_Run_HistoryProvider_DoesNotStoreInstructions(t *testing.T) {
	var storedRequestMessages []*message.Message
	var capturedInstructions []string
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			storedRequestMessages = invoked.RequestMessages
			return nil
		},
	})
	runFn := func(_ context.Context, msgs []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedInstructions = slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
		if got := messageStrings(msgs); !slices.Equal(got, []string{"input"}) {
			t.Fatalf("messages = %v, want [input]", got)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:              "test-agent",
		Name:            "test-agent",
		HistoryProvider: historyProvider,
		RunOptions:      []agent.Option{agent.WithInstructions(" instructions ")},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(capturedInstructions, []string{"instructions"}) {
		t.Fatalf("instructions = %q, want instructions", capturedInstructions)
	}
	if got := messageStrings(storedRequestMessages); !slices.Equal(got, []string{"input"}) {
		t.Fatalf("stored request messages = %v, want [input]", got)
	}
}

func TestAgent_Run_HistoryProvider_SkipsStoreOnRunError(t *testing.T) {
	expected := errors.New("run failed")
	storeCalled := false
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			return nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			storeCalled = true
			return nil
		},
	})
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, expected)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:              "test-agent",
		Name:            "test-agent",
		HistoryProvider: historyProvider,
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v, want %v", err, expected)
	}
	if storeCalled {
		t.Fatal("expected history provider store to be skipped after run error")
	}
}

func TestAgent_Run_ContextProvider_PropagatesInvokingError(t *testing.T) {
	expected := errors.New("invoking failed")
	runCalled := false

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return nil, nil, expected
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
	if runCalled {
		t.Fatal("expected run function not to be called when invoking fails")
	}
}

func TestAgent_Run_ContextProvider_RunsWithAutoCreatedSession(t *testing.T) {
	provideCalled := false
	runCalled := false

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return nil, nil, nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !runCalled {
		t.Fatal("expected run function to be called")
	}
	if !provideCalled {
		t.Fatal("expected history provider to run for auto-created session")
	}
}

func TestAgent_Run_ContextProvider_PersistsAfterSuccessfulRun(t *testing.T) {
	historyMessage := message.NewText("history")
	requestMessage := message.NewText("input")

	var capturedMessages []*message.Message
	var storedRequest []*message.Message
	var storedResponse []*message.Message
	storeCalled := false

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return []*message.Message{historyMessage}, nil, nil
		},
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			storeCalled = true
			storedRequest = invoked.RequestMessages
			storedResponse = invoked.ResponseMessages
			return nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "part1"}}}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "part2"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	_, err := a.RunMessage(t.Context(), requestMessage, agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(capturedMessages, requestMessage) {
		t.Fatal("expected request message to be included")
	}
	if !storeCalled {
		t.Fatal("expected store to be called")
	}
	if len(storedRequest) != 1 || storedRequest[0] != requestMessage {
		t.Fatal("expected default store filter to remove history-sourced request")
	}
	if len(storedResponse) == 0 {
		t.Fatal("expected response messages to be persisted")
	}
}

func TestAgent_Run_ContextProvider_PersistsWithoutResponseMessages(t *testing.T) {
	storeCalled := false
	storedResponseCount := -1

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			storeCalled = true
			storedResponseCount = len(invoked.ResponseMessages)
			return nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !storeCalled {
		t.Fatal("expected store to be called when no response messages are produced")
	}
	if storedResponseCount != 0 {
		t.Fatalf("expected zero response messages, got %d", storedResponseCount)
	}
}

func TestAgent_Run_ContextProvider_PropagatesStoreError(t *testing.T) {
	expected := errors.New("store failed")

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Store: func(context.Context, agent.InvokedContext) error {
			return expected
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestAgent_Run_ContextProvider_SkipsDefaultStoreOnRunError(t *testing.T) {
	runErr := errors.New("run failed")
	storeCalled := false

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			storeCalled = true
			return nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "before error"}}}, nil) {
				return
			}
			yield(nil, runErr)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, runErr) {
		t.Fatalf("expected %v, got %v", runErr, err)
	}
	if storeCalled {
		t.Fatal("expected store to be skipped when run stops on error")
	}
}

func TestAgent_Run_ContextProvider_InvokedReceivesRunError(t *testing.T) {
	runErr := errors.New("run failed")
	var invokedErr error
	contextProvider := contextProviderFunc{
		invoked: func(_ context.Context, invoked agent.InvokedContext) error {
			invokedErr = invoked.Err
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, runErr)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{contextProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, runErr) {
		t.Fatalf("expected %v, got %v", runErr, err)
	}
	if !errors.Is(invokedErr, runErr) {
		t.Fatalf("expected invoked error %v, got %v", runErr, invokedErr)
	}
}

func TestAgent_Run_ContextProvider_EarlyStopWithoutErrorDoesNotStore(t *testing.T) {
	storeCalled := false

	historyProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "history",
		Store: func(context.Context, agent.InvokedContext) error {
			storeCalled = true
			return nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "first"}}}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "second"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{historyProvider},
	})

	for _, err := range a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession()), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		break
	}

	if storeCalled {
		t.Fatal("store called after response stream was abandoned")
	}
}

func TestAgent_Run_UsesContextProvidersInOrder(t *testing.T) {
	sequence := make([]string, 0, 4)
	providerA := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "provider-a",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			sequence = append(sequence, "before-a")
			return []*message.Message{{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "a"}}}}, nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			sequence = append(sequence, "after-a")
			return nil
		},
	})
	providerB := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "provider-b",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			sequence = append(sequence, "before-b")
			return []*message.Message{{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "b"}}}}, nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			sequence = append(sequence, "after-b")
			return nil
		},
	})

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		if len(msgs) != 3 {
			t.Fatalf("expected providers to append 2 messages to request, got %d", len(msgs))
		}
		if got := []string{msgs[0].String(), msgs[1].String(), msgs[2].String()}; !slices.Equal(got, []string{"input", "a", "b"}) {
			t.Fatalf("expected providers to append messages in order, got %v", got)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []agent.ContextProvider{providerA, providerB},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"before-a", "before-b", "after-a", "after-b"}
	if !slices.Equal(sequence, expected) {
		t.Fatalf("expected sequence %v, got %v", expected, sequence)
	}
}

func TestAgent_Run_PipelineOrder_AgentHistoryContextProviderMiddlewareRun(t *testing.T) {
	contextTool := stubTool{name: "context-tool"}
	sequence := make([]string, 0, 5)
	var agentTools []tool.Tool
	var historyMessages []string
	var contextMessages []string
	var providerMessages []string
	var providerTools []tool.Tool
	var runMessages []string

	agentMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		sequence = append(sequence, "agent")
		agentTools = slices.Collect(agent.AllOptions(options, agent.WithTool))
		messages = append(slices.Clone(messages), message.NewText("agent"))
		return next(ctx, messages, options...)
	})
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			sequence = append(sequence, "history")
			historyMessages = messageStrings(invoking.Messages)
			return []*message.Message{message.NewText("history")}, nil
		},
	})
	contextProvider := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "context",
		Provide: func(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			sequence = append(sequence, "context")
			contextMessages = messageStrings(invoking.Messages)
			return []*message.Message{message.NewText("context")}, []agent.Option{agent.WithTool(contextTool)}, nil
		},
	})
	providerMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		sequence = append(sequence, "provider")
		providerMessages = messageStrings(messages)
		providerTools = slices.Collect(agent.AllOptions(options, agent.WithTool))
		return next(ctx, messages, options...)
	})
	runFn := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		sequence = append(sequence, "run")
		runMessages = messageStrings(messages)
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{
		Run:         runFn,
		Middlewares: []agent.Middleware{providerMiddleware},
	}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		Middlewares:      []agent.Middleware{agentMiddleware},
		HistoryProvider:  historyProvider,
		ContextProviders: []agent.ContextProvider{contextProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedSequence := []string{"agent", "history", "context", "provider", "run"}
	if !slices.Equal(sequence, expectedSequence) {
		t.Fatalf("expected sequence %v, got %v", expectedSequence, sequence)
	}
	if len(agentTools) != 0 {
		t.Fatalf("expected agent middleware not to see context provider tools, got %d", len(agentTools))
	}
	if got, want := toolNames(providerTools), []string{"context-tool"}; !slices.Equal(got, want) {
		t.Fatalf("expected provider middleware tools %v, got %v", want, got)
	}
	if got, want := historyMessages, []string{"input", "agent"}; !slices.Equal(got, want) {
		t.Fatalf("expected history messages %v, got %v", want, got)
	}
	if got, want := contextMessages, []string{"input"}; !slices.Equal(got, want) {
		t.Fatalf("expected context messages %v, got %v", want, got)
	}
	if got, want := providerMessages, []string{"history", "input", "agent", "context"}; !slices.Equal(got, want) {
		t.Fatalf("expected provider middleware messages %v, got %v", want, got)
	}
	if got, want := runMessages, []string{"history", "input", "agent", "context"}; !slices.Equal(got, want) {
		t.Fatalf("expected run messages %v, got %v", want, got)
	}
}

func TestAgent_Run_HistoryProvider_ConcurrentConflictClearIsRaceFree(t *testing.T) {
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(_ context.Context, _ agent.InvokingContext) ([]*message.Message, error) {
			return nil, nil
		},
		Store: func(context.Context, agent.InvokedContext) error {
			return nil
		},
	})
	// Every run promotes its own session to service-managed mid-run, which drives
	// the clear-on-conflict path that used to mutate the shared Agent field.
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{ConversationID: new("server-managed"), Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:                             "test-agent",
		Name:                           "test-agent",
		HistoryProvider:                historyProvider,
		ThrowOnHistoryProviderConflict: new(false),
		WarnOnHistoryProviderConflict:  new(false),
	})

	const goroutines = 64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			// Each goroutine drives a shared *Agent with its own session, so the
			// only shared state exercised is the agent's history-provider handling.
			if _, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
				t.Errorf("unexpected run error: %v", err)
			}
		}()
	}
	wg.Wait()
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}
