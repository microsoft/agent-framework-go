// Copyright (c) Microsoft. All rights reserved.

package logger_test

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/middleware/logger"
)

func TestLogger_Run_LogsDebugMessage(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	mw := logger.New(logger.Config{Logger: log})

	// Create a simple next function
	nextCalled := false
	next := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		nextCalled = true
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{MessageID: "test-1"}, nil)
		}
	}

	// Run the middleware
	ctx := context.Background()
	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := mw.Run(next, ctx, messages)
	for range seq {
	}

	// Assert next was called
	if !nextCalled {
		t.Error("expected next function to be called")
	}

	// Assert logs were written
	output := buf.String()
	if !strings.Contains(output, "run invoked") {
		t.Errorf("expected log to contain 'run invoked', got: %s", output)
	}
	if !strings.Contains(output, "run completed") {
		t.Errorf("expected log to contain 'run completed', got: %s", output)
	}
}

func TestLogger_Run_LogsTraceWithDetails(t *testing.T) {
	// Create a logger with trace level enabled
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	mw := logger.New(logger.Config{Logger: log, SensitiveData: true})

	// Create a simple next function
	next := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{MessageID: "test-1"}, nil)
		}
	}

	// Run the middleware
	ctx := context.Background()
	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := mw.Run(next, ctx, messages)
	for range seq {
	}

	// Assert trace logs include details
	output := buf.String()
	if !strings.Contains(output, "run invoked") {
		t.Errorf("expected log to contain 'run invoked', got: %s", output)
	}
	if !strings.Contains(output, "messages") {
		t.Errorf("expected log to contain 'messages' field, got: %s", output)
	}
	if !strings.Contains(output, "run received update") {
		t.Errorf("expected log to contain 'run received update', got: %s", output)
	}
	if !strings.Contains(output, "run completed") {
		t.Errorf("expected log to contain 'run completed', got: %s", output)
	}
}

func TestLogger_Run_LogsErrors(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	mw := logger.New(logger.Config{Logger: log})

	// Create a next function that returns an error
	expectedError := errors.New("test error")
	next := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, expectedError)
		}
	}

	// Run the middleware
	ctx := context.Background()
	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := mw.Run(next, ctx, messages)
	var receivedError error
	for _, err := range seq {
		if err != nil {
			receivedError = err
		}
	}

	// Assert error was received
	if receivedError != expectedError {
		t.Errorf("expected error %v, got %v", expectedError, receivedError)
	}

	// Assert error was logged
	output := buf.String()
	if !strings.Contains(output, "run failed") {
		t.Errorf("expected log to contain 'run failed', got: %s", output)
	}
	if !strings.Contains(output, "test error") {
		t.Errorf("expected log to contain error message 'test error', got: %s", output)
	}
}

func TestLogger_Run_HandlesMultipleUpdates(t *testing.T) {
	// Create a logger with trace level enabled
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	mw := logger.New(logger.Config{Logger: log, SensitiveData: true})

	// Create a next function that yields multiple updates
	next := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{MessageID: "test-1"}, nil) {
				return
			}
			if !yield(&message.ResponseUpdate{MessageID: "test-2"}, nil) {
				return
			}
			yield(&message.ResponseUpdate{MessageID: "test-3"}, nil)
		}
	}

	// Run the middleware
	ctx := context.Background()
	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := mw.Run(next, ctx, messages)
	updateCount := 0
	for range seq {
		updateCount++
	}

	// Assert all updates were received
	if updateCount != 3 {
		t.Errorf("expected 3 updates, got %d", updateCount)
	}

	// Assert trace logs show multiple updates
	output := buf.String()
	updateLogs := strings.Count(output, "run received update")
	if updateLogs != 3 {
		t.Errorf("expected 3 'run received update' logs, got %d", updateLogs)
	}
}

