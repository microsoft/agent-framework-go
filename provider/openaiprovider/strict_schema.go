// Copyright (c) Microsoft. All rights reserved.

package openaiprovider

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const strictSchemaTransformCacheLimit = 256

var strictSchemaCache strictSchemaTransformCache

var unsupportedStrictSchemaKeywords = [...]string{
	"contentEncoding",
	"contentMediaType",
	"not",
	"minLength",
	"maxLength",
	"pattern",
	"format",
	"minimum",
	"maximum",
	"multipleOf",
	"patternProperties",
	"minItems",
	"maxItems",
	"unevaluatedProperties",
	"propertyNames",
	"minProperties",
	"maxProperties",
	"unevaluatedItems",
	"contains",
	"minContains",
	"maxContains",
	"uniqueItems",
}

type strictSchemaTransformCache struct {
	mu      sync.RWMutex
	entries map[[sha256.Size]byte][]byte
}

func strictSchemaToMap(schema any) (map[string]any, error) {
	return strictSchemaCache.transform(schema)
}

func (c *strictSchemaTransformCache) transform(schema any) (map[string]any, error) {
	source, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(source)
	if transformed, ok := c.load(key); ok {
		return decodeStrictSchemaMap(transformed)
	}

	var schemaValue any
	if err := json.Unmarshal(source, &schemaValue); err != nil {
		return nil, err
	}
	schemaValue, err = transformStrictSchema(schemaValue, nil)
	if err != nil {
		return nil, err
	}
	schemaMap, ok := schemaValue.(map[string]any)
	if !ok {
		return nil, strictSchemaError(nil, "schema must be an object or boolean")
	}
	transformed, err := json.Marshal(schemaMap)
	if err != nil {
		return nil, err
	}
	return decodeStrictSchemaMap(c.store(key, transformed))
}

func (c *strictSchemaTransformCache) load(key [sha256.Size]byte) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, ok := c.entries[key]
	return value, ok
}

func (c *strictSchemaTransformCache) store(key [sha256.Size]byte, value []byte) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.entries[key]; ok {
		return cached
	}
	if c.entries == nil {
		c.entries = make(map[[sha256.Size]byte][]byte)
	}
	if len(c.entries) >= strictSchemaTransformCacheLimit {
		for entry := range c.entries {
			delete(c.entries, entry)
			break
		}
	}
	c.entries[key] = value
	return value
}

func decodeStrictSchemaMap(data []byte) (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	return schema, nil
}

func transformStrictSchema(value any, path []string) (any, error) {
	switch schema := value.(type) {
	case bool:
		if schema {
			return map[string]any{}, nil
		}
		// Match .NET's boolean conversion before its unsupported-keyword cleanup.
		converted := map[string]any{"not": true}
		if err := cleanStrictSchemaNode(converted, path); err != nil {
			return nil, err
		}
		return converted, nil
	case map[string]any:
		if err := transformStrictSchemaObject(schema, path); err != nil {
			return nil, err
		}
		return schema, nil
	default:
		return nil, strictSchemaError(path, "schema must be an object or boolean")
	}
}

