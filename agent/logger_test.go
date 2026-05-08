// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func TestAgent_RunLogs_WhenLoggerConfigured(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runCalled := false
	a := newRunLoggerTestAgent(log, false, false, func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return singleRunLoggerTestUpdate("ok")
	})

	for _, err := range a.RunText(context.Background(), "hello") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if !runCalled {
		t.Fatal("expected provider run to be called")
	}
	output := buf.String()
	if !strings.Contains(output, "run invoked") {
		t.Fatalf("expected run invoked log, got: %s", output)
	}
	if !strings.Contains(output, "run completed") {
		t.Fatalf("expected run completed log, got: %s", output)
	}
	if !strings.Contains(output, "agentID=test-agent") {
		t.Fatalf("expected agent ID in logs, got: %s", output)
	}
	if !strings.Contains(output, "agentName=test-agent") {
		t.Fatalf("expected agent name in logs, got: %s", output)
	}
}

func TestAgent_RunLogs_DisableRunLogs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := newRunLoggerTestAgent(log, false, true, func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return singleRunLoggerTestUpdate("ok")
	})

	for _, err := range a.RunText(context.Background(), "hello") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if output := buf.String(); strings.Contains(output, "run invoked") || strings.Contains(output, "run completed") {
		t.Fatalf("expected no run logs, got: %s", output)
	}
}

func TestAgent_RunLogs_SensitiveDataControlsPayloadLogs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := newRunLoggerTestAgent(log, true, false, func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return singleRunLoggerTestUpdate("ok")
	})

	for _, err := range a.RunText(context.Background(), "hello") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	output := buf.String()
	if !strings.Contains(output, "messages") {
		t.Fatalf("expected sensitive message payload log, got: %s", output)
	}
	if !strings.Contains(output, "run received update") {
		t.Fatalf("expected sensitive update payload log, got: %s", output)
	}
}

func TestAgent_RunLogs_LogsErrors(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	expectedErr := errors.New("test error")
	a := newRunLoggerTestAgent(log, false, false, func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, expectedErr)
		}
	})

	var runErr error
	for _, err := range a.RunText(context.Background(), "hello") {
		if err != nil {
			runErr = err
		}
	}

	if runErr != expectedErr {
		t.Fatalf("expected error %v, got %v", expectedErr, runErr)
	}
	output := buf.String()
	if !strings.Contains(output, "run failed") {
		t.Fatalf("expected run failed log, got: %s", output)
	}
	if !strings.Contains(output, "test error") {
		t.Fatalf("expected error message in log, got: %s", output)
	}
}

func TestAgent_RunLogs_HandlesMultipleUpdates(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := newRunLoggerTestAgent(log, true, false, func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{MessageID: "test-2"}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{MessageID: "test-3"}, nil)
		}
	})

	updateCount := 0
	for _, err := range a.RunText(context.Background(), "hello") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updateCount++
	}

	if updateCount != 3 {
		t.Fatalf("expected 3 updates, got %d", updateCount)
	}
	if updateLogs := strings.Count(buf.String(), "run received update"); updateLogs != 3 {
		t.Fatalf("expected 3 update logs, got %d: %s", updateLogs, buf.String())
	}
}

func TestAgent_RunLogs_EarlyTermination(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	yieldCount := 0
	a := newRunLoggerTestAgent(log, false, false, func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for range 5 {
				yieldCount++
				if !yield(&agent.ResponseUpdate{MessageID: "test"}, nil) {
					return
				}
			}
		}
	})

	consumedCount := 0
	for range a.RunText(context.Background(), "hello") {
		consumedCount++
		if consumedCount >= 2 {
			break
		}
	}

	if consumedCount != 2 {
		t.Fatalf("expected to consume 2 updates, got %d", consumedCount)
	}
	if yieldCount != 2 {
		t.Fatalf("expected provider to yield 2 times after early stop, got %d", yieldCount)
	}
}

func TestAgent_RunLogs_PropagatesContext(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	type contextKey string
	key := contextKey("test-key")
	var receivedCtx context.Context
	a := newRunLoggerTestAgent(log, false, false, func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		receivedCtx = ctx
		return singleRunLoggerTestUpdate("ok")
	})

	ctx := context.WithValue(context.Background(), key, "test-value")
	for _, err := range a.RunText(ctx, "hello") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if receivedCtx == nil {
		t.Fatal("context was not propagated")
	}
	if receivedCtx.Value(key) != "test-value" {
		t.Fatalf("expected context value test-value, got %v", receivedCtx.Value(key))
	}
}

func TestAgent_RunLogs_ContextCanceled(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := newRunLoggerTestAgent(log, false, false, func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil) {
				return
			}
			select {
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			default:
			}
			yield(&agent.ResponseUpdate{MessageID: "test-2"}, nil)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var receivedErr error
	updateCount := 0
	for _, err := range a.RunText(ctx, "hello") {
		if err != nil {
			receivedErr = err
		} else {
			updateCount++
		}
	}

	if receivedErr != context.Canceled {
		t.Fatalf("expected context.Canceled error, got %v", receivedErr)
	}
	if updateCount != 1 {
		t.Fatalf("expected 1 update before cancellation, got %d", updateCount)
	}
	output := buf.String()
	if !strings.Contains(output, "run canceled") {
		t.Fatalf("expected run canceled log, got: %s", output)
	}
	if !strings.Contains(output, "context canceled") {
		t.Fatalf("expected context canceled message, got: %s", output)
	}
}

func TestAgent_RunLogs_OutermostMiddleware(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var order []string
	mw := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		order = append(order, "user-middleware")
		return next(ctx, messages, opts...)
	})
	a := agent.New(agent.ProviderConfig{
		Run: func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			order = append(order, "provider")
			return singleRunLoggerTestUpdate("ok")
		},
	}, agent.Config{
		ID:          "test-agent",
		Name:        "test-agent",
		Logger:      log,
		Middlewares: []agent.Middleware{mw},
	})

	for _, err := range a.RunText(context.Background(), "hello") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(order) != 2 || order[0] != "user-middleware" || order[1] != "provider" {
		t.Fatalf("unexpected order: %v", order)
	}
	output := buf.String()
	if !strings.Contains(output, "run invoked") || !strings.Contains(output, "run completed") {
		t.Fatalf("expected automatic run logs around middleware chain, got: %s", output)
	}
}

func newRunLoggerTestAgent(log *slog.Logger, logSensitiveData bool, disableRunLogs bool, run agent.RunFunc) *agent.Agent {
	return agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		Logger:           log,
		LogSensitiveData: logSensitiveData,
		DisableRunLogs:   disableRunLogs,
	})
}

func singleRunLoggerTestUpdate(text string) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: text}}}, nil)
	}
}
