// Copyright (c) Microsoft. All rights reserved.

package embedding_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/embedding"
)

func TestEmbeddingGenerationOptions_PropsDefaulted(t *testing.T) {
	for _, options := range []*embedding.Options{nil, {}, (&embedding.Options{}).Clone()} {
		clone := options.Clone()
		if clone.ModelID != nil || clone.Dimensions != nil || clone.AdditionalProperties != nil || clone.RawRepresentationFactory != nil {
			t.Fatalf("clone = %+v", clone)
		}
	}
}

func TestEmbeddingGenerationOptions_InvalidJSON(t *testing.T) {
	for _, data := range []string{`{"Dimensions":"bad"}`, `{"Dimensions":1.5}`, `{`} {
		options := embedding.Options{ModelID: new("original")}
		if err := json.Unmarshal([]byte(data), &options); err == nil {
			t.Fatalf("expected decoding error for %s", data)
		}
		if options.ModelID == nil || *options.ModelID != "original" {
			t.Fatal("invalid input modified receiver")
		}
	}
}

func TestEmbeddingGenerationOptions_Properties_Roundtrip(t *testing.T) {
	nested := &struct{ value int }{42}
	raw := &struct{}{}
	generator := &testGenerator[string, *embedding.Embedding[embedding.Vector]]{}
	options := &embedding.Options{
		ModelID: new("modelId"), Dimensions: new(1536),
		AdditionalProperties: map[string]any{"key": nested},
		RawRepresentationFactory: func(g any) any {
			if g != generator {
				t.Fatal("wrong generator passed to factory")
			}
			return raw
		},
	}
	clone := options.Clone()
	if clone == options || *clone.ModelID != "modelId" || *clone.Dimensions != 1536 ||
		clone.AdditionalProperties["key"] != nested || clone.RawRepresentationFactory(generator) != raw {
		t.Fatalf("clone = %+v", clone)
	}
	*clone.ModelID = "new"
	*clone.Dimensions = 3
	clone.AdditionalProperties["other"] = 1
	if *options.ModelID != "modelId" || *options.Dimensions != 1536 || len(options.AdditionalProperties) != 1 {
		t.Fatal("clone mutations affected original")
	}
	nested.value++
	if clone.AdditionalProperties["key"].(*struct{ value int }).value != 43 {
		t.Fatal("property values must be shallow-copied")
	}
	empty := (&embedding.Options{AdditionalProperties: map[string]any{}}).Clone()
	if empty.AdditionalProperties == nil {
		t.Fatal("empty map became nil")
	}
}

func TestEmbeddingGenerationOptions_JsonSerialization_Roundtrips(t *testing.T) {
	options := embedding.Options{
		ModelID: new("model"), Dimensions: new(1536),
		AdditionalProperties:     map[string]any{"key": "value"},
		RawRepresentationFactory: func(any) any { return nil },
	}
	data, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var decoded embedding.Options
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	options.RawRepresentationFactory = nil
	if !reflect.DeepEqual(decoded, options) {
		t.Fatalf("decoded = %+v, want %+v", decoded, options)
	}
}

func TestEmbeddingGenerationOptions_JsonDeserialization_KnownPayload(t *testing.T) {
	var options embedding.Options
	err := json.Unmarshal([]byte(`{"dimensions":3072,"modelId":"text-embedding-3-large","additionalProperties":{"key":"val"}}`), &options)
	if err != nil || options.Dimensions == nil || *options.Dimensions != 3072 ||
		options.ModelID == nil || *options.ModelID != "text-embedding-3-large" ||
		options.AdditionalProperties["key"] != "val" {
		t.Fatalf("options = %+v, %v", options, err)
	}
}

func TestEmbeddingGenerationOptions_UnsetAndExplicitEmpty(t *testing.T) {
	for _, test := range []struct {
		options embedding.Options
		want    string
	}{
		{embedding.Options{}, `{}`},
		{embedding.Options{Dimensions: new(0)}, `{"Dimensions":0}`},
		{embedding.Options{ModelID: new("")}, `{"ModelID":""}`},
		{embedding.Options{AdditionalProperties: map[string]any{}}, `{"AdditionalProperties":{}}`},
	} {
		data, err := json.Marshal(test.options)
		if err != nil || string(data) != test.want {
			t.Fatalf("Marshal = %s, %v; want %s", data, err, test.want)
		}
		var result embedding.Options
		if err := json.Unmarshal(data, &result); err != nil || !reflect.DeepEqual(result, test.options) {
			t.Fatalf("roundtrip = %+v, %v", result, err)
		}
		clone := test.options.Clone()
		if !reflect.DeepEqual(clone, &test.options) ||
			test.options.Dimensions != nil && clone.Dimensions == test.options.Dimensions {
			t.Fatalf("clone = %+v, want independent copy of %+v", clone, test.options)
		}
	}
}
