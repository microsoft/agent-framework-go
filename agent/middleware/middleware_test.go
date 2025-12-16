// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

func TestChain(t *testing.T) {
	t.Run("empty middleware chain", func(t *testing.T) {
		called := false
		fn := func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
			called = true
			return func(yield func(*message.ResponseUpdate, error) bool) {}
		}

		ctx := context.Background()
		seq := RunChain(ctx, fn, []Middleware{}, []*message.Message{})
		for range seq {
		}

		if !called {
			t.Error("expected base function to be called")
		}
	})

	t.Run("single middleware", func(t *testing.T) {
		order := []string{}

		fn := func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
			order = append(order, "base")
			return func(yield func(*message.ResponseUpdate, error) bool) {}
		}

		mw := &testMiddleware{
			name: "mw1",
			onRun: func(name string) {
				order = append(order, name)
			},
		}

		ctx := context.Background()
		seq := RunChain(ctx, fn, []Middleware{mw}, []*message.Message{})
		for range seq {
		}

		expected := []string{"mw1", "base"}
		if len(order) != len(expected) {
			t.Fatalf("expected %d calls, got %d", len(expected), len(order))
		}
		for i, v := range expected {
			if order[i] != v {
				t.Errorf("expected order[%d] to be %s, got %s", i, v, order[i])
			}
		}
	})

	t.Run("multiple middlewares", func(t *testing.T) {
		order := []string{}

		fn := func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
			order = append(order, "base")
			return func(yield func(*message.ResponseUpdate, error) bool) {}
		}

		mw1 := &testMiddleware{
			name: "mw1",
			onRun: func(name string) {
				order = append(order, name)
			},
		}
		mw2 := &testMiddleware{
			name: "mw2",
			onRun: func(name string) {
				order = append(order, name)
			},
		}
		mw3 := &testMiddleware{
			name: "mw3",
			onRun: func(name string) {
				order = append(order, name)
			},
		}

		ctx := context.Background()
		seq := RunChain(ctx, fn, []Middleware{mw1, mw2, mw3}, []*message.Message{})
		for range seq {
		}

		// Middlewares should be called in order: mw1 -> mw2 -> mw3 -> base
		expected := []string{"mw1", "mw2", "mw3", "base"}
		if len(order) != len(expected) {
			t.Fatalf("expected %d calls, got %d", len(expected), len(order))
		}
		for i, v := range expected {
			if order[i] != v {
				t.Errorf("expected order[%d] to be %s, got %s", i, v, order[i])
			}
		}
	})

	t.Run("middleware can modify options", func(t *testing.T) {
		receivedOpts := []agentopt.RunOption{}

		fn := func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
			receivedOpts = options
			return func(yield func(*message.ResponseUpdate, error) bool) {}
		}

		additionalOpt := &testOption{value: "injected"}
		mw := &testMiddleware{
			name:  "mw1",
			onRun: func(name string) {},
			modifyOpts: func(opts []agentopt.RunOption) []agentopt.RunOption {
				return append(opts, additionalOpt)
			},
		}

		initialOpt := &testOption{value: "initial"}
		ctx := context.Background()
		seq := RunChain(ctx, fn, []Middleware{mw}, []*message.Message{}, initialOpt)
		for range seq {
		}

		if len(receivedOpts) != 2 {
			t.Fatalf("expected 2 options, got %d", len(receivedOpts))
		}
		if receivedOpts[0].(*testOption).value != "initial" {
			t.Errorf("expected first option to be 'initial', got %s", receivedOpts[0].(*testOption).value)
		}
		if receivedOpts[1].(*testOption).value != "injected" {
			t.Errorf("expected second option to be 'injected', got %s", receivedOpts[1].(*testOption).value)
		}
	})

	t.Run("middleware can intercept and modify sequence", func(t *testing.T) {
		fn := func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				if !yield(&message.ResponseUpdate{}, nil) {
					return
				}
				yield(&message.ResponseUpdate{}, nil)
			}
		}

		mw := &interceptMiddleware{
			interceptCount: 1,
		}

		ctx := context.Background()
		seq := RunChain(ctx, fn, []Middleware{mw}, []*message.Message{})

		count := 0
		for range seq {
			count++
		}

		// Base produces 2 updates, middleware intercepts 1, so we should see 1
		if count != 1 {
			t.Errorf("expected 1 update, got %d", count)
		}
	})

	t.Run("context propagation", func(t *testing.T) {
		type contextKey string
		key := contextKey("test")

		var capturedCtx context.Context
		fn := func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
			capturedCtx = ctx
			return func(yield func(*message.ResponseUpdate, error) bool) {}
		}

		mw := &testMiddleware{
			name:  "mw1",
			onRun: func(name string) {},
		}

		ctx := context.WithValue(context.Background(), key, "value")
		seq := RunChain(ctx, fn, []Middleware{mw}, []*message.Message{})
		for range seq {
		}

		if capturedCtx == nil {
			t.Fatal("context was not propagated")
		}
		if capturedCtx.Value(key) != "value" {
			t.Error("context value was not propagated correctly")
		}
	})
}

// testMiddleware is a test implementation of Middleware
type testMiddleware struct {
	name       string
	onRun      func(string)
	modifyOpts func([]agentopt.RunOption) []agentopt.RunOption
}

func (tm *testMiddleware) Run(ctx context.Context, next RunFunc, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	tm.onRun(tm.name)

	opts := options
	if tm.modifyOpts != nil {
		opts = tm.modifyOpts(options)
	}

	return next(ctx, messages, opts...)
}

// interceptMiddleware intercepts and limits the number of updates
type interceptMiddleware struct {
	interceptCount int
}

func (im *interceptMiddleware) Run(ctx context.Context, next RunFunc, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		count := 0
		for update, err := range next(ctx, messages, options...) {
			if count >= im.interceptCount {
				return
			}
			if !yield(update, err) {
				return
			}
			count++
		}
	}
}

// testOption is a test implementation of agentopt.Option
type testOption struct {
	value string
}

func (to *testOption) RunOption() {}

func (to *testOption) Value() any {
	return to.value
}
