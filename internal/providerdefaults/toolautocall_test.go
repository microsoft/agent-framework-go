// Copyright (c) Microsoft. All rights reserved.

package providerdefaults

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestToolAutoCallMiddlewareConcurrentInvocationOptIn(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		assertToolCallsRemainSerial(t, agent.Config{})
	})

	t.Run("enabled when configured", func(t *testing.T) {
		assertToolCallsRunConcurrently(t, agent.Config{AllowConcurrentInvocations: true})
	})
}

func TestToolAutoCallMiddlewareDisabled(t *testing.T) {
	if got := ToolAutoCallMiddleware(agent.Config{DisableFuncAutoCall: true}); got != nil {
		t.Fatalf("ToolAutoCallMiddleware() = %v, want nil", got)
	}
}

func assertToolCallsRemainSerial(t *testing.T, cfg agent.Config) {
	t.Helper()

	var active atomic.Int32
	middleware := ToolAutoCallMiddleware(cfg)
	if middleware == nil {
		t.Fatal("expected middleware")
	}

	for _, err := range runToolCallPlan(t, middleware, functool.MustNew(functool.Config{Name: "Func"},
		func(ctx context.Context, args struct{ Arg string }) (string, error) {
			active.Add(1)
			time.Sleep(25 * time.Millisecond)
			if got := active.Load(); got != 1 {
				t.Fatalf("serial auto-call saw %d active calls, want 1", got)
			}
			active.Add(-1)
			return args.Arg + args.Arg, nil
		})) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func assertToolCallsRunConcurrently(t *testing.T, cfg agent.Config) {
	t.Helper()

	var remaining atomic.Int32
	remaining.Store(2)
	done := make(chan struct{})
	middleware := ToolAutoCallMiddleware(cfg)
	if middleware == nil {
		t.Fatal("expected middleware")
	}

	for _, err := range runToolCallPlan(t, middleware, functool.MustNew(functool.Config{Name: "Func"},
		func(ctx context.Context, args struct{ Arg string }) (string, error) {
			if remaining.Add(-1) == 0 {
				close(done)
			}
			<-done
			return args.Arg + args.Arg, nil
		})) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func runToolCallPlan(t *testing.T, middleware agent.Middleware, testTool tool.Tool) []error {
	t.Helper()

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call1", Name: "Func", Arguments: `{"Arg":"hello"}`},
					&message.FunctionCallContent{CallID: "call2", Name: "Func", Arguments: `{"Arg":"world"}`},
				},
			}).
			NewTurn().
			AddText("done").
			Build(),
	}

	var errs []error
	for _, err := range middleware.Run(
		runner.Run,
		t.Context(),
		[]*message.Message{message.NewText("hello")},
		agent.WithTool(testTool),
	) {
		errs = append(errs, err)
	}
	return errs
}
