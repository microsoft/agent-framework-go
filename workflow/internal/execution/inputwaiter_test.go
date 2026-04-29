// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInputWaiter_WaitForInput_CompletesAfterSignal(t *testing.T) {
	w := newInputWaiter()
	defer w.close()

	w.signalInput()

	// Should complete immediately because input was already signaled.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.waitForInput(ctx); err != nil {
		t.Fatalf("waitForInput: %v", err)
	}
}

func TestInputWaiter_WaitForInput_BlocksUntilSignaled(t *testing.T) {
	w := newInputWaiter()
	defer w.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.waitForInput(ctx) }()

	// Should still be blocked.
	select {
	case err := <-done:
		t.Fatalf("waitForInput returned before signal: err=%v", err)
	case <-time.After(50 * time.Millisecond):
	}

	w.signalInput()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForInput: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForInput did not return after signal")
	}
}

func TestInputWaiter_SignalInput_DoubleSignalIsIdempotent(t *testing.T) {
	w := newInputWaiter()
	defer w.close()

	// Double signal should not panic and should still leave exactly one
	// pending signal (binary semaphore behavior).
	w.signalInput()
	w.signalInput()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.waitForInput(ctx); err != nil {
		t.Fatalf("first waitForInput: %v", err)
	}

	// A second wait without another signal must block (and time out).
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	err := w.waitForInput(ctx2)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestInputWaiter_WaitForInput_RespectsCancellation(t *testing.T) {
	w := newInputWaiter()
	defer w.close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.waitForInput(ctx) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForInput did not return after cancellation")
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		w.signalInput()
		if err := w.waitForInput(ctx); err != nil {
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
