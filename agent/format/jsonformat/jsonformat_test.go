// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/agent"
	legacyjsonformat "github.com/microsoft/agent-framework-go/agent/format/jsonformat"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
)

func TestConstructors(t *testing.T) {
	type output struct {
		Name string `json:"name"`
	}
	schema := &jsonschema.Schema{Type: "string"}
	generic, err := legacyjsonformat.For[output]()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		got, want format.ResponseFormat
	}{
		{"New", legacyjsonformat.New("string", "description", schema), jsonformat.New("string", "description", schema)},
		{"New nil schema", legacyjsonformat.New("any", "", nil), jsonformat.New("any", "", nil)},
		{"Any", legacyjsonformat.Any(), jsonformat.Any()},
		{"Nothing", legacyjsonformat.Nothing(), jsonformat.Nothing()},
		{"MustFor", legacyjsonformat.MustFor[output](), jsonformat.MustFor[output]()},
		{"For", generic, jsonformat.MustFor[output]()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Fatalf("format = %#v, want %#v", tc.got, tc.want)
			}
		})
	}

	for _, typ := range []reflect.Type{nil, reflect.TypeFor[output](), reflect.TypeFor[chan int]()} {
		got, gotErr := legacyjsonformat.ForType(typ)
		want, wantErr := jsonformat.ForType(typ)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("ForType(%v) error = %v, want %v", typ, gotErr, wantErr)
		}
		if gotErr != nil && gotErr.Error() != wantErr.Error() {
			t.Fatalf("ForType(%v) error = %v, want %v", typ, gotErr, wantErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ForType(%v) = %#v, want %#v", typ, got, want)
		}
	}
}

func TestCompatibility(t *testing.T) {
	if reflect.TypeFor[agent.ResponseFormat]() != reflect.TypeFor[format.ResponseFormat]() { //nolint:staticcheck // Verify the deprecated type remains an alias.
		t.Fatal("agent.ResponseFormat is not an alias of format.ResponseFormat")
	}
	if reflect.TypeFor[legacyjsonformat.Format]() != reflect.TypeFor[jsonformat.Format]() {
		t.Fatal("legacy Format is not an alias of jsonformat.Format")
	}
	legacy := legacyjsonformat.MustFor[int]()
	got, ok := agent.GetOption([]agent.Option{agent.WithResponseFormat(legacy)}, agent.WithResponseFormat)
	if !ok || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("response format option = (%#v, %v), want %#v", got, ok, legacy)
	}

	decoder, err := legacyjsonformat.FromResponseFormat(got)
	if err != nil {
		t.Fatal(err)
	}
	data, err := decoder.Marshal(42)
	if err != nil {
		t.Fatal(err)
	}
	var value int
	if err := decoder.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Normalize(&value); err != nil {
		t.Fatal(err)
	}
	if value != 42 {
		t.Fatalf("decoded value = %d, want 42", value)
	}

	wrapped, err := legacyjsonformat.WrapNonObjectSchema(legacy)
	if err != nil {
		t.Fatal(err)
	}
	wantWrapped, err := jsonformat.WrapNonObjectSchema(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wrapped, wantWrapped) {
		t.Fatalf("wrapped format = %#v, want %#v", wrapped, wantWrapped)
	}
	decoder, err = legacyjsonformat.FromResponseFormat(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	value = 0
	if err := decoder.UnmarshalStructuredOutput([]byte(`{"data":42}`), &value); err != nil {
		t.Fatal(err)
	}
	if value != 42 {
		t.Fatalf("structured output = %d, want 42", value)
	}
}

func TestErrors(t *testing.T) {
	if _, err := legacyjsonformat.For[chan int](); err == nil {
		t.Fatal("For[chan int]() succeeded, want error")
	}
	for _, invalid := range []format.ResponseFormat{
		{Kind: "text"},
		{Kind: "json", Schema: "invalid"},
		{Kind: "json", Schema: (*jsonschema.Schema)(nil)},
	} {
		gotDecoder, gotErr := legacyjsonformat.FromResponseFormat(invalid)
		_, wantErr := jsonformat.FromResponseFormat(invalid)
		if gotDecoder != nil || gotErr == nil || wantErr == nil || gotErr.Error() != wantErr.Error() {
			t.Fatalf("FromResponseFormat(%#v) = (%v, %v), want (nil, %v)", invalid, gotDecoder, gotErr, wantErr)
		}
		got, gotErr := legacyjsonformat.WrapNonObjectSchema(invalid)
		want, wantErr := jsonformat.WrapNonObjectSchema(invalid)
		if gotErr == nil || wantErr == nil || gotErr.Error() != wantErr.Error() || !reflect.DeepEqual(got, want) {
			t.Fatalf("WrapNonObjectSchema(%#v) = (%#v, %v), want (%#v, %v)", invalid, got, gotErr, want, wantErr)
		}
	}
}

func TestMustForPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustFor[chan int]() did not panic")
		}
	}()
	legacyjsonformat.MustFor[chan int]()
}
