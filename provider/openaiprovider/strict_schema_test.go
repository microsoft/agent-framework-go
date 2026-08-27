// Copyright (c) Microsoft. All rights reserved.

package openaiprovider

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
)

func TestStrictSchemaToMapTransformsCloneRecursively(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": []any{"string", "null"}},
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
				},
			},
		},
		"required": []any{"name"},
		"$defs": map[string]any{
			"details": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean"},
				},
			},
		},
	}
	original := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": []any{"string", "null"}},
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
				},
			},
		},
		"required": []any{"name"},
		"$defs": map[string]any{
			"details": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean"},
				},
			},
		},
	}

	strict, err := strictSchemaToMap(schema)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(schema, original) {
		t.Fatalf("source schema was mutated:\ngot  %#v\nwant %#v", schema, original)
	}
	if got, want := strict["required"], []any{"name", "items"}; !reflect.DeepEqual(got, want) {
		t.Errorf("root required = %#v, want %#v", got, want)
	}
	name := strict["properties"].(map[string]any)["name"].(map[string]any)
	if got, want := name["type"], []any{"string", "null"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required nullable type = %#v, want %#v", got, want)
	}
	items := strict["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)
	if got, want := items["required"], []any{"value"}; !reflect.DeepEqual(got, want) {
		t.Errorf("array item required = %#v, want %#v", got, want)
	}
	if got := items["additionalProperties"]; got != false {
		t.Errorf("array item additionalProperties = %#v, want false", got)
	}
	details := strict["$defs"].(map[string]any)["details"].(map[string]any)
	if got, want := details["required"], []any{"enabled"}; !reflect.DeepEqual(got, want) {
		t.Errorf("definition required = %#v, want %#v", got, want)
	}
}

func TestStrictSchemaToMapCleansUnsupportedOpenAIKeywords(t *testing.T) {
	unsupported := map[string]any{
		"contentEncoding":       "base64",
		"contentMediaType":      "text/plain",
		"not":                   true,
		"minLength":             1,
		"maxLength":             10,
		"pattern":               "^[a-z]+$",
		"format":                "email",
		"minimum":               1,
		"maximum":               10,
		"multipleOf":            2,
		"patternProperties":     map[string]any{"^x-": map[string]any{"type": "string"}},
		"minItems":              1,
		"maxItems":              10,
		"unevaluatedProperties": false,
		"propertyNames":         map[string]any{"type": "string"},
		"minProperties":         1,
		"maxProperties":         10,
		"unevaluatedItems":      false,
		"contains":              map[string]any{"type": "string"},
		"minContains":           1,
		"maxContains":           2,
		"uniqueItems":           true,
	}
	schema := map[string]any{
		"type":        "string",
		"description": "A value",
		"default":     "fallback",
	}
	for name, value := range unsupported {
		schema[name] = value
	}

	strict, err := strictSchemaToMap(schema)
	if err != nil {
		t.Fatal(err)
	}

	description, ok := strict["description"].(string)
	if !ok {
		t.Fatalf("description = %#v, want string", strict["description"])
	}
	if !strings.HasPrefix(description, `A value (Default value: "fallback")`) {
		t.Errorf("description = %q, want original description with default", description)
	}
	if _, ok := strict["default"]; ok {
		t.Error("strict schema retained default")
	}
	for name := range unsupported {
		if _, ok := strict[name]; ok {
			t.Errorf("strict schema retained unsupported keyword %q", name)
		}
		if !strings.Contains(description, name+": ") {
			t.Errorf("description does not preserve %q constraint: %q", name, description)
		}
	}
}

func TestStrictSchemaToMapConvertsBooleanSchemas(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"anything": true,
			"nothing":  false,
		},
	}

	strict, err := strictSchemaToMap(schema)
	if err != nil {
		t.Fatal(err)
	}

	properties := strict["properties"].(map[string]any)
	if got, want := properties["anything"], map[string]any{}; !reflect.DeepEqual(got, want) {
		t.Errorf("anything schema = %#v, want %#v", got, want)
	}
	if got, want := properties["nothing"], map[string]any{"description": "not: true"}; !reflect.DeepEqual(got, want) {
		t.Errorf("nothing schema = %#v, want %#v", got, want)
	}
}

func TestStrictSchemaTransformCacheReturnsIndependentMaps(t *testing.T) {
	cache := strictSchemaTransformCache{}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}

	first, err := cache.transform(schema)
	if err != nil {
		t.Fatal(err)
	}
	first["required"] = []any{"corrupted"}

	second, err := cache.transform(schema)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := second["required"], []any{"name"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cached required = %#v, want independent %#v", got, want)
	}
	if got := len(cache.entries); got != 1 {
		t.Fatalf("cache entries = %d, want 1", got)
	}

	schema["properties"].(map[string]any)["age"] = map[string]any{"type": "integer"}
	third, err := cache.transform(schema)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := third["required"], []any{"age", "name"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("required after source mutation = %#v, want %#v", got, want)
	}
	if got := len(cache.entries); got != 2 {
		t.Fatalf("cache entries after source mutation = %d, want 2", got)
	}
}