func TestLogger_Run_EarlyTermination(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	mw := logger.New(logger.Config{Logger: log})

	// Create a next function that yields multiple updates
	yieldCount := 0
	next := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			for i := 0; i < 5; i++ {
				yieldCount++
				if !yield(&message.ResponseUpdate{MessageID: "test"}, nil) {
					return
				}
			}
		}
	}

	// Run the middleware but stop early
	ctx := context.Background()
	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := mw.Run(next, ctx, messages)
	consumedCount := 0
	for range seq {
		consumedCount++
		if consumedCount >= 2 {
			break
		}
	}

	// Assert only 2 updates were consumed
	if consumedCount != 2 {
		t.Errorf("expected to consume 2 updates, got %d", consumedCount)
	}

	// Assert that next function stopped after consumer stopped
	if yieldCount != 2 {
		t.Errorf("expected next to yield 2 times (stopped early), got %d", yieldCount)
	}
}

func TestLogger_Run_PropagatesContext(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	mw := logger.New(logger.Config{Logger: log})

	// Create a next function that checks context
	type contextKey string
	key := contextKey("test-key")
	var receivedCtx context.Context
	next := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		receivedCtx = ctx
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{MessageID: "test-1"}, nil)
		}
	}

	// Run the middleware with a context value
	ctx := context.WithValue(context.Background(), key, "test-value")
	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := mw.Run(next, ctx, messages)
	for range seq {
	}

	// Assert context was propagated
	if receivedCtx == nil {
		t.Fatal("context was not propagated")
	}
	if receivedCtx.Value(key) != "test-value" {
		t.Errorf("expected context value 'test-value', got %v", receivedCtx.Value(key))
	}
}

func TestLogger_Run_WorksInMiddlewareChain(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	loggerMw := logger.New(logger.Config{Logger: log})

	// Create another test middleware
	var order []string
	testMw := &testMiddleware{
		name: "test-mw",
		onRun: func(name string) {
			order = append(order, name)
		},
	}

	// Base function
	baseFn := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		order = append(order, "base")
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{MessageID: "test-1"}, nil)
		}
	}

	// Run the middleware chain
	ctx := context.Background()
	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := middleware.RunChain(ctx, baseFn, []middleware.Middleware{loggerMw, testMw}, messages)
	for range seq {
	}

	// Assert execution order
	expected := []string{"test-mw", "base"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d calls, got %d", len(expected), len(order))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("expected order[%d] to be %s, got %s", i, v, order[i])
		}
	}

	// Assert logs were written
	output := buf.String()
	if !strings.Contains(output, "run invoked") {
		t.Errorf("expected log to contain 'Run invoked', got: %s", output)
	}
	if !strings.Contains(output, "run completed") {
		t.Errorf("expected log to contain 'Run completed', got: %s", output)
	}
}

func TestLogger_Run_ContextCanceled(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create logger middleware
	mw := logger.New(logger.Config{Logger: log})

	// Create a next function that checks for context cancellation
	next := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			// Yield first update
			if !yield(&message.ResponseUpdate{MessageID: "test-1"}, nil) {
				return
			}
			// Check if context is canceled
			select {
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			default:
			}
			// Yield second update
			yield(&message.ResponseUpdate{MessageID: "test-2"}, nil)
		}
	}

	// Create a cancelable context and cancel it immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	messages := []*message.Message{
		message.New(&message.TextContent{Text: "test message"}),
	}

	seq := mw.Run(next, ctx, messages)
	var receivedError error
	updateCount := 0
	for _, err := range seq {
		if err != nil {
			receivedError = err
		} else {
			updateCount++
		}
	}

	// Assert we got the context canceled error
	if receivedError != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", receivedError)
	}

	// Assert logs were written
	output := buf.String()
	if !strings.Contains(output, "run invoked") {
		t.Errorf("expected log to contain 'run invoked', got: %s", output)
	}
	if !strings.Contains(output, "run canceled") {
		t.Errorf("expected log to contain 'run canceled', got: %s", output)
	}
	if !strings.Contains(output, "context canceled") {
		t.Errorf("expected log to contain 'context canceled', got: %s", output)
	}
}

// testMiddleware is a test implementation of Middleware
type testMiddleware struct {
	name  string
	onRun func(string)
}

func (tm *testMiddleware) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	tm.onRun(tm.name)
	return next(ctx, messages, options...)
}
