// Copyright (c) Microsoft. All rights reserved.

package workflowext

import (
	"context"

	"github.com/microsoft/agent-framework/go/workflow"
)

type StatefulExecutorOptions struct {
	workflow.ExecutorOptions
	StateKey          string
	ScopeName         string
	CrossRunShareable bool
}

type StatefulExecutor[T any] struct {
	Options             StatefulExecutorOptions
	InitialStateFactory func() T
	DefaultStateKey     string

	stateCache T
	cached     bool
}

func (e *StatefulExecutor[T]) CrossRunShareable() bool {
	return e.Options.CrossRunShareable
}

func (e *StatefulExecutor[T]) DefaultOptions() workflow.ExecutorOptions {
	return e.Options.ExecutorOptions
}

func (e *StatefulExecutor[T]) maybeCache(wctx workflow.Context, v T) {
	if !wctx.ConcurrentRunsEnabled() {
		e.cache(v)
	}
}

func (e *StatefulExecutor[T]) cache(v T) {
	e.cached = true
	e.stateCache = v
}

func (e *StatefulExecutor[T]) StateKey() string {
	if e.Options.StateKey != "" {
		return e.Options.StateKey
	}
	return "StatefulExecutor.State"
}

func (e *StatefulExecutor[T]) ReadState(ctx context.Context, wctx workflow.Context, skipCache bool) (T, error) {
	if !skipCache && e.cached {
		return e.stateCache, nil
	}
	state, err := wctx.ReadOrInitState(ctx, e.StateKey(), e.Options.ScopeName, func(ctx context.Context, key, scope string) (any, error) {
		return e.InitialStateFactory(), nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	e.maybeCache(wctx, state.(T))
	return state.(T), nil
}

func (e *StatefulExecutor[T]) QueueStateUpdate(ctx context.Context, wctx workflow.Context, state T) error {
	e.maybeCache(wctx, state)
	return wctx.QueueStateUpdate(ctx, e.StateKey(), e.Options.ScopeName, state)
}

func (e *StatefulExecutor[T]) InvokeWithState(ctx context.Context, wctx workflow.Context, skipCache bool, fn func(ctx context.Context, wctx workflow.Context, state T) (T, error)) error {
	if !skipCache && !wctx.ConcurrentRunsEnabled() {
		state := e.stateCache
		if !e.cached {
			state = e.InitialStateFactory()
		}
		newState, err := fn(ctx, wctx, state)
		if err != nil {
			return err
		}
		if err := wctx.QueueStateUpdate(ctx, e.StateKey(), e.Options.ScopeName, newState); err != nil {
			return err
		}
		e.cache(newState)
		return nil
	}
	state, err := wctx.ReadState(ctx, e.StateKey(), e.Options.ScopeName)
	if err != nil {
		return err
	}
	newState, err := fn(ctx, wctx, state.(T))
	if err != nil {
		return err
	}
	return wctx.QueueStateUpdate(ctx, e.StateKey(), e.Options.ScopeName, newState)
}

func (e *StatefulExecutor[T]) Reset() {
	e.cached = false
	var zero T
	e.stateCache = zero
}
