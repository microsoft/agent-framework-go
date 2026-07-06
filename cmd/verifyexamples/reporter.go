// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

type ConsoleReporter struct {
	w    io.Writer
	lock sync.Mutex
}

func NewConsoleReporter(w io.Writer) *ConsoleReporter {
	return &ConsoleReporter{w: w}
}

func (r *ConsoleReporter) WriteLineWithPrefix(exampleName string, message string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	_, _ = fmt.Fprintf(r.w, "[%s] %s\n", exampleName, message)
}

func (r *ConsoleReporter) PrintSummary(orderedResults []VerificationResult, skipped []SkippedExample, elapsed time.Duration) {
	passCount := 0
	failCount := 0
	for _, result := range orderedResults {
		if result.Passed {
			passCount++
		} else {
			failCount++
		}
	}

	_, _ = fmt.Fprintln(r.w)
	_, _ = fmt.Fprintln(r.w, "────────────────────────────────────────────────────────────")
	_, _ = fmt.Fprintln(r.w, "SUMMARY")
	for _, result := range orderedResults {
		marker := "✗"
		if result.Passed {
			marker = "✓"
		}
		_, _ = fmt.Fprintf(r.w, "  %s %s: %s\n", marker, result.ExampleName, result.Summary)
	}
	for _, skippedExample := range skipped {
		_, _ = fmt.Fprintf(r.w, "  ○ %s: Skipped — %s\n", skippedExample.Name, skippedExample.Reason)
	}

	_, _ = fmt.Fprintf(r.w, "\nResults: %d passed", passCount)
	if failCount > 0 {
		_, _ = fmt.Fprintf(r.w, ", %d failed", failCount)
	}
	if len(skipped) > 0 {
		_, _ = fmt.Fprintf(r.w, ", %d skipped", len(skipped))
	}
	_, _ = fmt.Fprintf(r.w, "\nElapsed: %02d:%02d:%02d\n", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
}
