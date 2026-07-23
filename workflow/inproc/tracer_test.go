// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"slices"
	"testing"
)

func TestStepTracer_CompletionExecutorIDsAreUniqueAndSorted(t *testing.T) {
	tracer := &stepTracer{}
	tracer.TraceActivated("writer")
	tracer.TraceActivated("planner")
	tracer.TraceActivated("writer")
	tracer.TraceInstantiated("reviewer")
	tracer.TraceInstantiated("planner")
	tracer.TraceInstantiated("reviewer")

	event := tracer.Complete(false, false)

	if got, want := event.CompletionInfo.ActivatedExecutors, []string{"planner", "writer"}; !slices.Equal(got, want) {
		t.Fatalf("ActivatedExecutors = %v, want %v", got, want)
	}
	if got, want := event.CompletionInfo.InstantiatedExecutors, []string{"planner", "reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("InstantiatedExecutors = %v, want %v", got, want)
	}
}
