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

func TestMessage_WithSource_ClonesWhenSourceChanges(t *testing.T) {
	original := message.NewText("Test content")
	original.Role = message.RoleAssistant
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
	if got.Role != message.RoleAssistant {
		t.Fatalf("WithSource role = %q, want assistant", got.Role)
	}
	if text := got.String(); text != "Test content" {
		t.Fatalf("WithSource text = %q, want Test content", text)
	}
	if original.Role != message.RoleAssistant {
		t.Fatalf("original role = %q, want assistant", original.Role)
	}
	if text := original.String(); text != "Test content" {
		t.Fatalf("original text = %q, want Test content", text)
	}
}

func TestMessage_WithSource_ClonesWhenFirstAssigned(t *testing.T) {
	original := message.NewText("Hello")
	source := message.Source{Type: message.SourceTypeExternal, ID: "TestSourceId"}

	got := original.WithSource(source)
	if got == original || got.Source != source {
		t.Fatalf("WithSource() = %+v, want a clone with source %+v", got, source)
	}
	if original.Source != (message.Source{}) {
		t.Fatalf("original source = %+v, want unchanged", original.Source)
	}
}

func TestMessage_WithSource_ClonesWhenSourceTypeChanges(t *testing.T) {
	original := message.NewText("Hello")
	original.Source = message.Source{Type: message.SourceTypeExternal, ID: "SourceId"}
	source := message.Source{Type: "context-provider", ID: "SourceId"}

	got := original.WithSource(source)
	if got == original || got.Source != source {
		t.Fatalf("WithSource() = %+v, want a clone with source %+v", got, source)
	}
	if original.Source.Type != message.SourceTypeExternal {
		t.Fatalf("original source = %+v, want external SourceId", original.Source)
	}
}

func TestMessage_WithSource_ClonesWhenSourceIDChanges(t *testing.T) {
	original := message.NewText("Hello")
	original.Source = message.Source{Type: message.SourceTypeExternal, ID: "OriginalId"}
	source := message.Source{Type: message.SourceTypeExternal, ID: "NewId"}

	got := original.WithSource(source)
	if got == original || got.Source != source {
		t.Fatalf("WithSource() = %+v, want a clone with source %+v", got, source)
	}
	if original.Source.ID != "OriginalId" {
		t.Fatalf("original source = %+v, want OriginalId", original.Source)
	}
}

func TestMessage_WithSource_ClonesWithSourceTypeAndNoID(t *testing.T) {
	original := message.NewText("Hello")
	source := message.Source{Type: "history-provider"}

	got := original.WithSource(source)
	if got == original || got.Source != source || got.Source.ID != "" {
		t.Fatalf("WithSource() = %+v, want a clone with history source and no ID", got)
	}
	if original.Source != (message.Source{}) {
		t.Fatalf("original source = %+v, want unchanged", original.Source)
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
