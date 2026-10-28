// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func TestSourceAttribution_Constructor_SetsSourceTypeAndSourceId(t *testing.T) {
	source := message.Source{Type: agent.SourceTypeContextProvider, ID: "MyProvider"}
	if source.Type != agent.SourceTypeContextProvider || source.ID != "MyProvider" {
		t.Fatalf("source = %+v", source)
	}
}

func TestSourceAttribution_Constructor_WithEmptySourceId_SetsEmptySourceId(t *testing.T) {
	source := message.Source{Type: agent.SourceTypeHistoryProvider}
	if source.Type != agent.SourceTypeHistoryProvider || source.ID != "" {
		t.Fatalf("source = %+v", source)
	}
}

func TestSourceAttribution_Default_HasDefaultSourceTypeAndEmptySourceId(t *testing.T) {
	var source message.Source
	if source.Type != message.SourceTypeExternal || source.ID != "" {
		t.Fatalf("source = %+v", source)
	}
}

func TestSourceAttribution_Equals_WithSameSourceTypeAndSourceId_ReturnsTrue(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	if first != second {
		t.Fatalf("%+v != %+v", first, second)
	}
}

func TestSourceAttribution_Equals_WithDifferentSourceType_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider1"}
	if first == second {
		t.Fatalf("%+v == %+v", first, second)
	}
}

func TestSourceAttribution_Equals_WithDifferentSourceId_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider2"}
	if first == second {
		t.Fatalf("%+v == %+v", first, second)
	}
}

func TestSourceAttribution_Equals_WithDifferentSourceTypeAndSourceId_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider2"}
	if first == second {
		t.Fatalf("%+v == %+v", first, second)
	}
}

func TestSourceAttribution_Equals_WithDifferentCaseSourceId_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider"}
	second := message.Source{Type: agent.SourceTypeContextProvider, ID: "provider"}
	if first == second {
		t.Fatalf("%+v == %+v", first, second)
	}
}

func TestSourceAttribution_Equals_BothDefaultValues_ReturnsTrue(t *testing.T) {
	var first, second message.Source
	if first != second {
		t.Fatalf("%+v != %+v", first, second)
	}
}

func TestSourceAttribution_Equals_WithBothEmptySourceIds_ReturnsTrue(t *testing.T) {
	first := message.Source{Type: message.SourceTypeExternal}
	second := message.Source{Type: message.SourceTypeExternal}
	if first != second {
		t.Fatalf("%+v != %+v", first, second)
	}
}

func TestSourceAttribution_Equals_WithOneEmptySourceId_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: message.SourceTypeExternal, ID: "Provider1"}
	second := message.Source{Type: message.SourceTypeExternal}
	if first == second {
		t.Fatalf("%+v == %+v", first, second)
	}
}

func TestSourceAttribution_ObjectEquals_WithEqualAttribution_ReturnsTrue(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider"}
	var second any = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider"}
	if first != second {
		t.Fatalf("%+v != %+v", first, second)
	}
}

func TestSourceAttribution_ObjectEquals_WithDifferentType_ReturnsFalse(t *testing.T) {
	source := message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider"}
	var other any = "NotAnAttribution"
	if source == other {
		t.Fatalf("%+v == %v", source, other)
	}
}

func TestSourceAttribution_ObjectEquals_WithBoxedDifferentAttribution_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider1"}
	var second any = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider2"}
	if first == second {
		t.Fatalf("%+v == %+v", first, second)
	}
}

func TestSourceAttribution_EqualityOperator_WithEqualValues_ReturnsTrue(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider"}
	second := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider"}
	if got := first == second; !got {
		t.Fatalf("%+v == %+v returned false", first, second)
	}
}

func TestSourceAttribution_EqualityOperator_WithDifferentValues_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider2"}
	if got := first == second; got {
		t.Fatalf("%+v == %+v returned true", first, second)
	}
}

func TestSourceAttribution_EqualityOperator_WithBothDefault_ReturnsTrue(t *testing.T) {
	var first, second message.Source
	if got := first == second; !got {
		t.Fatalf("%+v == %+v returned false", first, second)
	}
}

func TestSourceAttribution_EqualityOperator_WithDifferentSourceTypeOnly_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider"}
	second := message.Source{Type: message.SourceTypeExternal, ID: "Provider"}
	if got := first == second; got {
		t.Fatalf("%+v == %+v returned true", first, second)
	}
}

func TestSourceAttribution_EqualityOperator_WithDifferentSourceIdOnly_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider2"}
	if got := first == second; got {
		t.Fatalf("%+v == %+v returned true", first, second)
	}
}

func TestSourceAttribution_InequalityOperator_WithEqualValues_ReturnsFalse(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider"}
	second := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider"}
	if got := first != second; got {
		t.Fatalf("%+v != %+v returned true", first, second)
	}
}

