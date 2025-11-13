// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework/go/format/jsonformat"
)

func TestNewValue(t *testing.T) {
	t.Run("SimpleStruct", func(t *testing.T) {
		initial := SimpleStruct{Name: "John", Age: 30, Email: "john@example.com"}
		value, err := jsonformat.NewValue(initial, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(initial, value.Unwrap()) {
			t.Fatalf("expected: %v, got: %v", initial, value.Unwrap())
		}
	})

	t.Run("ZeroValue", func(t *testing.T) {
		value, err := jsonformat.NewValue(SimpleStruct{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(SimpleStruct{}, value.Unwrap()) {
			t.Fatalf("expected: %v, got: %v", SimpleStruct{}, value.Unwrap())
		}
	})

	t.Run("WithOptions", func(t *testing.T) {
		opts := &jsonformat.Options{
			Name:        "TestValue",
			Description: "Test description",
		}
		value, err := jsonformat.NewValue(SimpleStruct{Name: "Test"}, opts)
		if err != nil {
			t.Fatal(err)
		}

		format, err := value.FormatJSON()
		if err != nil {
			t.Fatal(err)
		}
		if format.Name != "TestValue" {
			t.Fatalf("expected: %v, got: %v", "TestValue", format.Name)
		}
		if format.Description != "Test description" {
			t.Fatalf("expected: %v, got: %v", "Test description", format.Description)
		}
	})

	t.Run("AnyTypeWithNilValue", func(t *testing.T) {
		value, err := jsonformat.NewValue[any](nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if value == nil {
			t.Fatal("expected non-nil value, got nil")
		}
	})

	t.Run("AnyTypeWithNonNilValue", func(t *testing.T) {
		value, err := jsonformat.NewValue[any]("something", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if value != nil {
			t.Fatalf("expected nil, got: %v", value)
		}
		want := "cannot create Value[any] with non-nil value"
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got: %q", want, err.Error())
		}
	})

	t.Run("PointerType", func(t *testing.T) {
		name := "test"
		ps := &PointerStruct{Value: &name}
		value, err := jsonformat.NewValue(ps, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ps, value.Unwrap()) {
			t.Fatalf("expected: %v, got: %v", ps, value.Unwrap())
		}
	})
}

func TestMustNewValue(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		assertNotPanics(t, func() {
			jsonformat.MustNewValue(SimpleStruct{Name: "Test"}, nil)
		})
	})

	t.Run("Panic", func(t *testing.T) {
		assertPanics(t, func() {
			_ = jsonformat.MustNewValue[any]("invalid", nil)
		})
	})
}

func TestValueWrapUnwrap(t *testing.T) {
	value := jsonformat.MustNewValue(SimpleStruct{Name: "Initial"}, nil)

	t.Run("InitialValue", func(t *testing.T) {
		got := value.Unwrap()
		if got.Name != "Initial" {
			t.Fatalf("expected: %v, got: %v", "Initial", got.Name)
		}
	})

	t.Run("WrapNewValue", func(t *testing.T) {
		newVal := SimpleStruct{Name: "Updated", Age: 25}
		value.Wrap(newVal)
		got := value.Unwrap()
		if !reflect.DeepEqual(newVal, got) {
			t.Fatalf("expected: %v, got: %v", newVal, got)
		}
	})
}

func TestValueFormat(t *testing.T) {
	value := jsonformat.MustNewValue(SimpleStruct{}, nil)
	format, err := value.FormatJSON()
	if err != nil {
		t.Fatal(err)
	}
	if format.Name != "SimpleStruct" {
		t.Fatalf("expected: %v, got: %v", "SimpleStruct", format.Name)
	}
}

func TestValueMustFormat(t *testing.T) {
	value := jsonformat.MustNewValue(SimpleStruct{}, nil)
	assertNotPanics(t, func() {
		value.MustFormat()
	})
}

func TestValueMarshalJSON(t *testing.T) {
	t.Run("SimpleStruct", func(t *testing.T) {
		testRoundtrip(t, SimpleStruct{Name: "John", Age: 30, Email: "john@example.com"})
	})

	t.Run("ZeroValue", func(t *testing.T) {
		testRoundtrip(t, SimpleStruct{})
	})

	t.Run("NestedStruct", func(t *testing.T) {
		testRoundtrip(t, NestedStruct{
			ID: "123",
			Person: SimpleStruct{
				Name: "Jane",
				Age:  25,
			},
			Active: true,
		})
	})

	t.Run("PointerTypeWithNil", func(t *testing.T) {
		// Note: marshaling nil pointer and unmarshaling creates zero value, not nil
		var ps *PointerStruct
		value, err := jsonformat.NewValue(ps, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = json.Marshal(value); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("PointerTypeWithValue", func(t *testing.T) {
		name := "test"
		ps := &PointerStruct{Value: &name}
		testRoundtrip(t, ps)
	})
}

func TestValueUnmarshalJSON(t *testing.T) {
	t.Run("SimpleStruct", func(t *testing.T) {
		input := `{"name":"Alice","age":28,"email":"alice@example.com"}`
		value := &jsonformat.Value[SimpleStruct]{}

		err := json.Unmarshal([]byte(input), value)
		if err != nil {
			t.Fatal(err)
		}

		expected := SimpleStruct{Name: "Alice", Age: 28, Email: "alice@example.com"}
		if !reflect.DeepEqual(expected, value.Unwrap()) {
			t.Fatalf("expected: %v, got: %v", expected, value.Unwrap())
		}
	})

	t.Run("PartialData", func(t *testing.T) {
		input := `{"name":"Bob","age":35}`
		value := &jsonformat.Value[SimpleStruct]{}

		err := json.Unmarshal([]byte(input), value)
		if err != nil {
			t.Fatal(err)
		}

		expected := SimpleStruct{Name: "Bob", Age: 35, Email: ""}
		if !reflect.DeepEqual(expected, value.Unwrap()) {
			t.Fatalf("expected: %v, got: %v", expected, value.Unwrap())
		}
	})

	t.Run("NestedStruct", func(t *testing.T) {
		input := `{"id":"456","person":{"name":"Charlie","age":40},"active":false}`
		value := &jsonformat.Value[NestedStruct]{}

		err := json.Unmarshal([]byte(input), value)
		if err != nil {
			t.Fatal(err)
		}

		expected := NestedStruct{
			ID:     "456",
			Person: SimpleStruct{Name: "Charlie", Age: 40},
			Active: false,
		}
		if !reflect.DeepEqual(expected, value.Unwrap()) {
			t.Fatalf("expected: %v, got: %v", expected, value.Unwrap())
		}
	})

	t.Run("ZeroValueStruct", func(t *testing.T) {
		input := `{"name":"","age":0}`
		value := &jsonformat.Value[SimpleStruct]{}

		err := json.Unmarshal([]byte(input), value)
		if err != nil {
			t.Fatal(err)
		}

		expected := SimpleStruct{Name: "", Age: 0}
		if !reflect.DeepEqual(expected, value.Unwrap()) {
			t.Fatalf("expected: %v, got: %v", expected, value.Unwrap())
		}
	})
}

func TestValueRoundtrip(t *testing.T) {
	t.Run("SimpleStruct", func(t *testing.T) {
		testRoundtrip(t, SimpleStruct{Name: "David", Age: 45, Email: "david@example.com"})
	})

	t.Run("NestedStruct", func(t *testing.T) {
		testRoundtrip(t, NestedStruct{
			ID: "789",
			Person: SimpleStruct{
				Name:  "Eve",
				Age:   32,
				Email: "eve@example.com",
			},
			Active: true,
		})
	})

	t.Run("PointerStruct", func(t *testing.T) {
		count := 5
		name := "pointer test"
		testRoundtrip(t, PointerStruct{
			Value: &name,
			Count: &count,
		})
	})
}

func testRoundtrip[T any](t *testing.T, original T) {
	t.Helper()

	// Create a Value with the original data
	value, err := jsonformat.NewValue(original, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the value matches
	if !reflect.DeepEqual(original, value.Unwrap()) {
		t.Fatalf("expected: %v, got: %v", original, value.Unwrap())
	}

	// Marshal to JSON
	data, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshal back
	newValue := new(jsonformat.Value[T])
	err = newValue.UnmarshalJSON(data)
	if err != nil {
		t.Fatal(err)
	}

	// Compare using reflect.DeepEqual
	result := newValue.Unwrap()
	if !reflect.DeepEqual(original, result) {
		t.Fatalf("expected: %v, got: %v", original, result)
	}
}

func TestValueWithOptions(t *testing.T) {
	t.Run("CustomNameAndDescription", func(t *testing.T) {
		opts := &jsonformat.Options{
			Name:        "CustomStruct",
			Description: "This is a custom struct",
		}
		value := jsonformat.MustNewValue(SimpleStruct{Name: "Test"}, opts)

		format, err := value.FormatJSON()
		if err != nil {
			t.Fatal(err)
		}
		if format.Name != "CustomStruct" {
			t.Fatalf("expected: %v, got: %v", "CustomStruct", format.Name)
		}
		if format.Description != "This is a custom struct" {
			t.Fatalf("expected: %v, got: %v", "This is a custom struct", format.Description)
		}
	})

	t.Run("StrictMode", func(t *testing.T) {
		opts := &jsonformat.Options{
			Strict: true,
		}
		value := jsonformat.MustNewValue(SimpleStruct{Name: "Test"}, opts)

		format, err := value.FormatJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !format.Strict {
			t.Fatal("expected Strict to be true")
		}
	})
}

func TestValueMarshalNilPointer(t *testing.T) {
	// Test marshaling a nil pointer value
	var ps *PointerStruct
	value, err := jsonformat.NewValue(ps, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := json.Marshal(value); err != nil {
		t.Fatal(err)
	}
}

func TestValueLazyInit(t *testing.T) {
	// Test that Format is lazily initialized
	value := &jsonformat.Value[SimpleStruct]{}

	// Before any operation, format should not be initialized
	// First call to Format should initialize it
	format1, err := value.Format()
	if err != nil {
		t.Fatal(err)
	}

	// Second call should return the same format
	format2, err := value.Format()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(format1, format2) {
		t.Fatalf("expected: %v, got: %v", format1, format2)
	}
}

func TestValueUnmarshalInvalidJSON(t *testing.T) {
	t.Run("InvalidJSON", func(t *testing.T) {
		input := `{"name":"test","age":"not a number"}`
		value := &jsonformat.Value[SimpleStruct]{}

		err := json.Unmarshal([]byte(input), value)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("MalformedJSON", func(t *testing.T) {
		input := `{invalid json}`
		value := &jsonformat.Value[SimpleStruct]{}

		err := json.Unmarshal([]byte(input), value)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestValueMarshalAfterWrap(t *testing.T) {
	value := jsonformat.MustNewValue(SimpleStruct{Name: "Initial", Age: 20}, nil)

	// Marshal initial value
	data1, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	// Wrap with new value
	value.Wrap(SimpleStruct{Name: "Updated", Age: 30})

	// Marshal wrapped value
	data2, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshal both and compare
	var result1, result2 SimpleStruct
	if err := json.Unmarshal(data1, &result1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data2, &result2); err != nil {
		t.Fatal(err)
	}

	expected1 := SimpleStruct{Name: "Initial", Age: 20}
	expected2 := SimpleStruct{Name: "Updated", Age: 30}
	if !reflect.DeepEqual(expected1, result1) {
		t.Fatalf("expected: %v, got: %v", expected1, result1)
	}
	if !reflect.DeepEqual(expected2, result2) {
		t.Fatalf("expected: %v, got: %v", expected2, result2)
	}
}

func TestComplexNestedStructure(t *testing.T) {
	type Address struct {
		Street  string `json:"street"`
		City    string `json:"city"`
		ZipCode string `json:"zipCode"`
	}

	type Contact struct {
		Email   string  `json:"email"`
		Phone   string  `json:"phone"`
		Address Address `json:"address"`
	}

	type Employee struct {
		ID      string  `json:"id"`
		Name    string  `json:"name"`
		Contact Contact `json:"contact"`
		Active  bool    `json:"active"`
	}

	testRoundtrip(t, Employee{
		ID:   "emp123",
		Name: "John Doe",
		Contact: Contact{
			Email: "john@example.com",
			Phone: "+1234567890",
			Address: Address{
				Street:  "123 Main St",
				City:    "Springfield",
				ZipCode: "12345",
			},
		},
		Active: true,
	})
}

func TestSliceAndMapTypes(t *testing.T) {
	type DataStruct struct {
		Tags     []string          `json:"tags"`
		Metadata map[string]string `json:"metadata"`
	}

	testRoundtrip(t, DataStruct{
		Tags: []string{"tag1", "tag2", "tag3"},
		Metadata: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	})
}

func TestBuiltinTypes(t *testing.T) {
	t.Run("String", func(t *testing.T) {
		testRoundtrip(t, "hello world")
		testRoundtrip(t, "")
		testRoundtrip(t, "special chars: !@#$%^&*()")
	})

	t.Run("Int", func(t *testing.T) {
		testRoundtrip(t, 0)
		testRoundtrip(t, 42)
		testRoundtrip(t, -100)
		testRoundtrip(t, 999999)
	})

	t.Run("Int64", func(t *testing.T) {
		testRoundtrip(t, int64(0))
		testRoundtrip(t, int64(123456789012345))
		testRoundtrip(t, int64(-123456789012345))
	})

	t.Run("Float64", func(t *testing.T) {
		testRoundtrip(t, 0.0)
		testRoundtrip(t, 3.14159)
		testRoundtrip(t, -273.15)
		testRoundtrip(t, 1.23e10)
	})

	t.Run("Bool", func(t *testing.T) {
		testRoundtrip(t, true)
		testRoundtrip(t, false)
	})

	t.Run("Uint", func(t *testing.T) {
		testRoundtrip(t, uint(0))
		testRoundtrip(t, uint(42))
		testRoundtrip(t, uint(4294967295))
	})
}

func TestBuiltinValueOperations(t *testing.T) {
	t.Run("WrapUnwrapString", func(t *testing.T) {
		value := jsonformat.MustNewValue("initial", nil)
		if value.Unwrap() != "initial" {
			t.Fatalf("expected: %v, got: %v", "initial", value.Unwrap())
		}

		value.Wrap("updated")
		if value.Unwrap() != "updated" {
			t.Fatalf("expected: %v, got: %v", "updated", value.Unwrap())
		}
	})

	t.Run("WrapUnwrapInt", func(t *testing.T) {
		value := jsonformat.MustNewValue(10, nil)
		if value.Unwrap() != 10 {
			t.Fatalf("expected: %v, got: %v", 10, value.Unwrap())
		}

		value.Wrap(20)
		if value.Unwrap() != 20 {
			t.Fatalf("expected: %v, got: %v", 20, value.Unwrap())
		}
	})

	t.Run("FormatString", func(t *testing.T) {
		value := jsonformat.MustNewValue("test", nil)
		format := value.MustFormat()
		if format.Kind() != "json" {
			t.Fatalf("expected: %v, got: %v", "json", format.Kind())
		}
	})

	t.Run("FormatInt", func(t *testing.T) {
		value := jsonformat.MustNewValue(42, nil)
		format := value.MustFormat()
		if format.Kind() != "json" {
			t.Fatalf("expected: %v, got: %v", "json", format.Kind())
		}
	})
}

func TestBuiltinSliceTypes(t *testing.T) {
	t.Run("StringSlice", func(t *testing.T) {
		original := []string{"one", "two", "three"}
		testRoundtrip(t, original)
	})

	t.Run("IntSlice", func(t *testing.T) {
		original := []int{1, 2, 3, 4, 5}
		testRoundtrip(t, original)
	})

	t.Run("BoolSlice", func(t *testing.T) {
		original := []bool{true, false, true}
		testRoundtrip(t, original)
	})

	t.Run("EmptySlice", func(t *testing.T) {
		original := []string{}
		testRoundtrip(t, original)
	})
}

func TestBuiltinMapTypes(t *testing.T) {
	t.Run("StringMap", func(t *testing.T) {
		original := map[string]string{
			"key1": "value1",
			"key2": "value2",
		}
		testRoundtrip(t, original)
	})

	t.Run("IntMap", func(t *testing.T) {
		original := map[string]int{
			"count": 42,
			"total": 100,
		}
		testRoundtrip(t, original)
	})

	t.Run("EmptyMap", func(t *testing.T) {
		original := map[string]string{}
		testRoundtrip(t, original)
	})
}

func TestBuiltinPointerTypes(t *testing.T) {
	t.Run("StringPointer", func(t *testing.T) {
		str := "test"
		value := jsonformat.MustNewValue(&str, nil)
		result := value.Unwrap()
		if *result != "test" {
			t.Fatalf("expected: %v, got: %v", "test", *result)
		}
	})

	t.Run("IntPointer", func(t *testing.T) {
		num := 42
		value := jsonformat.MustNewValue(&num, nil)
		result := value.Unwrap()
		if *result != 42 {
			t.Fatalf("expected: %v, got: %v", 42, *result)
		}
	})

	t.Run("NilPointer", func(t *testing.T) {
		var str *string
		value, err := jsonformat.NewValue(str, nil)
		if err != nil {
			t.Fatal(err)
		}
		result := value.Unwrap()
		if result != nil {
			t.Fatalf("expected nil, got: %v", result)
		}
	})
}