func transformStrictSchemaObject(schema map[string]any, path []string) error {
	properties, hasProperties, err := strictSchemaProperties(schema, path)
	if err != nil {
		return err
	}

	if required, ok := schema["required"]; ok {
		requiredNames, ok := required.([]any)
		if !ok {
			return strictSchemaError(path, "required must be an array of property names")
		}
		for _, value := range requiredNames {
			name, ok := value.(string)
			if !ok {
				return strictSchemaError(path, "required must contain only property names")
			}
			if _, ok := properties[name]; !ok {
				return strictSchemaError(path, "required property %q is not declared in properties", name)
			}
		}
	}

	if additionalProperties, ok := schema["additionalProperties"]; ok {
		closed, isBoolean := additionalProperties.(bool)
		if !isBoolean || closed {
			return strictSchemaError(path, "additionalProperties must be false")
		}
	}
	if strictSchemaHasType(schema, "object") && !hasProperties {
		if _, ok := schema["additionalProperties"]; !ok {
			return strictSchemaError(path, "object schema must declare properties or set additionalProperties to false")
		}
	}

	for name, property := range properties {
		transformed, err := transformStrictSchema(property, strictSchemaPath(path, "properties", name))
		if err != nil {
			return err
		}
		properties[name] = transformed
	}
	if item, ok := schema["items"]; ok {
		transformed, err := transformStrictSchema(item, strictSchemaPath(path, "items"))
		if err != nil {
			return err
		}
		schema["items"] = transformed
	}
	if not, ok := schema["not"]; ok {
		transformed, err := transformStrictSchema(not, strictSchemaPath(path, "not"))
		if err != nil {
			return err
		}
		schema["not"] = transformed
	}
	for _, keyword := range []string{"prefixItems", "anyOf", "allOf", "oneOf"} {
		value, ok := schema[keyword]
		if !ok {
			continue
		}
		subschemas, ok := value.([]any)
		if !ok {
			return strictSchemaError(append(path, keyword), "must be an array of schemas")
		}
		for index, subschema := range subschemas {
			transformed, err := transformStrictSchema(subschema, strictSchemaPath(path, keyword, fmt.Sprintf("[%d]", index)))
			if err != nil {
				return err
			}
			subschemas[index] = transformed
		}
	}
	for _, keyword := range []string{"$defs", "definitions"} {
		value, ok := schema[keyword]
		if !ok {
			continue
		}
		definitions, ok := value.(map[string]any)
		if !ok {
			return strictSchemaError(append(path, keyword), "must be an object of schemas")
		}
		for name, definition := range definitions {
			transformed, err := transformStrictSchema(definition, strictSchemaPath(path, keyword, name))
			if err != nil {
				return err
			}
			definitions[name] = transformed
		}
	}

	if hasProperties {
		if _, ok := schema["additionalProperties"]; !ok {
			schema["additionalProperties"] = false
		}

		required := make([]any, 0, len(properties))
		seen := make(map[string]bool, len(properties))
		if original, ok := schema["required"].([]any); ok {
			for _, value := range original {
				name := value.(string)
				if !seen[name] {
					required = append(required, name)
					seen[name] = true
				}
			}
		}
		missing := make([]string, 0, len(properties)-len(required))
		for name := range properties {
			if !seen[name] {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		for _, name := range missing {
			required = append(required, name)
		}
		schema["required"] = required
	}

	return cleanStrictSchemaNode(schema, path)
}

func cleanStrictSchemaNode(schema map[string]any, path []string) error {
	if defaultValue, ok := schema["default"]; ok {
		encoded, err := json.Marshal(defaultValue)
		if err != nil {
			return strictSchemaError(path, "encoding default: %v", err)
		}
		defaultDescription := "Default value: " + string(encoded)
		if description, ok := schema["description"]; ok && description != nil {
			descriptionText, ok := description.(string)
			if !ok {
				return strictSchemaError(path, "description must be a string")
			}
			schema["description"] = descriptionText + " (" + defaultDescription + ")"
		} else {
			schema["description"] = defaultDescription
		}
		delete(schema, "default")
	}

	constraints := make([]string, 0)
	for _, keyword := range unsupportedStrictSchemaKeywords {
		value, ok := schema[keyword]
		if !ok {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return strictSchemaError(path, "encoding %s: %v", keyword, err)
		}
		constraints = append(constraints, keyword+": "+string(encoded))
		delete(schema, keyword)
	}
	if len(constraints) > 0 {
		constraintDescription := strings.Join(constraints, "\n")
		if description, ok := schema["description"].(string); ok {
			schema["description"] = description + "\n" + constraintDescription
		} else {
			schema["description"] = constraintDescription
		}
	}
	return nil
}

func strictSchemaProperties(schema map[string]any, path []string) (map[string]any, bool, error) {
	value, ok := schema["properties"]
	if !ok {
		return nil, false, nil
	}
	properties, ok := value.(map[string]any)
	if !ok {
		return nil, false, strictSchemaError(path, "properties must be an object")
	}
	return properties, true, nil
}

func strictSchemaHasType(schema map[string]any, want string) bool {
	switch value := schema["type"].(type) {
	case string:
		return value == want
	case []any:
		for _, item := range value {
			if item == want {
				return true
			}
		}
	}
	return false
}

func strictSchemaPath(path []string, elements ...string) []string {
	result := make([]string, 0, len(path)+len(elements))
	result = append(result, path...)
	return append(result, elements...)
}

func strictSchemaError(path []string, format string, args ...any) error {
	location := "<root>"
	if len(path) > 0 {
		location = strings.Join(path, "/")
	}
	return fmt.Errorf("strict JSON schema at %s: %s", location, fmt.Sprintf(format, args...))
}
