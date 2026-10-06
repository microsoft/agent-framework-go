// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestInputWaiter_WaitForInput_CompletesAfterSignal(t *testing.T) {
	w := newInputWaiter()
	defer w.close()

	w.signalInput()

	// Should complete immediately because input was already signaled.
	if err := w.waitForInput(t.Context()); err != nil {
		t.Fatalf("waitForInput: %v", err)
	}
}

func TestInputWaiter_WaitForInput_BlocksUntilSignaled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newInputWaiter()
		defer w.close()

		done := make(chan error, 1)
		go func() { done <- w.waitForInput(t.Context()) }()

		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("waitForInput returned before signal: err=%v", err)
		default:
		}

		w.signalInput()
		if err := <-done; err != nil {
			t.Fatalf("waitForInput: %v", err)
		}
	})
}

func TestInputWaiter_SignalInput_DoubleSignalIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newInputWaiter()
		defer w.close()

		// Double signal must leave exactly one pending signal.
		w.signalInput()
		w.signalInput()
		if err := w.waitForInput(t.Context()); err != nil {
			t.Fatalf("first waitForInput: %v", err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- w.waitForInput(ctx) }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("second wait consumed an extra signal: %v", err)
		default:
		}

		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("second waitForInput = %v, want context.Canceled", err)
		}
	})
}

func TestInputWaiter_WaitForInput_RespectsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newInputWaiter()
		defer w.close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- w.waitForInput(ctx) }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("waitForInput returned before cancellation: %v", err)
		default:
		}

		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}

		w.signalInput()
		if err := w.waitForInput(t.Context()); err != nil {
			t.Fatalf("waitForInput after canceled wait: %v", err)
		}
	})
}

func TestInputWaiter_WaitForInput_DoesNotCompleteWhenNotSignaled(t *testing.T) {
	w := newInputWaiter()
	defer w.close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := w.waitForInput(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestInputWaiter_WaitForInput_CanBeSignaledMultipleTimesSequentially(t *testing.T) {
	w := newInputWaiter()
	defer w.close()

	for i := range 3 {
		w.signalInput()
		if err := w.waitForInput(t.Context()); err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
	}
}

func TestInputWaiter_Close_ReleasesWaitersAndDropsSignals(t *testing.T) {
	w := newInputWaiter()

	// Signaling after close must not panic.
	w.close()
	w.signalInput()
}
