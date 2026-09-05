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

func TestMessage_SourceType_DefaultsToExternal(t *testing.T) {
	msg := message.NewText("hello")

	if got := msg.SourceType(); got != message.SourceTypeExternal {
		t.Fatalf("SourceType() = %q, want %q", got, message.SourceTypeExternal)
	}
}

func TestMessage_SourceTypeAndSourceID_ReturnExplicitSource(t *testing.T) {
	msg := message.NewText("hello")
	msg.Source = message.Source{Type: message.SourceType("context-provider"), ID: "ctx"}

	if got := msg.SourceType(); got != message.SourceType("context-provider") {
		t.Fatalf("SourceType() = %q, want %q", got, message.SourceType("context-provider"))
	}
	if got := msg.SourceID(); got != "ctx" {
		t.Fatalf("SourceID() = %q, want %q", got, "ctx")
	}
}

func TestMessage_WithSource_ClonesWhenSourceChanges(t *testing.T) {
	original := message.NewText("hello")
	original.AdditionalProperties = map[string]any{"k": "v"}

	got := original.WithSource(message.Source{Type: message.SourceType("history-provider"), ID: "history"})
	if got == nil {
		t.Fatal("expected sourced message")
	}
	if got == original {
		t.Fatal("expected WithSource to clone when source changes")
	}
	if got.Source != (message.Source{Type: message.SourceType("history-provider"), ID: "history"}) {
		t.Fatalf("WithSource source = %#v", got.Source)
	}
	if got.AdditionalProperties["k"] != "v" {
		t.Fatalf("expected cloned additional properties, got %v", got.AdditionalProperties["k"])
	}
	if original.Source != (message.Source{}) {
		t.Fatalf("expected original source to remain unchanged, got %#v", original.Source)
	}
}

func TestMessage_WithSource_ReturnsOriginalWhenUnchanged(t *testing.T) {
	original := message.NewText("hello")
	original.Source = message.Source{Type: message.SourceType("context-provider"), ID: "ctx"}

	got := original.WithSource(message.Source{Type: message.SourceType("context-provider"), ID: "ctx"})
	if got != original {
		t.Fatal("expected WithSource to return original message when source is unchanged")
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
