// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework/go/format/jsonformat"
)

func TestEncodingRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		v    any
	}{
		{"Struct", Struct{Name: "Alice", Age: 30, Email: "alice@example.com"}},
		{"Struct", &Struct{Name: "Alice", Age: 30, Email: "alice@example.com"}},
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
			format, err := jsonformat.ForType(rt)
			if err != nil {
				t.Fatal(err)
			}

			data, err := jsonformat.Marshal(format, tt.v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var v2 any = reflect.New(rt).Interface()
			if err := jsonformat.Unmarshal(format, data, &v2); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got := reflect.ValueOf(v2).Elem().Interface()
			if !reflect.DeepEqual(tt.v, got) {
				t.Fatalf("expected: %+v, got: %+v", tt.v, got)
			}
		})
	}
}
