// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
)

// WithTool(nil) yields an option whose Value() is a nil tool.Tool. Collecting
// options with AllOptions must skip it gracefully (as GetOption does) rather
// than panic — a panic here aborts tool collection in the auto-call middleware
// and in every provider.
func TestAllOptions_NilToolIsSkippedNotPanic(t *testing.T) {
	var count int
	for range agent.AllOptions([]agent.Option{agent.WithTool(nil)}, agent.WithTool) { // must not panic
		count++
	}
	if count != 0 {
		t.Fatalf("expected the nil tool option to be skipped, got %d yielded", count)
	}
}
