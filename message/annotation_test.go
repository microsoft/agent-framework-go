// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework/go/message"
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

func TestAnnotatedRegionEncoding_Roundtrip(t *testing.T) {
	regions := message.AnnotatedRegions{
		&message.TextSpanAnnotatedRegion{
			Start: 10,
			End:   50,
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