func TestSourceAttribution_InequalityOperator_WithDifferentValues_ReturnsTrue(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeHistoryProvider, ID: "Provider2"}
	if got := first != second; !got {
		t.Fatalf("%+v != %+v returned false", first, second)
	}
}

func TestSourceAttribution_InequalityOperator_WithBothDefault_ReturnsFalse(t *testing.T) {
	var first, second message.Source
	if got := first != second; got {
		t.Fatalf("%+v != %+v returned true", first, second)
	}
}

func TestSourceAttribution_InequalityOperator_WithDifferentSourceTypeOnly_ReturnsTrue(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider"}
	second := message.Source{Type: message.SourceTypeExternal, ID: "Provider"}
	if got := first != second; !got {
		t.Fatalf("%+v != %+v returned false", first, second)
	}
}

func TestSourceAttribution_InequalityOperator_WithDifferentSourceIdOnly_ReturnsTrue(t *testing.T) {
	first := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider1"}
	second := message.Source{Type: agent.SourceTypeContextProvider, ID: "Provider2"}
	if got := first != second; !got {
		t.Fatalf("%+v != %+v returned false", first, second)
	}
}

func TestSourceAttribution_ToString_WithSourceId_ReturnsTypeColonId(t *testing.T) {
	source := message.Source{Type: agent.SourceTypeContextProvider, ID: "MyProvider"}
	if got := source.String(); got != string(agent.SourceTypeContextProvider)+":MyProvider" {
		t.Fatalf("String() = %q", got)
	}
}

func TestSourceAttribution_ToString_WithEmptySourceId_ReturnsTypeOnly(t *testing.T) {
	source := message.Source{Type: agent.SourceTypeHistoryProvider}
	if got := source.String(); got != string(agent.SourceTypeHistoryProvider) {
		t.Fatalf("String() = %q", got)
	}
}

func TestSourceAttribution_ToString_Default_ReturnsExternalOnly(t *testing.T) {
	var source message.Source
	if got := source.String(); got != "External" {
		t.Fatalf("String() = %q, want External", got)
	}
}

func TestSourceType_Constructor_WithValue_SetsValue(t *testing.T) {
	source := message.SourceType("CustomSource")
	if got := string(source); got != "CustomSource" {
		t.Fatalf("source = %q, want CustomSource", got)
	}
}

func TestSourceType_Default_DefaultsToExternal(t *testing.T) {
	var source message.SourceType
	if source != message.SourceTypeExternal {
		t.Fatalf("source = %q, want external", source)
	}
}

func TestSourceType_External_HasExternalValue(t *testing.T) {
	source := message.SourceTypeExternal
	if source != message.SourceType("") {
		t.Fatalf("external = %q, want the zero source type", source)
	}
}

func TestSourceType_ContextProvider_HasContextProviderValue(t *testing.T) {
	source := agent.SourceTypeContextProvider
	if string(source) != "context-provider" {
		t.Fatalf("context provider = %q", source)
	}
}

func TestSourceType_HistoryProvider_HasHistoryProviderValue(t *testing.T) {
	source := agent.SourceTypeHistoryProvider
	if string(source) != "history-provider" {
		t.Fatalf("history provider = %q", source)
	}
}

func TestSourceType_StaticProperties_ReturnEqualValuesOnMultipleCalls(t *testing.T) {
	external1, external2 := message.SourceTypeExternal, message.SourceTypeExternal
	context1, context2 := agent.SourceTypeContextProvider, agent.SourceTypeContextProvider
	history1, history2 := agent.SourceTypeHistoryProvider, agent.SourceTypeHistoryProvider
	if external1 != external2 || context1 != context2 || history1 != history2 {
		t.Fatal("source type constants are unequal on repeated access")
	}
}

func TestSourceType_Equals_WithSameInstance_ReturnsTrue(t *testing.T) {
	source := message.SourceType("Test")
	other := source
	if source != other {
		t.Fatalf("%q != %q", source, other)
	}
}

func TestSourceType_Equals_WithEqualValue_ReturnsTrue(t *testing.T) {
	first := message.SourceType("Test")
	second := message.SourceType("Test")
	if first != second {
		t.Fatalf("%q != %q", first, second)
	}
}

func TestSourceType_Equals_WithDifferentValue_ReturnsFalse(t *testing.T) {
	first := message.SourceType("Test1")
	second := message.SourceType("Test2")
	if first == second {
		t.Fatalf("%q == %q", first, second)
	}
}

func TestSourceType_Equals_WithDifferentCase_ReturnsFalse(t *testing.T) {
	first := message.SourceType("Test")
	second := message.SourceType("test")
	if first == second {
		t.Fatalf("%q == %q", first, second)
	}
}

