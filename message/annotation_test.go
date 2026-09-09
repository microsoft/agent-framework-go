// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestAnnotationEncoding_Roundtrip(t *testing.T) {
	annotations := message.Annotations{
		&message.CitationAnnotation{
			URL:      "http://example.com",
			Title:    "Example Title",
			Snippet:  "This is a snippet from the source.",
			ToolName: "ExampleTool",
			FileID:   "file123",
		},
	}
	data, err := json.Marshal(annotations)
	if err != nil {
		t.Error(err)
	}
	var decoded message.Annotations
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Error(err)
	}
	if len(decoded) != len(annotations) {
		t.Errorf("expected %d contents, got %d", len(annotations), len(decoded))
	}
	for i, v := range annotations {
		if !reflect.DeepEqual(v, decoded[i]) {
			t.Errorf("[%d]: expected content %v, got %v", i, v, decoded[i])
		}
	}
}

func TestCitationAnnotationEncoding_PreservesAdditionalProperties(t *testing.T) {
	original := &message.CitationAnnotation{
		AdditionalProperties: map[string]any{"provider": "openai", "score": "0.9"},
		Title:                "Example Title",
		URL:                  "http://example.com",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("AdditionalProperties")) {
		t.Fatalf("marshaled JSON does not contain AdditionalProperties: %s", data)
	}
	var decoded message.CitationAnnotation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.AdditionalProperties, decoded.AdditionalProperties) {
		t.Fatalf("AdditionalProperties = %v, want %v", decoded.AdditionalProperties, original.AdditionalProperties)
	}
}

func TestAnnotationEncoding_UnknownTypesPreservedAsRaw(t *testing.T) {
	const rawRegion = `{"Type":"futureRegion","Start":1,"End":2}`
	const rawAnnotation = `{"Type":"futureAnnotation","Value":42}`
	data := []byte(`[` +
		`{"Type":"citation","AnnotatedRegions":[` + rawRegion + `]},` +
		rawAnnotation +
		`]`)

	var annotations message.Annotations
	if err := json.Unmarshal(data, &annotations); err != nil {
		t.Fatal(err)
	}
	if len(annotations) != 2 {
		t.Fatalf("expected 2 annotations, got %d", len(annotations))
	}

	citation, ok := annotations[0].(*message.CitationAnnotation)
	if !ok {
		t.Fatalf("annotations[0] = %T, want *message.CitationAnnotation", annotations[0])
	}
	if len(citation.AnnotatedRegions) != 1 {
		t.Fatalf("expected 1 annotated region, got %d", len(citation.AnnotatedRegions))
	}
	rawRegionVal, ok := citation.AnnotatedRegions[0].(*message.RawAnnotatedRegion)
	if !ok {
		t.Fatalf("region = %T, want *message.RawAnnotatedRegion", citation.AnnotatedRegions[0])
	}
	if got := string(rawRegionVal.RawRepresentation); got != rawRegion {
		t.Fatalf("region raw = %s, want %s", got, rawRegion)
	}

	rawAnnotationVal, ok := annotations[1].(*message.RawAnnotation)
	if !ok {
		t.Fatalf("annotations[1] = %T, want *message.RawAnnotation", annotations[1])
	}
	if got := string(rawAnnotationVal.RawRepresentation); got != rawAnnotation {
		t.Fatalf("annotation raw = %s, want %s", got, rawAnnotation)
	}

	// The unknown entries round-trip back to their original JSON.
	if b, err := json.Marshal(citation.AnnotatedRegions[0]); err != nil || string(b) != rawRegion {
		t.Fatalf("region remarshal = %s (err %v), want %s", b, err, rawRegion)
	}
	if b, err := json.Marshal(annotations[1]); err != nil || string(b) != rawAnnotation {
		t.Fatalf("annotation remarshal = %s (err %v), want %s", b, err, rawAnnotation)
	}
}

func TestAnnotationEncoding_KnownTypeInvalidPayloadReturnsError(t *testing.T) {
	// A known discriminator ("citation") with a payload that does not match the
	// structured type must surface a decode error rather than being silently
	// downgraded to a RawAnnotation.
	if err := json.Unmarshal([]byte(`[{"Type":"citation","URL":123}]`), new(message.Annotations)); err == nil {
		t.Fatal("expected error decoding known annotation with invalid payload, got nil")
	}

	// Likewise for a known annotated region type ("textSpan").
	if err := json.Unmarshal([]byte(`[{"Type":"textSpan","StartIndex":"nope"}]`), new(message.AnnotatedRegions)); err == nil {
		t.Fatal("expected error decoding known region with invalid payload, got nil")
	}
}

func TestAnnotatedRegionEncoding_Roundtrip(t *testing.T) {
	start, end := 10, 50
	regions := message.AnnotatedRegions{
		&message.TextSpanAnnotatedRegion{
			StartIndex: &start,
			EndIndex:   &end,
		},
	}
	data, err := json.Marshal(regions)
	if err != nil {
		t.Error(err)
	}
	var decoded message.AnnotatedRegions
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Error(err)
	}
	if len(decoded) != len(regions) {
		t.Errorf("expected %d regions, got %d", len(regions), len(decoded))
	}
	for i, v := range regions {
		if !reflect.DeepEqual(v, decoded[i]) {
			t.Errorf("[%d]: expected region %v, got %v", i, v, decoded[i])
		}
	}
}

func TestTextSpanAnnotatedRegionEncoding_NullableEndpoints(t *testing.T) {
	zero := 0
	regions := message.AnnotatedRegions{
		&message.TextSpanAnnotatedRegion{},
		&message.TextSpanAnnotatedRegion{StartIndex: &zero},
	}

	data, err := json.Marshal(regions)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `[{"Type":"textSpan"},{"StartIndex":0,"Type":"textSpan"}]`; got != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}

	var decoded message.AnnotatedRegions
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	first := decoded[0].(*message.TextSpanAnnotatedRegion)
	second := decoded[1].(*message.TextSpanAnnotatedRegion)
	if first.StartIndex != nil || first.EndIndex != nil {
		t.Fatalf("first region = %#v, want nil endpoints", first)
	}
	if second.StartIndex == nil || *second.StartIndex != 0 || second.EndIndex != nil {
		t.Fatalf("second region = %#v, want start 0 and nil end", second)
	}
}
