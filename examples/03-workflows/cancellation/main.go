// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Workflow Cancellation",
	"This sample streams a long-running workflow, cancels it mid-flight, and inspects the run status.",
)

// tick carries the current iteration number around the Counter -> Printer loop.
type tick int

const (
	// cancelAfter is the number of streamed outputs to observe before cancelling.
	cancelAfter = 3
	// workBound is the number of ticks the workflow would emit if left alone.
	workBound = 100
	// workDelay simulates a slow, long-running step so the cancellation lands
	// while the workflow is still producing outputs rather than after it has
	// already finished. It keeps the sample fully offline.
	workDelay = 25 * time.Millisecond
)

func main() {
	ctx := context.Background()

	// A two-executor loop (Counter -> Printer -> Counter) that would emit
	// workBound outputs if left to run to completion. We cancel it well before
	// it reaches the bound.
	counter := newCounterExecutor("Counter", workBound)
	printer := newPrinterExecutor("Printer")

	wf, err := workflow.NewBuilder(counter).
		AddEdge(counter, printer).
		AddEdge(printer, counter).
		WithOutputFrom(printer).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.RunStreaming(ctx, wf, tick(1))
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	var (
		seen      int
		cancelled bool
	)
stream:
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			// Once cancellation is requested, the in-flight superstep unwinds
			// and surfaces context.Canceled. That is the expected, clean end of
			// a cancelled stream, so stop iterating rather than treating it as a
			// failure.
			if cancelled && errors.Is(err, context.Canceled) {
				break
			}
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			seen++
			demo.Assistantf("output %d: %v", seen, e.Output)
			if seen == cancelAfter {
				demo.Assistantf("Cancelling run after %d outputs", seen)
				if err := run.CancelRun(); err != nil {
					demo.Panic(err)
				}
				cancelled = true
			}
		case workflow.ErrorEvent:
			// A cancelled run reports context.Canceled as it tears down; treat
			// that as the normal terminal event instead of a hard error.
			if cancelled && errors.Is(e.Error, context.Canceled) {
				break stream
			}
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}

	// WatchStream has returned, so the run has unwound cleanly. The status is
	// informational only: it is published from a deferred setStatus on the
	// unwinding goroutine and may still be settling when we read it, so treat
	// the value as illustrative rather than something to branch on.
	status, err := run.GetStatus(ctx)
	if err != nil {
		demo.Panic(err)
	}
	demo.Assistantf("Stream terminated cleanly; final run status: %s", statusLabel(status))
}

// newCounterExecutor paces the loop: it simulates slow work, then forwards the
// current tick to the printer until the bound is reached.
func newCounterExecutor(id string, bound int) workflow.ExecutorBinding {
	return workflow.NewExecutor(id, func(ctx *workflow.Context, n tick) error {
		if int(n) > bound {
			return nil
		}
		// Simulate a slow, long-running step, but honor the executor context so a
		// cancellation lands immediately instead of after the delay elapses.
		select {
		case <-time.After(workDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
		return ctx.SendMessage("", n)
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[tick]())
			return rb, nil
		},
	}).Bind()
}

// newPrinterExecutor emits one workflow output per tick, then advances the loop.
func newPrinterExecutor(id string) workflow.ExecutorBinding {
	return workflow.NewExecutor(id, func(ctx *workflow.Context, n tick) error {
		if err := ctx.YieldOutput(fmt.Sprintf("tick %d", int(n))); err != nil {
			return err
		}
		return ctx.SendMessage("", n+1)
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[tick]())
			rb.YieldsOutputType(reflect.TypeFor[string]())
			return rb, nil
		},
	}).Bind()
}

func statusLabel(status inproc.RunStatus) string {
	switch status {
	case inproc.RunStatusNotStarted:
		return "not started"
	case inproc.RunStatusIdle:
		return "idle"
	case inproc.RunStatusPendingRequests:
		return "pending requests"
	case inproc.RunStatusEnded:
		return "ended"
	case inproc.RunStatusRunning:
		return "running"
	default:
		return fmt.Sprintf("unknown (%d)", int(status))
	}
}
