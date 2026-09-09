// Copyright (c) Microsoft. All rights reserved.

package openaiprovider

import (
	"reflect"
	"strconv"
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

func TestStrictSchemaToMapPreservesSupportedOpenAIKeywords(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":      "string",
				"minLength": float64(1),
				"maxLength": float64(10),
				"pattern":   "^[a-z]+$",
				"format":    "email",
			},
			"number": map[string]any{
				"type":       "number",
				"minimum":    float64(1),
				"maximum":    float64(10),
				"multipleOf": float64(2),
			},
			"items": map[string]any{
				"type":     "array",
				"items":    map[string]any{"type": "string"},
				"minItems": float64(1),
				"maxItems": float64(10),
			},
		},
	}

	strict, err := strictSchemaToMap(schema)
	if err != nil {
		t.Fatal(err)
	}

	properties := strict["properties"].(map[string]any)
	for name, want := range schema["properties"].(map[string]any) {
		got := properties[name].(map[string]any)
		for keyword, wantValue := range want.(map[string]any) {
			if gotValue := got[keyword]; !reflect.DeepEqual(gotValue, wantValue) {
				t.Errorf("properties/%s/%s = %#v, want %#v", name, keyword, gotValue, wantValue)
			}
		}
	}
}

func TestStrictSchemaToMapMovesDefaultToDescription(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{
				"type":        "string",
				"description": "A value",
				"default":     "fallback",
			},
		},
	}

	strict, err := strictSchemaToMap(schema)
	if err != nil {
		t.Fatal(err)
	}

	value := strict["properties"].(map[string]any)["value"].(map[string]any)
	if got, want := value["description"], `A value (Default value: "fallback")`; got != want {
		t.Errorf("description = %#v, want %#v", got, want)
	}
	if _, ok := value["default"]; ok {
		t.Error("strict schema retained default")
	}
}

func TestStrictSchemaToMapRejectsUnsupportedOpenAIKeywords(t *testing.T) {
	tests := map[string]any{
		"allOf":                 []any{map[string]any{"type": "string"}},
		"contains":              map[string]any{"type": "string"},
		"contentEncoding":       "base64",
		"contentMediaType":      "text/plain",
		"dependentRequired":     map[string]any{"value": []any{"other"}},
		"dependentSchemas":      map[string]any{"value": map[string]any{"type": "string"}},
		"else":                  map[string]any{"type": "string"},
		"if":                    map[string]any{"type": "string"},
		"maxContains":           float64(2),
		"maxProperties":         float64(2),
		"minContains":           float64(1),
		"minProperties":         float64(1),
		"not":                   map[string]any{"type": "string"},
		"patternProperties":     map[string]any{"^x-": map[string]any{"type": "string"}},
		"prefixItems":           []any{map[string]any{"type": "string"}},
		"propertyNames":         map[string]any{"type": "string"},
		"then":                  map[string]any{"type": "string"},
		"unevaluatedItems":      false,
		"unevaluatedProperties": false,
		"uniqueItems":           true,
	}
	for keyword, value := range tests {
		t.Run(keyword, func(t *testing.T) {
			schema := map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{
						"type":  keywordType(keyword),
						keyword: value,
					},
				},
			}

			_, err := strictSchemaToMap(schema)
			if err == nil || !strings.Contains(err.Error(), `properties/value: unsupported keyword "`+keyword+`"`) {
				t.Fatalf("strictSchemaToMap() error = %v, want unsupported-keyword error", err)
			}
		})
	}
}

func keywordType(keyword string) string {
	switch keyword {
	case "contains", "maxContains", "minContains", "prefixItems", "unevaluatedItems", "uniqueItems":
		return "array"
	case "dependentRequired", "dependentSchemas", "maxProperties", "minProperties", "patternProperties", "propertyNames", "unevaluatedProperties":
		return "object"
	default:
		return "string"
	}
}

func TestStrictSchemaToMapRejectsBooleanSchemas(t *testing.T) {
	for _, value := range []bool{true, false} {
		t.Run(strconv.FormatBool(value), func(t *testing.T) {
			schema := map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": value,
				},
			}

			_, err := strictSchemaToMap(schema)
			if err == nil || !strings.Contains(err.Error(), "properties/value: boolean schemas are not supported") {
				t.Fatalf("strictSchemaToMap() error = %v, want boolean-schema error", err)
			}
		})
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
		schema := map[string]any{
			"type":        "object",
			"properties":  map[string]any{"value": map[string]any{"type": "string"}},
			"description": strconv.Itoa(index),
		}
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

func TestStrictSchemaToMapRejectsEmptyPropertiesOpenObject(t *testing.T) {
	_, err := strictSchemaToMap(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "<root>: object schema must declare properties or set additionalProperties to false") {
		t.Fatalf("strictSchemaToMap() error = %v, want empty open-object error", err)
	}
}

func TestStrictSchemaToMapRejectsNonObjectRoot(t *testing.T) {
	stringFormat, err := jsonformat.For[string]()
	if err != nil {
		t.Fatal(err)
	}
	arrayFormat, err := jsonformat.For[[]string]()
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]any{
		"string":        stringFormat.Schema,
		"array":         arrayFormat.Schema,
		"unconstrained": jsonformat.Any().Schema,
		"nothing":       jsonformat.Nothing().Schema,
		"boolean":       true,
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := strictSchemaToMap(schema)
			if err == nil || !strings.Contains(err.Error(), "<root>: root schema must have type object") {
				t.Fatalf("strictSchemaToMap() error = %v, want root-object error", err)
			}
		})
	}
}

func TestStrictSchemaToMapRejectsRootAnyOf(t *testing.T) {
	_, err := strictSchemaToMap(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
		"anyOf": []any{
			map[string]any{"type": "object", "additionalProperties": false},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "<root>: root schema must not use anyOf") {
		t.Fatalf("strictSchemaToMap() error = %v, want root-anyOf error", err)
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
