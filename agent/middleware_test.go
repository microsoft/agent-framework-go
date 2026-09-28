// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestProviderConfig_ManagesToolExecution_ToolOptions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invokes    bool
		middleware bool
	}{
		{name: "not opted in", middleware: true},
		{name: "no middleware", invokes: true},
		{name: "enabled", invokes: true, middleware: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
				return "found", nil
			})
			hosted := stubTool{name: "hosted"}
			schemaOnly := struct{ tool.SchemaTool }{fn}
			options := []agent.Option{
				agent.WithInstructions("first"), agent.WithTool(fn), agent.WithTool(hosted),
				agent.WithTool(nil), agent.WithTool(schemaOnly), agent.WithInstructions("last"),
			}
			cfg := agent.Config{}
			if tc.middleware {
				cfg.FunctionMiddlewares = []agent.FunctionInvocationMiddleware{func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
					result, err := next(ctx, invocation)
					if err != nil {
						return nil, err
					}
					return "wrapped " + result.(string), nil
				}}
			}
			run := func(ctx context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					tools := slices.Collect(agent.AllOptions(opts, agent.WithTool))
					if len(tools) != 3 || tools[1] != hosted || tools[2] != schemaOnly {
						t.Fatalf("non-function tools or tool order changed: %v", tools)
					}
					if (!tc.invokes || !tc.middleware) && tools[0] != fn {
						t.Error("tool changed without native invocation middleware")
					}
					if got := slices.Collect(agent.AllOptions(opts, agent.WithInstructions)); !slices.Equal(got, []string{"first", "last"}) {
						t.Errorf("instructions = %v, want [first last]", got)
					}
					result, err := tools[0].(tool.FuncTool).Call(ctx, "{}")
					if err != nil {
						yield(nil, err)
						return
					}
					yield(&agent.ResponseUpdate{Contents: []message.Content{&message.TextContent{Text: result.(string)}}}, nil)
				}
			}
			a := agent.New(agent.ProviderConfig{Run: run, ManagesToolExecution: tc.invokes}, cfg)
			response, err := a.RunText(t.Context(), "lookup", options...).Collect()
			if err != nil {
				t.Fatal(err)
			}
			want := "found"
			if tc.invokes && tc.middleware {
				want = "wrapped found"
			}
			if response.String() != want {
				t.Errorf("response = %q, want %q", response.String(), want)
			}
			if got := slices.Collect(agent.AllOptions(options, agent.WithTool)); !slices.Equal(got, []tool.Tool{fn, hosted, schemaOnly}) {
				t.Error("caller tools changed")
			}
		})
	}
}

