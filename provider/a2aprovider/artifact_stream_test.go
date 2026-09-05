// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func artifactText(evt *a2a.TaskArtifactUpdateEvent) string {
	if evt == nil || evt.Artifact == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range evt.Artifact.Parts {
		if part == nil {
			continue
		}
		sb.WriteString(part.Text())
	}
	return sb.String()
}

func TestArtifactStreamWriter_MissingMessageIDFallsBackToResponseID(t *testing.T) {
	w := newArtifactStreamWriter(testTaskInfoProvider{})

	events, err := w.Write(&agent.ResponseUpdate{
		ResponseID: "resp-1",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "reply"}},
	})
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("event count = %d, want 0 (should stay buffered until Complete)", len(events))
	}

	evt, err := w.Complete()
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if evt == nil {
		t.Fatal("expected a flushed artifact event")
	}
	if evt.Artifact.ID != "resp-1" {
		t.Fatalf("artifact id = %q, want %q", evt.Artifact.ID, "resp-1")
	}
}

func TestArtifactStreamWriter_WriteFlushesBufferedArtifactOnConversionError(t *testing.T) {
	w := newArtifactStreamWriter(testTaskInfoProvider{})

	events, err := w.Write(&agent.ResponseUpdate{
		MessageID: "msg-1",
		Role:      message.RoleAssistant,
		Contents:  message.Contents{&message.TextContent{Text: "partial"}},
	})
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("event count = %d, want 0 (should stay buffered)", len(events))
	}

	events, err = w.Write(&agent.ResponseUpdate{
		MessageID: "msg-1",
		Role:      message.RoleAssistant,
		Contents:  message.Contents{&message.DataContent{Data: "not-valid-base64!!!", MediaType: "application/octet-stream"}},
	})
	if err == nil {
		t.Fatal("expected a conversion error")
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1 (buffered artifact should be flushed before the error)", len(events))
	}
	if got := artifactText(events[0]); got != "partial" {
		t.Fatalf("artifact text = %q, want %q", got, "partial")
	}
	if !events[0].LastChunk {
		t.Fatal("expected flushed artifact to be closed as the last chunk")
	}
}