func TestSourceType_Equals_StaticExternalWithSameValue_ReturnsTrue(t *testing.T) {
	external := message.SourceTypeExternal
	other := message.SourceType("")
	if external != other {
		t.Fatalf("external %q != %q", external, other)
	}
}

func TestSourceType_ObjectEquals_WithEqualSourceType_ReturnsTrue(t *testing.T) {
	first := message.SourceType("Test")
	var second any = message.SourceType("Test")
	if first != second {
		t.Fatalf("%q != %v", first, second)
	}
}

func TestSourceType_ObjectEquals_WithDifferentType_ReturnsFalse(t *testing.T) {
	source := message.SourceType("Test")
	var other any = "Test"
	if source == other {
		t.Fatalf("typed source %q == untyped string %v", source, other)
	}
}

func TestSourceType_EqualityOperator_WithEqualValues_ReturnsTrue(t *testing.T) {
	first := message.SourceType("Test")
	second := message.SourceType("Test")
	if got := first == second; !got {
		t.Fatalf("%q == %q returned false", first, second)
	}
}

func TestSourceType_EqualityOperator_WithDifferentValues_ReturnsFalse(t *testing.T) {
	first := message.SourceType("Test1")
	second := message.SourceType("Test2")
	if got := first == second; got {
		t.Fatalf("%q == %q returned true", first, second)
	}
}

func TestSourceType_EqualityOperator_WithDefaultValues_ReturnsTrue(t *testing.T) {
	var first, second message.SourceType
	if got := first == second; !got {
		t.Fatalf("%q == %q returned false", first, second)
	}
}

func TestSourceType_EqualityOperator_WithStaticInstances_ReturnsTrue(t *testing.T) {
	first, second := message.SourceTypeExternal, message.SourceTypeExternal
	if got := first == second; !got {
		t.Fatalf("%q == %q returned false", first, second)
	}
}

func TestSourceType_EqualityOperator_StaticWithSameValue_ReturnsTrue(t *testing.T) {
	external := message.SourceTypeExternal
	other := message.SourceType("")
	if got := external == other; !got {
		t.Fatalf("%q == %q returned false", external, other)
	}
}

func TestSourceType_InequalityOperator_WithEqualValues_ReturnsFalse(t *testing.T) {
	first := message.SourceType("Test")
	second := message.SourceType("Test")
	if got := first != second; got {
		t.Fatalf("%q != %q returned true", first, second)
	}
}

func TestSourceType_InequalityOperator_WithDifferentValues_ReturnsTrue(t *testing.T) {
	first := message.SourceType("Test1")
	second := message.SourceType("Test2")
	if got := first != second; !got {
		t.Fatalf("%q != %q returned false", first, second)
	}
}

func TestSourceType_InequalityOperator_WithBothDefault_ReturnsFalse(t *testing.T) {
	var first, second message.SourceType
	if got := first != second; got {
		t.Fatalf("%q != %q returned true", first, second)
	}
}

func TestSourceType_InequalityOperator_DifferentStaticInstances_ReturnsTrue(t *testing.T) {
	external := message.SourceTypeExternal
	history := agent.SourceTypeHistoryProvider
	if got := external != history; !got {
		t.Fatalf("%q != %q returned false", external, history)
	}
}

func TestSourceType_ToString_ReturnsValue(t *testing.T) {
	source := message.SourceType("CustomSource")
	if got := string(source); got != "CustomSource" {
		t.Fatalf("string(source) = %q, want CustomSource", got)
	}
}

func TestMessage_WithSource_DoesNotModifyOriginalMessage(t *testing.T) {
	original := message.NewText("Hello")

	sourced := original.WithSource(message.Source{Type: agent.SourceTypeContextProvider, ID: "ProviderId"})

	if sourced == original || original.AdditionalProperties != nil || original.Source.Type != message.SourceTypeExternal {
		t.Fatalf("original = %+v, sourced = %+v; want an unchanged original and a clone", original, sourced)
	}
}

func TestMessage_WithSource_WithMatchingSourceTypeAndEmptySourceId_ReturnsSameInstance(t *testing.T) {
	original := message.NewText("Hello")
	original.Source = message.Source{Type: message.SourceTypeExternal}

	if got := original.WithSource(message.Source{Type: message.SourceTypeExternal}); got != original {
		t.Fatal("WithSource returned a clone for unchanged external attribution")
	}
}

func TestMessage_WithSource_WithNilAdditionalProperties_ClonesWithAttribution(t *testing.T) {
	original := message.NewText("Hello")
	original.AdditionalProperties = nil

	got := original.WithSource(message.Source{Type: agent.SourceTypeContextProvider, ID: "ProviderSourceId"})

	if got == original || got.Source.Type != agent.SourceTypeContextProvider || got.Source.ID != "ProviderSourceId" {
		t.Fatalf("WithSource() = %+v", got)
	}
}

