// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework/go/format/jsonformat"
)

func ExampleValue_string() {
	// Create a Value with a string
	value := jsonformat.MustNewValue("hello world", nil)

	// Marshal to JSON
	data, _ := json.Marshal(value)
	fmt.Printf("JSON: %s\n", string(data))

	// Unmarshal from JSON
	newValue := new(jsonformat.Value[string])
	_ = json.Unmarshal(data, newValue)
	fmt.Printf("Value: %s\n", newValue.Unwrap())

	// Output:
	// JSON: "hello world"
	// Value: hello world
}

func ExampleValue_int() {
	// Create a Value with an int
	value := jsonformat.MustNewValue(42, nil)

	// Marshal to JSON
	data, _ := json.Marshal(value)
	fmt.Printf("JSON: %s\n", string(data))

	// Unmarshal from JSON
	newValue := new(jsonformat.Value[int])
	_ = json.Unmarshal(data, newValue)
	fmt.Printf("Value: %d\n", newValue.Unwrap())

	// Output:
	// JSON: 42
	// Value: 42
}

func ExampleValue_slice() {
	// Create a Value with a slice
	value := jsonformat.MustNewValue([]string{"one", "two", "three"}, nil)

	// Marshal to JSON
	data, _ := json.Marshal(value)
	fmt.Printf("JSON: %s\n", string(data))

	// Unmarshal from JSON
	newValue := new(jsonformat.Value[[]string])
	_ = json.Unmarshal(data, newValue)
	fmt.Printf("Value: %v\n", newValue.Unwrap())

	// Output:
	// JSON: ["one","two","three"]
	// Value: [one two three]
}

func ExampleValue_map() {
	// Create a Value with a map
	value := jsonformat.MustNewValue(map[string]int{"count": 42, "total": 100}, nil)

	// Marshal to JSON
	data, _ := json.Marshal(value)

	// Note: Map order is not guaranteed in Go, so we just check it unmarshals correctly
	newValue := new(jsonformat.Value[map[string]int])
	_ = json.Unmarshal(data, newValue)
	result := newValue.Unwrap()

	fmt.Printf("count: %d, total: %d\n", result["count"], result["total"])

	// Output:
	// count: 42, total: 100
}
