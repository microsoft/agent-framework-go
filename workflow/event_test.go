// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestOutputEvent_SingleTag(t *testing.T) {
	event := OutputEvent{Output: "hello", ExecutorID: "e1", Tags: []OutputTag{OutputTagIntermediate}}
	if event.ExecutorID != "e1" || !slices.Equal(event.Tags, []OutputTag{OutputTagIntermediate}) ||
		!event.HasTag(OutputTagIntermediate) || !event.IsIntermediate() {
		t.Fatalf("single-tag event = %#v", event)
	}
}

func TestOutputEvent_NoTags(t *testing.T) {
	event := OutputEvent{Output: "hello", ExecutorID: "e1"}
	if len(event.Tags) != 0 || event.IsIntermediate() {
		t.Fatalf("untagged event = %#v", event)
	}
}

func TestOutputEvent_MultipleTags(t *testing.T) {
	var custom OutputTag
	if err := json.Unmarshal([]byte(`"custom"`), &custom); err != nil {
		t.Fatal(err)
	}
	event := OutputEvent{Output: "hello", ExecutorID: "e1", Tags: []OutputTag{OutputTagIntermediate, custom}}
	if len(event.Tags) != 2 || !event.HasTag(OutputTagIntermediate) || !event.HasTag(custom) || !event.IsIntermediate() {
		t.Fatalf("multi-tag event = %#v", event)
	}
}

func TestOutputTag_KnownValue(t *testing.T) {
	if got := OutputTagIntermediate.Value(); got != "intermediate" {
		t.Fatalf("Intermediate.Value() = %q, want intermediate", got)
	}
}

func TestOutputTag_EqualityUsesValue(t *testing.T) {
	first, second := OutputTagIntermediate, OutputTagIntermediate
	if first != second {
		t.Fatal("intermediate tag must equal itself")
	}
	var rebuilt OutputTag
	if err := json.Unmarshal([]byte(`"intermediate"`), &rebuilt); err != nil {
		t.Fatal(err)
	}
	if rebuilt != OutputTagIntermediate {
		t.Fatalf("rebuilt tag = %q, want intermediate", rebuilt)
	}
}

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