func TestMessage_SourceID_WithAttribution_ReturnsSourceID(t *testing.T) {
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "MyProvider.FullName"}
	if got := msg.Source.ID; got != "MyProvider.FullName" {
		t.Fatalf("source ID = %q", got)
	}
}

func TestMessage_SourceID_WithDifferentSourceIds_ReturnsCorrectSourceID(t *testing.T) {
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "CustomHistorySourceId"}
	if got := msg.Source.ID; got != "CustomHistorySourceId" {
		t.Fatalf("source ID = %q", got)
	}
}

func TestMessage_SourceID_WithEmptyAdditionalProperties_ReturnsEmpty(t *testing.T) {
	msg := message.NewText("Hello")
	msg.AdditionalProperties = map[string]any{}
	if got := msg.Source.ID; got != "" {
		t.Fatalf("source ID = %q, want empty", got)
	}
}

func TestMessage_SourceID_WithMultipleProperties_ReturnsCorrectSourceID(t *testing.T) {
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: message.SourceTypeExternal, ID: "ExpectedSourceId"}
	msg.AdditionalProperties = map[string]any{"OtherProperty": "SomeValue", "AnotherProperty": 123}
	if got := msg.Source.ID; got != "ExpectedSourceId" {
		t.Fatalf("source ID = %q", got)
	}
}

func TestMessage_SourceID_WithNoAdditionalProperties_ReturnsEmpty(t *testing.T) {
	msg := message.NewText("Hello")
	if got := msg.Source.ID; got != "" {
		t.Fatalf("source ID = %q, want empty", got)
	}
}

func TestMessage_SourceID_WithNilAdditionalProperties_ReturnsEmpty(t *testing.T) {
	msg := message.NewText("Hello")
	msg.AdditionalProperties = nil
	if got := msg.Source.ID; got != "" {
		t.Fatalf("source ID = %q, want empty", got)
	}
}

func TestMessage_SourceType_WithContextProvider_ReturnsContextProvider(t *testing.T) {
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "TestSourceId"}
	if got := msg.Source.Type; got != agent.SourceTypeContextProvider {
		t.Fatalf("source type = %q", got)
	}
}

func TestMessage_SourceType_WithHistoryProvider_ReturnsHistoryProvider(t *testing.T) {
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "TestSourceId"}
	if got := msg.Source.Type; got != agent.SourceTypeHistoryProvider {
		t.Fatalf("source type = %q", got)
	}
}

func TestMessage_SourceType_WithCustomSourceType_ReturnsCustomSourceType(t *testing.T) {
	custom := message.SourceType("CustomSourceType")
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: custom, ID: "TestSourceId"}
	if got := msg.Source.Type; got != custom || string(got) != "CustomSourceType" {
		t.Fatalf("source type = %q", got)
	}
}

func TestMessage_SourceType_WithEmptyAdditionalProperties_ReturnsExternal(t *testing.T) {
	msg := message.NewText("Hello")
	msg.AdditionalProperties = map[string]any{}
	if got := msg.Source.Type; got != message.SourceTypeExternal {
		t.Fatalf("source type = %q, want external", got)
	}
}

func TestMessage_SourceType_WithExternalSourceType_ReturnsExternal(t *testing.T) {
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: message.SourceTypeExternal, ID: "TestSourceId"}
	if got := msg.Source.Type; got != message.SourceTypeExternal {
		t.Fatalf("source type = %q, want external", got)
	}
}

func TestMessage_SourceType_WithMultipleProperties_ReturnsCorrectSourceType(t *testing.T) {
	msg := message.NewText("Hello")
	msg.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "TestSourceId"}
	msg.AdditionalProperties = map[string]any{"OtherProperty": "SomeValue", "AnotherProperty": 123}
	if got := msg.Source.Type; got != agent.SourceTypeHistoryProvider {
		t.Fatalf("source type = %q", got)
	}
}

func TestMessage_SourceType_WithNoAdditionalProperties_ReturnsExternal(t *testing.T) {
	msg := message.NewText("Hello")
	if got := msg.Source.Type; got != message.SourceTypeExternal {
		t.Fatalf("source type = %q, want external", got)
	}
}

func TestMessage_SourceType_WithNilAdditionalProperties_ReturnsExternal(t *testing.T) {
	msg := message.NewText("Hello")
	msg.AdditionalProperties = nil
	if got := msg.Source.Type; got != message.SourceTypeExternal {
		t.Fatalf("source type = %q, want external", got)
	}
}

func TestSource_String(t *testing.T) {
	tests := []struct {
		source message.Source
		want   string
	}{
		{message.Source{}, "External"},
		{message.Source{ID: "x"}, "External:x"},
		{message.Source{Type: "middleware"}, "middleware"},
		{message.Source{Type: "context-provider", ID: "mem"}, "context-provider:mem"},
	}
	for _, tt := range tests {
		if got := tt.source.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

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
