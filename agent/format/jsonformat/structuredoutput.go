// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/agent"
)

// WrapNonObjectSchema returns a response format with an object-root schema.
// A non-object JSON schema is copied into a required "data" property. The
// supplied format and its schema are not modified; [ForType] still describes
// the unwrapped value for local validation.
func WrapNonObjectSchema(format agent.ResponseFormat) (agent.ResponseFormat, error) {
	if format.Kind != "json" {
		return agent.ResponseFormat{}, fmt.Errorf("response format kind %q, want json", format.Kind)
	}
	schema, ok := format.Schema.(*jsonschema.Schema)
	if !ok {
		return agent.ResponseFormat{}, fmt.Errorf("response format schema has type %T, want *jsonschema.Schema", format.Schema)
	}
	if schema == nil {
		return agent.ResponseFormat{}, fmt.Errorf("response format schema cannot be nil")
	}
	if schema.Type == "object" {
		return format, nil
	}

	dataSchema := schema.CloneSchemas()
	// Typed array results cannot be null, and some providers require a single schema type.
	if slices.Contains(dataSchema.Types, "array") &&
		(len(dataSchema.Types) == 1 || len(dataSchema.Types) == 2 && slices.Contains(dataSchema.Types, "null")) {
		dataSchema.Type = "array"
		dataSchema.Types = nil
	}
	wrappedSchema := &jsonschema.Schema{
		Type:                 "object",
		Properties:           map[string]*jsonschema.Schema{"data": dataSchema},
		Required:             []string{"data"},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
	wrappedSchema.Schema, dataSchema.Schema = dataSchema.Schema, ""
	wrappedSchema.ID, dataSchema.ID = dataSchema.ID, ""
	// Root references must still resolve after nesting the original schema.
	wrappedSchema.Defs, dataSchema.Defs = dataSchema.Defs, nil
	wrappedSchema.Definitions, dataSchema.Definitions = dataSchema.Definitions, nil
	format.Schema = wrappedSchema
	return format, nil
}

// UnmarshalStructuredOutput validates and decodes a response formatted by
// [WrapNonObjectSchema]. It unwraps non-object results from "data", while
// accepting valid bare JSON as a fallback. Extra envelope properties are
// ignored. Object results use the first top-level JSON value; wrapped results
// require a single top-level value. [Format.Unmarshal] uses its schema as supplied.
func (f *Format) UnmarshalStructuredOutput(data []byte, target any) error {
	if target == nil {
		return fmt.Errorf("structured output target cannot be nil")
	}
	targetFormat, err := ForType(reflect.TypeOf(target))
	if err != nil {
		return err
	}
	schema, ok := f.Schema.(*jsonschema.Schema)
	if !ok || schema == nil {
		return fmt.Errorf("structured output schema has type %T, want *jsonschema.Schema", f.Schema)
	}
	if targetFormat.Schema.(*jsonschema.Schema).Type == "object" || schema.Type != "object" {
		return f.unmarshalFirstValue(data, target)
	}

	dataSchema := schema.Properties["data"]
	if dataSchema == nil || !slices.Contains(schema.Required, "data") {
		return fmt.Errorf("structured output schema must have a required data property")
	}
	valueSchema := dataSchema.CloneSchemas()
	valueSchema.Schema = schema.Schema
	valueSchema.ID = schema.ID
	valueSchema.Defs = schema.Defs
	valueSchema.Definitions = schema.Definitions
	valueFormat := newFormat(f.Name, f.Description, valueSchema)

	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("structured output is empty")
	}
	if bytes.Equal(data, []byte("null")) {
		return fmt.Errorf("structured output is null")
	}
	if data[0] != '{' {
		return valueFormat.Unmarshal(data, target)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	wrappedData, ok := envelope["data"]
	if !ok {
		return valueFormat.Unmarshal(data, target)
	}
	if bytes.Equal(bytes.TrimSpace(wrappedData), []byte("null")) {
		return fmt.Errorf("structured output data is null")
	}
	return valueFormat.Unmarshal(wrappedData, target)
}

func (f *Format) unmarshalFirstValue(data []byte, target any) error {
	var first json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&first); err != nil {
		return fmt.Errorf("decoding structured output: %w", err)
	}
	return f.Unmarshal(first, target)
}