func TestStrictSchemaTransformCacheIsBounded(t *testing.T) {
	cache := strictSchemaTransformCache{}
	for index := 0; index < strictSchemaTransformCacheLimit+1; index++ {
		schema := map[string]any{"type": "string", "cacheKey": index}
		if _, err := cache.transform(schema); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(cache.entries); got != strictSchemaTransformCacheLimit {
		t.Fatalf("cache entries = %d, want %d", got, strictSchemaTransformCacheLimit)
	}
}

func TestStrictSchemaTransformCacheSupportsConcurrentCalls(t *testing.T) {
	cache := strictSchemaTransformCache{}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}

	const callCount = 32
	results := make(chan map[string]any, callCount)
	errors := make(chan error, callCount)
	var waitGroup sync.WaitGroup
	for range callCount {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := cache.transform(schema)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Errorf("transform() error = %v", err)
	}
	for result := range results {
		if got, want := result["required"], []any{"name"}; !reflect.DeepEqual(got, want) {
			t.Errorf("required = %#v, want %#v", got, want)
		}
	}
	if got := len(cache.entries); got != 1 {
		t.Fatalf("cache entries = %d, want 1", got)
	}
}

func TestStrictSchemaToMapRejectsOpenMap(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
	}

	_, err := strictSchemaToMap(schema)
	if err == nil || !strings.Contains(err.Error(), "properties/tags: additionalProperties must be false") {
		t.Fatalf("strictSchemaToMap() error = %v, want open-map error with path", err)
	}
}

func TestStrictSchemaToMapRejectsInferredMap(t *testing.T) {
	type payload struct {
		Tags map[string]string `json:"tags"`
	}
	format, err := jsonformat.For[payload]()
	if err != nil {
		t.Fatal(err)
	}

	_, err = strictSchemaToMap(format.Schema)
	if err == nil || !strings.Contains(err.Error(), "properties/tags: additionalProperties must be false") {
		t.Fatalf("strictSchemaToMap() error = %v, want inferred-map error with path", err)
	}
}

func TestStrictSchemaToMapRejectsImplicitlyOpenObject(t *testing.T) {
	_, err := strictSchemaToMap(map[string]any{"type": "object"})
	if err == nil || !strings.Contains(err.Error(), "<root>: object schema must declare properties or set additionalProperties to false") {
		t.Fatalf("strictSchemaToMap() error = %v, want implicitly-open-object error", err)
	}
}

func TestStrictSchemaToMapRejectsUndeclaredRequiredProperty(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name", "ghost"},
	}

	_, err := strictSchemaToMap(schema)
	if err == nil || !strings.Contains(err.Error(), `<root>: required property "ghost" is not declared in properties`) {
		t.Fatalf("strictSchemaToMap() error = %v, want undeclared-property error", err)
	}
}

func TestStrictSchemaToMapPreservesRequiredNullableLocalValidation(t *testing.T) {
	type payload struct {
		Name  *string `json:"name"`
		Email *string `json:"email,omitempty"`
	}
	format, err := jsonformat.For[payload]()
	if err != nil {
		t.Fatal(err)
	}

	strict, err := strictSchemaToMap(format.Schema)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strict["required"], []any{"name", "email"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("strict required = %#v, want %#v", got, want)
	}

	local, err := jsonformat.FromResponseFormat(format)
	if err != nil {
		t.Fatal(err)
	}
	var value payload
	if err := local.Unmarshal([]byte(`{"email":null}`), &value); err == nil {
		t.Fatal("local Unmarshal() accepted a missing required nullable property")
	}
}

func TestResponseBuildersRejectOpenMapOnlyInStrictMode(t *testing.T) {
	type payload struct {
		Tags map[string]string `json:"tags"`
	}
	format, err := jsonformat.For[payload]()
	if err != nil {
		t.Fatal(err)
	}

	strictOptions := []agent.Option{agent.WithResponseFormat(format)}
	if _, err := buildCompletionParams("test-model", nil, strictOptions); err == nil || !strings.Contains(err.Error(), "properties/tags: additionalProperties must be false") {
		t.Fatalf("buildCompletionParams() error = %v, want strict map error", err)
	}
	if _, err := responsesBuildCompletionParams(AgentConfig{Model: "test-model"}, nil, strictOptions); err == nil || !strings.Contains(err.Error(), "properties/tags: additionalProperties must be false") {
		t.Fatalf("responsesBuildCompletionParams() error = %v, want strict map error", err)
	}

	format.Strict = false
	nonStrictOptions := []agent.Option{agent.WithResponseFormat(format)}
	if _, err := buildCompletionParams("test-model", nil, nonStrictOptions); err != nil {
		t.Fatalf("buildCompletionParams() rejected non-strict map: %v", err)
	}
	if _, err := responsesBuildCompletionParams(AgentConfig{Model: "test-model"}, nil, nonStrictOptions); err != nil {
		t.Fatalf("responsesBuildCompletionParams() rejected non-strict map: %v", err)
	}
}
