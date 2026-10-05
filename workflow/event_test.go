// Copyright (c) Microsoft. All rights reserved.

package workflow

import "testing"

func TestWorkflowWarningEvent(t *testing.T) {
	event := WorkflowWarningEvent{
		Message:       "workflow warning",
		SubWorkflowID: "child-workflow",
	}

	if got := event.Data(); got != event.Message {
		t.Fatalf("Data() = %v, want %q", got, event.Message)
	}
	if event.SubWorkflowID != "child-workflow" {
		t.Fatalf("SubWorkflowID = %q, want %q", event.SubWorkflowID, "child-workflow")
	}

	var warning Event = event
	switch warning.(type) {
	case WorkflowWarningEvent:
	default:
		t.Fatalf("warning type = %T, want WorkflowWarningEvent", warning)
	}
}