func TestProviderConfig_ManagesToolExecution(t *testing.T) {
	toolFailure := errors.New("tool failed")
	for _, source := range []string{"configured", "run option", "context provider", "provider middleware"} {
		for _, tc := range []struct {
			name     string
			block    bool
			approval bool
			toolErr  error
		}{
			{name: "composition"},
			{name: "short circuit", block: true},
			{name: "error propagation", toolErr: toolFailure},
			{name: "approval required", approval: true},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				type traceKey struct{}
				type middlewareKey struct{}
				var order []string
				var callID string
				fn := functool.MustNew(functool.Config{Name: "lookup", Description: "Look up a value"}, func(ctx context.Context, args struct {
					Value string `json:"value"`
				},
				) (string, error) {
					order = append(order, "tool")
					if args.Value != "changed" || ctx.Value(middlewareKey{}) != callID {
						t.Error("tool did not receive the middleware's arguments and context")
					}
					return args.Value, tc.toolErr
				})
				if tc.approval {
					fn = tool.ApprovalRequiredFunc(fn)
				}
				first := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
					order = append(order, "first before")
					if invocation.Function != fn || invocation.CallID != callID || invocation.Arguments != `{"value":"original"}` {
						t.Errorf("unexpected invocation: %#v", invocation)
					}
					if ctx.Value(traceKey{}) != "trace" {
						t.Error("middleware lost the provider's context")
					}
					if tc.block {
						return "blocked", nil
					}
					invocation.Arguments = `{"value":"changed"}`
					result, err := next(context.WithValue(ctx, middlewareKey{}, invocation.CallID), invocation)
					order = append(order, "first after")
					if err != nil {
						return nil, err
					}
					return "wrapped " + result.(string), nil
				})
				second := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
					order = append(order, "second before")
					result, err := next(ctx, invocation)
					order = append(order, "second after")
					return result, err
				})
				toolOptions := []agent.Option{agent.WithTool(fn)}
				cfg := agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{first, nil, second}}
				var runOptions []agent.Option
				var providerMiddlewares []agent.Middleware
				switch source {
				case "configured":
					cfg.Tools = []tool.Tool{fn}
				case "run option":
					runOptions = toolOptions
				case "context provider":
					cfg.ContextProviders = []agent.ContextProvider{agent.NewContextProvider(agent.ContextProviderConfig{
						SourceID: "tools",
						Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
							return nil, toolOptions, nil
						},
					})}
				case "provider middleware":
					providerMiddlewares = []agent.Middleware{agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
						return next(ctx, messages, append(slices.Clone(options), toolOptions...)...)
					})}
				}
				run := func(ctx context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						var function tool.FuncTool
						for tl := range agent.AllOptions(options, agent.WithTool) {
							function, _ = tl.(tool.FuncTool)
						}
						if function == nil {
							t.Fatal("provider did not receive the function tool")
						}
						if function.Name() != fn.Name() || function.Description() != fn.Description() ||
							!reflect.DeepEqual(function.Schema(), fn.Schema()) || !reflect.DeepEqual(function.ReturnSchema(), fn.ReturnSchema()) {
							t.Error("wrapped tool metadata changed")
						}
						approval, ok := function.(tool.ApprovalRequiredTool)
						if got := ok && approval.ApprovalRequired(); got != tc.approval {
							t.Errorf("ApprovalRequired() = %v, want %v", got, tc.approval)
						}
						if len(order) != 0 {
							t.Fatal("middleware ran before the tool was invoked")
						}
						result, err := function.Call(agent.WithFuncCallID(ctx, callID), `{"value":"original"}`)
						if err != nil {
							yield(nil, err)
							return
						}
						yield(&agent.ResponseUpdate{
							Role:     message.RoleAssistant,
							Contents: []message.Content{&message.TextContent{Text: result.(string)}},
						}, nil)
					}
				}
				a := agent.New(agent.ProviderConfig{Run: run, Middlewares: providerMiddlewares, ManagesToolExecution: true}, cfg)
				ctx := context.WithValue(t.Context(), traceKey{}, "trace")
				ctx = agent.WithFuncCallID(ctx, "parent-call")
				for _, id := range []string{"call-1", ""} {
					callID = id
					order = nil
					response, err := a.RunText(ctx, "lookup", runOptions...).Collect()
					if !errors.Is(err, tc.toolErr) {
						t.Fatalf("RunText() error = %v, want %v", err, tc.toolErr)
					}
					wantOrder := []string{"first before", "second before", "tool", "second after", "first after"}
					wantResult := "wrapped changed"
					if tc.block {
						wantOrder = []string{"first before"}
						wantResult = "blocked"
					}
					if !slices.Equal(order, wantOrder) {
						t.Errorf("callback order = %v, want %v", order, wantOrder)
					}
					if err == nil && response.String() != wantResult {
						t.Errorf("response = %q, want %q", response.String(), wantResult)
					}
				}
				if original, _ := agent.GetOption(toolOptions, agent.WithTool); original != fn {
					t.Error("wrapping mutated the caller's options")
				}
			})
		}
	}
}

func TestProviderConfig_ManagesToolExecution_InsideProviderMiddleware(t *testing.T) {
	fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
		return "found", nil
	})
	functionMiddleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, invocation *agent.FunctionInvocationContext) (any, error) {
		if invocation.Function != fn {
			t.Error("middleware did not receive the original tool")
		}
		result, err := next(ctx, invocation)
		if err != nil {
			return nil, err
		}
		return "wrapped " + result.(string), nil
	})
	providerMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for range 2 {
				original, _ := agent.GetOption(options, agent.WithTool)
				if original != fn {
					t.Fatal("provider middleware did not receive the original tool")
				}
				for update, err := range next(ctx, messages, options...) {
					if !yield(update, err) {
						return
					}
				}
				if original, _ := agent.GetOption(options, agent.WithTool); original != fn {
					t.Fatal("wrapping mutated provider middleware's options")
				}
			}
		}
	})
	run := func(ctx context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			tl, _ := agent.GetOption(options, agent.WithTool)
			result, err := tl.(tool.FuncTool).Call(ctx, "{}")
			if err != nil {
				yield(nil, err)
				return
			}
			yield(&agent.ResponseUpdate{Contents: []message.Content{&message.TextContent{Text: result.(string)}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{
		Run: run, Middlewares: []agent.Middleware{providerMiddleware}, ManagesToolExecution: true,
	}, agent.Config{Tools: []tool.Tool{fn}, FunctionMiddlewares: []agent.FunctionInvocationMiddleware{functionMiddleware}})
	var results []string
	for update, err := range a.RunText(t.Context(), "lookup") {
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, update.String())
	}
	if !slices.Equal(results, []string{"wrapped found", "wrapped found"}) {
		t.Errorf("results = %v, want [wrapped found wrapped found]", results)
	}
}

func TestWithFuncCallID_PreservesContext(t *testing.T) {
	type contextKey struct{}
	parent, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "value"))
	defer cancel()
	ctx := agent.WithFuncCallID(parent, "call-1")
	if ctx.Value(contextKey{}) != "value" {
		t.Error("WithFuncCallID lost a context value")
	}
	if ctx.Done() != parent.Done() {
		t.Error("WithFuncCallID changed the cancellation channel")
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Errorf("context error = %v, want context.Canceled", ctx.Err())
	}
}
