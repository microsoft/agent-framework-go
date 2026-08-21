// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestMessage_Clone_ClonesAdditionalProperties(t *testing.T) {
	original := &message.Message{
		AdditionalProperties: map[string]any{"k": "v"},
	}

	cloned := original.Clone()
	if cloned == nil {
		t.Fatal("expected cloned message")
	}
	if cloned.AdditionalProperties["k"] != "v" {
		t.Fatalf("expected cloned additional property value 'v', got %v", cloned.AdditionalProperties["k"])
	}

	cloned.AdditionalProperties["k"] = "changed"
	if original.AdditionalProperties["k"] != "v" {
		t.Fatalf("expected original additional properties to remain unchanged, got %v", original.AdditionalProperties["k"])
	}
}

func TestMessage_Clone_ClonesContentsSlice(t *testing.T) {
	content := &message.TextContent{Text: "original"}
	original := message.New(content)

	cloned := original.Clone()
	if cloned.Contents[0] != content {
		t.Fatal("expected content values to remain shallow-copied")
	}

	cloned.Contents[0] = &message.TextContent{Text: "replacement"}
	if original.Contents[0] != content {
		t.Fatal("expected replacing cloned contents not to modify the original message")
	}
}
