// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
)

func requireFormat(t *testing.T, responseFormat agent.ResponseFormat) *jsonformat.Format {
	t.Helper()
	format, err := jsonformat.FromResponseFormat(responseFormat)
	if err != nil {
		t.Fatal(err)
	}
	return format
}

func TestNew_NilSchemaIsUnconstrained(t *testing.T) {
	format := requireFormat(t, jsonformat.New("any", "", nil))
	value := map[string]any{"status": "ok"}

	data, err := format.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"status":"ok"}` {
		t.Fatalf("Marshal() = %s, want object JSON", data)
	}
}

func TestFromResponseFormat_RejectsTypedNilSchema(t *testing.T) {
	var schema *jsonschema.Schema
	format, err := jsonformat.FromResponseFormat(agent.ResponseFormat{Kind: "json", Schema: schema})
	if err == nil || err.Error() != "response format schema cannot be nil" {
		t.Fatalf("FromResponseFormat() error = %v, want nil-schema error", err)
	}
	if format != nil {
		t.Fatalf("FromResponseFormat() = %#v, want nil", format)
	}
}

func TestEncodingRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		v    any
	}{
		{"Struct", Struct{Name: "Alice", Age: 30, Email: "alice@example.com"}},
		{"Struct", &Struct{Name: "Alice", Age: 30, Email: "alice@example.com"}},
		{"EmptyStruct", struct{}{}},
		{"map[string]int", map[string]int{"a": 1, "b": 2}},
		{"[]string", []string{"foo", "bar", "baz"}},
		{"int", 42},
		{"string", "hello, world"},
		{"bool", true},
		{"bool", new(bool)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := reflect.TypeOf(tt.v)
			responseFormat, err := jsonformat.ForType(rt)
			if err != nil {
				t.Fatal(err)
			}
			format := requireFormat(t, responseFormat)

			data, err := format.Marshal(tt.v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			v2 := reflect.New(rt).Interface()
			if err := format.Unmarshal(data, &v2); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got := reflect.ValueOf(v2).Elem().Interface()
			if !reflect.DeepEqual(tt.v, got) {
				t.Fatalf("expected: %+v, got: %+v", tt.v, got)
			}
		})
	}
}

func TestWrapNonObjectSchema(t *testing.T) {
	type object struct {
		Name string `json:"name"`
	}
	type species string
	type nested struct {
		Label string `json:"label"`
	}
	for _, tc := range []struct {
		name, property, propertyType string
		target                       any
	}{
		{name: "object", property: "name", propertyType: "string", target: new(object)},
		{name: "integer", property: "data", propertyType: "integer", target: new(int)},
		{name: "array", property: "data", propertyType: "array", target: new([]string)},
		{name: "enum-like string", property: "data", propertyType: "string", target: new(species)},
		{name: "nested array", property: "data", propertyType: "array", target: new([]nested)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := jsonformat.ForType(reflect.TypeOf(tc.target))
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(original.Schema)
			if err != nil {
				t.Fatal(err)
			}
			wrapped, err := jsonformat.WrapNonObjectSchema(original)
			if err != nil {
				t.Fatal(err)
			}
			schema, ok := wrapped.Schema.(*jsonschema.Schema)
			if !ok || schema.Type != "object" || wrapped.Name != original.Name {
				t.Fatalf("wrapped format = %+v, want named object schema", wrapped)
			}
			property := schema.Properties[tc.property]
			if property == nil || property.Type != tc.propertyType {
				t.Errorf("schema property %q = %+v, want %s", tc.property, property, tc.propertyType)
			}
			if tc.property == "data" {
				if !slices.Equal(schema.Required, []string{"data"}) {
					t.Errorf("required properties = %v, want [data]", schema.Required)
				}
				closed, err := json.Marshal(schema.AdditionalProperties)
				if err != nil {
					t.Fatal(err)
				}
				if string(closed) != "false" {
					t.Errorf("additionalProperties = %s, want false", closed)
				}
			} else if _, wrapped := schema.Properties["data"]; wrapped {
				t.Error("object schema unexpectedly wrapped")
			}
			after, err := json.Marshal(original.Schema)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("original schema mutated: %s -> %s", before, after)
			}
		})
	}
}

func TestUnmarshalStructuredOutput(t *testing.T) {
	type object struct {
		Name string `json:"name"`
	}
	type species string
	type nested struct {
		Label string `json:"label"`
	}
	pointerValue := new(42)
	for _, tc := range []struct {
		name, response string
		newTarget      func() any
		want           any
	}{
		{name: "object", response: `{"name":"Tiger"}`,
			newTarget: func() any { return new(object) }, want: object{Name: "Tiger"}},
		{name: "object with multiple JSON values", response: `{"name":"First"} {"name":"Second"}`,
			newTarget: func() any { return new(object) }, want: object{Name: "First"}},
		{name: "integer", response: `{"data":42}`,
			newTarget: func() any { return new(int) }, want: 42},
		{name: "wrapped result with extra property", response: `{"data":42,"extra":true}`,
			newTarget: func() any { return new(int) }, want: 42},
		{name: "array", response: `{"data":["a","b"]}`,
			newTarget: func() any { return new([]string) }, want: []string{"a", "b"}},
		{name: "enum-like string", response: `{"data":"Tiger"}`,
			newTarget: func() any { return new(species) }, want: species("Tiger")},
		{name: "nested array", response: `{"data":[{"label":"value"}]}`,
			newTarget: func() any { return new([]nested) }, want: []nested{{Label: "value"}}},
		{name: "pointer destination", response: `{"data":42}`,
			newTarget: func() any { return new(*int) }, want: pointerValue},
		{name: "bare integer fallback", response: `42`,
			newTarget: func() any { return new(int) }, want: 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := tc.newTarget()
			original, err := jsonformat.ForType(reflect.TypeOf(output))
			if err != nil {
				t.Fatal(err)
			}
			wrapped, err := jsonformat.WrapNonObjectSchema(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := requireFormat(t, wrapped).UnmarshalStructuredOutput([]byte(tc.response), output); err != nil {
				t.Fatal(err)
			}
			if got := reflect.ValueOf(output).Elem().Interface(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decoded result = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestUnmarshalStructuredOutputRejectsInvalidEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		newTarget      func() any
	}{
		{name: "missing data", response: `{"other":42}`},
		{name: "wrongly cased data property", response: `{"DATA":42}`},
		{name: "wrong data type", response: `{"data":"bad"}`},
		{name: "null data", response: `{"data":null}`},
		{name: "wrapped multiple JSON values", response: `{"data":42} {"data":99}`},
		{name: "bare multiple JSON values", response: `42 99`},
		{name: "empty response"},
		{name: "empty object response", newTarget: func() any { return new(struct{}) }},
		{name: "bare null array", response: `null`, newTarget: func() any { return new([]string) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := any(new(int))
			if tc.newTarget != nil {
				output = tc.newTarget()
			}
			original, err := jsonformat.ForType(reflect.TypeOf(output))
			if err != nil {
				t.Fatal(err)
			}
			wrapped, err := jsonformat.WrapNonObjectSchema(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := requireFormat(t, wrapped).UnmarshalStructuredOutput([]byte(tc.response), output); err == nil {
				t.Fatalf("UnmarshalStructuredOutput(%q) succeeded, want error", tc.response)
			}
		})
	}
}

func TestWrapNonObjectSchemaPreservesConstraints(t *testing.T) {
	original, err := jsonformat.For[int]()
	if err != nil {
		t.Fatal(err)
	}
	schema := original.Schema.(*jsonschema.Schema)
	schema.Minimum = new(float64(10))
	wrapped, err := jsonformat.WrapNonObjectSchema(original)
	if err != nil {
		t.Fatal(err)
	}
	dataSchema := wrapped.Schema.(*jsonschema.Schema).Properties["data"]
	if dataSchema == nil || dataSchema.Minimum == nil || *dataSchema.Minimum != 10 {
		t.Fatalf("wrapped data schema = %+v, want minimum 10", dataSchema)
	}
	if schema.Type != "integer" || schema.Minimum == nil || *schema.Minimum != 10 {
		t.Fatalf("original schema mutated: %+v", schema)
	}

	for _, tc := range []struct {
		response string
		wantErr  bool
	}{
		{response: `{"data":42}`},
		{response: `{"data":5}`, wantErr: true},
		{response: `42`},
		{response: `5`, wantErr: true},
	} {
		var output int
		err := requireFormat(t, wrapped).UnmarshalStructuredOutput([]byte(tc.response), &output)
		if (err != nil) != tc.wantErr {
			t.Errorf("UnmarshalStructuredOutput(%s) error = %v, want error %v", tc.response, err, tc.wantErr)
		}
		if !tc.wantErr && output != 42 {
			t.Errorf("decoded result = %d, want 42", output)
		}
	}
}

func TestWrapNonObjectSchemaPreservesReferences(t *testing.T) {
	original := jsonformat.New("constrained", "", &jsonschema.Schema{
		Ref: "#/$defs/positive",
		Defs: map[string]*jsonschema.Schema{
			"positive": {Type: "integer", Minimum: new(float64(10))},
		},
	})
	wrapped, err := jsonformat.WrapNonObjectSchema(original)
	if err != nil {
		t.Fatal(err)
	}
	var output int
	if err := requireFormat(t, wrapped).UnmarshalStructuredOutput([]byte(`{"data":42}`), &output); err != nil {
		t.Fatalf("decode referenced schema: %v", err)
	}
	if output != 42 {
		t.Errorf("decoded result = %d, want 42", output)
	}
	if err := requireFormat(t, wrapped).UnmarshalStructuredOutput([]byte(`{"data":5}`), &output); err == nil {
		t.Fatal("value below referenced minimum was accepted")
	}
	schema := original.Schema.(*jsonschema.Schema)
	if schema.Ref != "#/$defs/positive" || schema.Defs["positive"] == nil {
		t.Fatalf("original references mutated: %+v", schema)
	}
}

func TestNormalizePreservesInterfaceValues(t *testing.T) {
	format := requireFormat(t, jsonformat.Any())
	var value any = map[string]any{"status": "ok", "value": 42}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	got, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", value)
	}
	if !reflect.DeepEqual(got, map[string]any{"status": "ok", "value": 42}) {
		t.Fatalf("expected interface value to be preserved, got %#v", got)
	}
}

func TestNormalizeAppliesDefaults(t *testing.T) {
	format := requireFormat(t, jsonformat.New("test", "", &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"count":  {Type: "integer"},
			"status": {Type: "string", Default: json.RawMessage(`"ok"`)},
		},
		Required:             []string{"count"},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}))
	value := map[string]any{"count": 1}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	if !reflect.DeepEqual(value, map[string]any{"count": 1, "status": "ok"}) {
		t.Fatalf("expected defaults to be applied, got %#v", value)
	}
}

func TestNormalizeStructValue(t *testing.T) {
	type output struct {
		Count int `json:"count"`
	}

	format := requireFormat(t, jsonformat.MustFor[output]())
	value := output{Count: 1}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
}

func TestNormalizeInterfaceStructValue(t *testing.T) {
	type output struct {
		Count int `json:"count"`
	}

	format := requireFormat(t, jsonformat.MustFor[output]())
	var value any = output{Count: 1}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
}

func TestNormalizeEmptyStruct(t *testing.T) {
	format := requireFormat(t, jsonformat.MustFor[struct{}]())
	value := struct{}{}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
}
