// Copyright (c) Microsoft. All rights reserved.

package embedding_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/message"
)

type customVector []float32

func (v customVector) Dimensions() int { return len(v) }

func TestOf_Alias(t *testing.T) {
	original := &embedding.Of[float32]{
		Vector: embedding.VectorOf[float32]{1, 2, 3}, ModelID: "served-model",
	}
	if reflect.TypeFor[embedding.Of[float32]]() != reflect.TypeFor[embedding.Embedding[embedding.VectorOf[float32]]]() {
		t.Fatal("alias must have the same type identity")
	}
	original.Vector[0] = 42
	if original.Dimensions() != 3 {
		t.Fatal("alias must preserve vector access and methods")
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded embedding.Of[float32]
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, *original) {
		t.Fatalf("alias roundtrip = %+v, %v; want %+v", decoded, err, original)
	}
}

func TestGeneratedOf_Alias(t *testing.T) {
	if reflect.TypeFor[embedding.GeneratedOf[float32]]() != reflect.TypeFor[embedding.Generated[*embedding.Of[float32]]]() {
		t.Fatal("alias must have the same type identity")
	}
}

func TestEmbedding_Ctor_Roundtrips(t *testing.T) {
	vector := embedding.VectorOf[float32]{1, 2, 3}
	props := map[string]any{}
	created := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	e := &embedding.Of[float32]{
		Vector: vector, CreatedAt: created,
		ModelID: "text-embedding-3-small", AdditionalProperties: props,
	}
	if !slices.Equal(vector, e.Vector) ||
		e.ModelID != "text-embedding-3-small" || !e.CreatedAt.Equal(created) {
		t.Fatalf("embedding = %+v", e)
	}
	if e.Dimensions() != 3 {
		t.Fatalf("dimensions = %d", e.Dimensions())
	}
	vector[0] = 42
	props["key"] = "value"
	if e.Vector[0] != 42 || e.AdditionalProperties["key"] != "value" {
		t.Fatal("vector and metadata must be borrowed")
	}
	e.Vector = embedding.VectorOf[float32]{4, 5}
	if e.Dimensions() != 2 {
		t.Fatalf("dimensions after replacement = %d", e.Dimensions())
	}
}

func TestEmbedding_UnmarshalReplacesValue(t *testing.T) {
	for _, test := range []struct {
		data    string
		vector  embedding.Vector
		modelID string
	}{
		{`{}`, nil, ""},
		{`{"Vector":null}`, nil, ""},
		{`null`, embedding.VectorOf[float32]{1, 2, 3}, "original"},
		{`{"Type":"float32","Vector":[4,5]}`, embedding.VectorOf[float32]{4, 5}, ""},
		{`{"ModelID":"updated"}`, nil, "updated"},
	} {
		t.Run(test.data, func(t *testing.T) {
			e := embedding.Embedding[embedding.Vector]{
				Vector: embedding.VectorOf[float32]{1, 2, 3}, ModelID: "original",
			}
			if err := json.Unmarshal([]byte(test.data), &e); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(e.Vector, test.vector) || e.ModelID != test.modelID {
				t.Fatalf("decoded embedding = %+v", e)
			}
		})
	}
}

func TestEmbedding_SerializationRoundtrips(t *testing.T) {
	tests := []struct {
		name string
		e    embedding.Embedding[embedding.Vector]
		want string
	}{
		{"byte", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[uint8]{1, 2, 3}}, `{"Type":"uint8","Vector":"AQID"}`},
		{"sbyte", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[int8]{-1, 2, 3}}, `{"Type":"int8","Vector":[-1,2,3]}`},
		{"single", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[float32]{1, 2, 3}}, `{"Type":"float32","Vector":[1,2,3]}`},
		{"double", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[float64]{1, 2, 3}}, `{"Type":"float64","Vector":[1,2,3]}`},
		{"empty", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[float32]{}}, `{"Type":"float32","Vector":[]}`},
		{"empty bytes", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[uint8]{}}, `{"Type":"uint8","Vector":""}`},
		{"nil numeric", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[float32](nil)}, `{"Type":"float32","Vector":null}`},
		{"nil bytes", embedding.Embedding[embedding.Vector]{Vector: embedding.VectorOf[uint8](nil)}, `{"Type":"uint8","Vector":null}`},
		{"no vector", embedding.Embedding[embedding.Vector]{}, `{"Vector":null}`},
		{"empty properties", embedding.Embedding[embedding.Vector]{AdditionalProperties: map[string]any{}}, `{"AdditionalProperties":{},"Vector":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.e)
			if err != nil || string(data) != tt.want {
				t.Fatalf("Marshal = %s, %v; want %s", data, err, tt.want)
			}
			var result embedding.Embedding[embedding.Vector]
			err = json.Unmarshal(data, &result)
			if err != nil || !reflect.DeepEqual(result, tt.e) {
				t.Fatalf("Unmarshal = %+v, %v; want %+v", result, err, tt.e)
			}
		})
	}
}

func TestEmbedding_ZeroVector(t *testing.T) {
	e := &embedding.Embedding[embedding.Vector]{}
	if e.Dimensions() != 0 {
		t.Fatalf("dimensions = %d", e.Dimensions())
	}
	data, err := json.Marshal(e)
	if err != nil || string(data) != `{"Vector":null}` {
		t.Fatalf("Marshal = %s, %v", data, err)
	}
	if e.Vector != nil {
		t.Fatal("Marshal changed the vector")
	}
}

func TestEmbedding_TypedJSONRoundtrip(t *testing.T) {
	testTypedEmbeddingJSON(t, embedding.VectorOf[uint8]{1, 2, 3}, `{"Type":"uint8","Vector":"AQID"}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[int8]{-1, 2, 3}, `{"Type":"int8","Vector":[-1,2,3]}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[float32]{1, 2, 3}, `{"Type":"float32","Vector":[1,2,3]}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[float64]{1, 2, 3}, `{"Type":"float64","Vector":[1,2,3]}`)
	testTypedEmbeddingJSON(t, embedding.Binary{true, false}, `{"Type":"binary","Vector":"10"}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[float32](nil), `{"Type":"float32","Vector":null}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[float32]{}, `{"Type":"float32","Vector":[]}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[uint8](nil), `{"Type":"uint8","Vector":null}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[uint8]{}, `{"Type":"uint8","Vector":""}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[int8](nil), `{"Type":"int8","Vector":null}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[float64](nil), `{"Type":"float64","Vector":null}`)
	testTypedEmbeddingJSON(t, embedding.VectorOf[uint8]{158, 233, 101}, `{"Type":"uint8","Vector":"null"}`)
}

func testTypedEmbeddingJSON[TVector embedding.Vector](t *testing.T, vector TVector, want string) {
	t.Helper()
	t.Run(fmt.Sprintf("%T/%s", vector, want), func(t *testing.T) {
		e := embedding.Embedding[TVector]{Vector: vector}
		if e.Dimensions() != vector.Dimensions() {
			t.Fatalf("dimensions = %d, want %d", e.Dimensions(), vector.Dimensions())
		}
		data, err := json.Marshal(e)
		if err != nil || string(data) != want {
			t.Fatalf("Marshal = %s, %v; want %s", data, err, want)
		}
		var decoded embedding.Embedding[TVector]
		if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, e) {
			t.Fatalf("typed roundtrip = %+v, %v; want %+v", decoded, err, e)
		}
		var polymorphic embedding.Embedding[embedding.Vector]
		if err := json.Unmarshal(data, &polymorphic); err != nil || !reflect.DeepEqual(polymorphic.Vector, vector) {
			t.Fatalf("polymorphic decoding = %+v, %v", polymorphic, err)
		}
		e.ModelID = "served-model"
		e.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		e.AdditionalProperties = map[string]any{"source": "test"}
		type response struct {
			Value  embedding.Embedding[TVector]
			Values []embedding.Embedding[TVector]
			Result embedding.Generated[*embedding.Embedding[TVector]]
		}
		original := response{
			Value:  e,
			Values: []embedding.Embedding[TVector]{e},
			Result: embedding.Generated[*embedding.Embedding[TVector]]{
				Embeddings: []*embedding.Embedding[TVector]{&e, nil},
				Usage:      &message.UsageDetails{InputTokenCount: 42, TotalTokenCount: 42},
			},
		}
		data, err = json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var result response
		if err := json.Unmarshal(data, &result); err != nil || !reflect.DeepEqual(result, original) {
			t.Fatalf("nested typed roundtrip = %+v, %v; want %+v", result, err, original)
		}
	})
}

func TestEmbedding_TypedUnmarshalReplacesValue(t *testing.T) {
	for _, payload := range []string{`{}`, `{"Vector":null}`, `{"Type":"float32","Vector":null}`, `null`} {
		t.Run(payload, func(t *testing.T) {
			e := embedding.Of[float32]{
				Vector: embedding.VectorOf[float32]{1}, ModelID: "original",
			}
			if err := json.Unmarshal([]byte(payload), &e); err != nil {
				t.Fatal(err)
			}
			if payload == "null" {
				if e.ModelID != "original" || !slices.Equal(e.Vector, []float32{1}) {
					t.Fatalf("JSON null changed receiver: %+v", e)
				}
			} else if e.Vector != nil || e.ModelID != "" || e.Dimensions() != 0 {
				t.Fatalf("decoded embedding = %+v", e)
			}
		})
	}
}

func TestEmbedding_TypedUnmarshalRejectsIncompatibleVectors(t *testing.T) {
	for _, payload := range []string{
		`{"Type":"float64","Vector":[1,2,3]}`,
		`{"Type":"float64","Vector":null}`,
		`{"Type":"int8","Vector":[1,2,3]}`,
		`{"Type":"uint8","Vector":"AQID"}`,
		`{"Type":"binary","Vector":"101"}`,
		`{"Type":"float32","Vector":["bad"]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			e := embedding.Of[float32]{
				Vector: embedding.VectorOf[float32]{42}, ModelID: "original",
				AdditionalProperties: map[string]any{"key": "value"},
			}
			if err := json.Unmarshal([]byte(payload), &e); err == nil {
				t.Fatal("expected incompatible or invalid vector error")
			}
			if !slices.Equal(e.Vector, []float32{42}) || e.ModelID != "original" ||
				!reflect.DeepEqual(e.AdditionalProperties, map[string]any{"key": "value"}) {
				t.Fatalf("invalid payload changed receiver: %+v", e)
			}
		})
	}
	e := embedding.Embedding[customVector]{Vector: customVector{1}}
	if e.Dimensions() != 1 {
		t.Fatal("custom typed vectors should work in memory")
	}
	if _, err := json.Marshal(e); err == nil {
		t.Fatal("expected unsupported custom vector serialization error")
	}
	if err := json.Unmarshal([]byte(`{"Type":"float32","Vector":[2]}`), &e); err == nil || e.Vector[0] != 1 {
		t.Fatalf("custom vector decoding = %+v, %v", e, err)
	}
}

func TestEmbedding_UnmarshalRejectsNullNumericElements(t *testing.T) {
	testEmbeddingNullNumericElements[uint8](t, "uint8")
	testEmbeddingNullNumericElements[int8](t, "int8")
	testEmbeddingNullNumericElements[float32](t, "float32")
	testEmbeddingNullNumericElements[float64](t, "float64")
}

func testEmbeddingNullNumericElements[T ~uint8 | ~int8 | ~float32 | ~float64](t *testing.T, kind string) {
	t.Helper()
	for _, vector := range []string{`[null]`, `[null,2,3]`, `[1,null,3]`, `[1,2,null]`, `[1, null, null ]`} {
		for _, target := range []any{
			&embedding.Of[T]{
				Vector: embedding.VectorOf[T]{42}, ModelID: "original",
				CreatedAt:            time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				AdditionalProperties: map[string]any{"key": "value"},
			},
			&embedding.Embedding[embedding.Vector]{
				Vector: embedding.VectorOf[T]{42}, ModelID: "original",
				CreatedAt:            time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				AdditionalProperties: map[string]any{"key": "value"},
			},
		} {
			t.Run(fmt.Sprintf("%s/%T/%s", kind, target, vector), func(t *testing.T) {
				before, err := json.Marshal(target)
				if err != nil {
					t.Fatal(err)
				}
				payload := fmt.Sprintf(`{"Type":%q,"ModelID":"changed","CreatedAt":"2025-01-01T00:00:00Z","AdditionalProperties":{"new":"value"},"Vector":%s}`, kind, vector)
				if err := json.Unmarshal([]byte(payload), target); err == nil {
					t.Fatal("expected null numeric element error")
				}
				after, err := json.Marshal(target)
				if err != nil || string(after) != string(before) {
					t.Fatalf("invalid payload changed receiver: %s -> %s, %v", before, after, err)
				}
				valid := fmt.Sprintf(`{"Type":%q,"Vector":[1,0,3]}`, kind)
				if err := json.Unmarshal([]byte(valid), target); err != nil {
					t.Fatalf("valid numeric array was rejected: %v", err)
				}
				want, err := json.Marshal(embedding.Of[T]{Vector: embedding.VectorOf[T]{1, 0, 3}})
				if err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(target)
				if err != nil || string(got) != string(want) {
					t.Fatalf("valid numeric array = %s, %v; want %s", got, err, want)
				}
			})
		}
	}
}

func TestEmbedding_JsonDeserialization_KnownPayload(t *testing.T) {
	data := []byte(`{
		"Type":"float32", "vector":[1,2,3],
		"createdAt":"2024-01-01T00:00:00Z",
		"modelId":"text-embedding-ada-002",
		"additionalProperties":{"key":"val"}
	}`)
	var e embedding.Of[float32]
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(e.Vector, []float32{1, 2, 3}) ||
		e.ModelID != "text-embedding-ada-002" ||
		!e.CreatedAt.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) ||
		e.AdditionalProperties["key"] != "val" {
		t.Fatalf("embedding = %+v", e)
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Type":"float32","CreatedAt":"2024-01-01T00:00:00Z","ModelID":"text-embedding-ada-002","AdditionalProperties":{"key":"val"},"Vector":[1,2,3]}`
	if string(data) != want {
		t.Fatalf("Marshal = %s, want %s", data, want)
	}
	var roundtrip embedding.Of[float32]
	err = json.Unmarshal(data, &roundtrip)
	if err != nil || !reflect.DeepEqual(roundtrip, e) {
		t.Fatalf("metadata roundtrip = %+v, %v", roundtrip, err)
	}
}

func TestEmbedding_InvalidSerialization(t *testing.T) {
	for _, payload := range []string{
		`{"Type":"unknown"}`, `{"Type":"float16","Vector":[1]}`,
		`{"Type":42}`, `{`, `{"Type":"float32","Vector":["bad"]}`,
		`{"Type":"int8","Vector":[128]}`, `{"Type":"uint8","Vector":"!"}`,
		`{"Vector":[1,2]}`, `{"Type":"float32"}`, `{"Type":"binary"}`,
		`{"Type":"float32","CreatedAt":"bad","Vector":[1]}`, `[]`,
		`{"AdditionalProperties":{"new":"value"},"ModelID":"changed","Type":"float32","Vector":["bad"]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			e := embedding.Embedding[embedding.Vector]{
				ModelID: "original", Vector: embedding.VectorOf[float32]{42},
				AdditionalProperties: map[string]any{"key": "value"},
			}
			want := embedding.Embedding[embedding.Vector]{
				ModelID: "original", Vector: embedding.VectorOf[float32]{42},
				AdditionalProperties: map[string]any{"key": "value"},
			}
			if err := json.Unmarshal([]byte(payload), &e); err == nil {
				t.Fatal("expected decoding error")
			}
			if !reflect.DeepEqual(e, want) {
				t.Fatalf("invalid payload modified receiver: %+v", e)
			}
		})
	}
	for _, vector := range []embedding.Vector{embedding.VectorOf[int]{1}, customVector{1}} {
		e := embedding.Embedding[embedding.Vector]{Vector: vector}
		if e.Dimensions() != 1 {
			t.Fatal("custom vectors should work in memory")
		}
		if _, err := json.Marshal(e); err == nil {
			t.Fatal("expected unsupported vector type error")
		}
	}
	for _, vector := range []embedding.Vector{embedding.VectorOf[float32]{float32(math.NaN())}, embedding.VectorOf[float64]{math.Inf(1)}} {
		if _, err := json.Marshal(embedding.Embedding[embedding.Vector]{Vector: vector}); err == nil {
			t.Fatal("expected invalid numeric value error")
		}
	}
	if _, err := json.Marshal(embedding.Embedding[embedding.Vector]{AdditionalProperties: map[string]any{"unsupported": make(chan int)}}); err == nil {
		t.Fatal("expected invalid metadata error")
	}
}

func TestVectorOf_Serialization(t *testing.T) {
	for _, test := range []struct {
		vector embedding.Vector
		want   string
	}{
		{embedding.VectorOf[uint8]{1, 2, 3}, `"AQID"`},
		{embedding.VectorOf[int8]{-1, 2, 3}, `[-1,2,3]`},
		{embedding.VectorOf[float32]{1, 2, 3}, `[1,2,3]`},
		{embedding.VectorOf[float64]{1, 2, 3}, `[1,2,3]`},
		{embedding.VectorOf[int]{1, 2, 3}, `[1,2,3]`},
	} {
		t.Run(fmt.Sprintf("%T", test.vector), func(t *testing.T) {
			data, err := json.Marshal(test.vector)
			if err != nil || string(data) != test.want || test.vector.Dimensions() != 3 {
				t.Fatalf("Marshal = %s, %v; dimensions = %d", data, err, test.vector.Dimensions())
			}
		})
	}
}

func TestBinaryEmbedding_Properties_Roundtrips(t *testing.T) {
	bits := embedding.Binary{false, true, false, true}
	e := &embedding.Embedding[embedding.Binary]{Vector: bits}
	if e.ModelID != "" || !e.CreatedAt.IsZero() || e.AdditionalProperties != nil {
		t.Fatalf("default metadata = %+v", e)
	}
	bits[0] = true
	if !e.Vector[0] || e.Dimensions() != len(bits) {
		t.Fatal("binary vector must be borrowed")
	}
	e.Vector = embedding.Binary{true, false}
	e.ModelID = "text-embedding-3-small"
	e.CreatedAt = time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	e.AdditionalProperties = map[string]any{"key": "value"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Type":"binary","CreatedAt":"2022-01-01T00:00:00Z","ModelID":"text-embedding-3-small","AdditionalProperties":{"key":"value"},"Vector":"10"}`
	if string(data) != want {
		t.Fatalf("Marshal = %s, want %s", data, want)
	}
	var result embedding.Embedding[embedding.Binary]
	err = json.Unmarshal(data, &result)
	if err != nil || !reflect.DeepEqual(&result, e) {
		t.Fatalf("roundtrip = %+v, %v; want %+v", result, err, e)
	}
	if e.Dimensions() != 2 {
		t.Fatalf("dimensions = %d", e.Dimensions())
	}
}

func TestBinaryEmbedding_Serialization_Roundtrips(t *testing.T) {
	lengths := make([]int, 64)
	for i := range lengths {
		lengths[i] = i
	}
	lengths = append(lengths, 10_000)
	for _, length := range lengths {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			bits := make(embedding.Binary, length)
			var want strings.Builder
			for i := range bits {
				bits[i] = i%3 == 1
				if bits[i] {
					want.WriteByte('1')
				} else {
					want.WriteByte('0')
				}
			}
			e := &embedding.Embedding[embedding.Binary]{Vector: bits}
			data, err := json.Marshal(e)
			if err != nil || string(data) != `{"Type":"binary","Vector":"`+want.String()+`"}` {
				t.Fatalf("unexpected binary encoding: %v", err)
			}
			var result embedding.Embedding[embedding.Binary]
			err = json.Unmarshal(data, &result)
			if err != nil || !reflect.DeepEqual(&result, e) {
				t.Fatalf("binary roundtrip failed: %v", err)
			}
		})
	}
}

func TestBinaryEmbedding_Deserialization_SupportsEncodedBits(t *testing.T) {
	var e embedding.Embedding[embedding.Binary]
	if err := json.Unmarshal([]byte(`{"Type":"binary","vector":"\u0030\u0031\u0030\u0031\u0030\u0031"}`), &e); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(e.Vector, []bool{false, true, false, true, false, true}) {
		t.Fatalf("bits = %+v", e)
	}
}

func TestBinaryEmbedding_Deserialization_InvalidBinaryEmbedding(t *testing.T) {
	for _, data := range []string{
		`"\u0030\u0032"`, `"02"`, `" "`, `10101`, `"\u00e9"`, `null`, `{}`, `{`,
	} {
		t.Run(data, func(t *testing.T) {
			vector := embedding.Binary{true}
			if err := json.Unmarshal([]byte(data), &vector); err == nil {
				t.Fatal("expected decoding error")
			}
			if !slices.Equal(vector, []bool{true}) {
				t.Fatal("invalid input modified receiver")
			}
			e := embedding.Embedding[embedding.Binary]{Vector: embedding.Binary{true}, ModelID: "original"}
			if err := json.Unmarshal([]byte(`{"Type":"binary","Vector":`+data+`}`), &e); err == nil {
				t.Fatal("expected embedding decoding error")
			}
			if e.ModelID != "original" || !slices.Equal(e.Vector, []bool{true}) {
				t.Fatal("invalid input modified embedding receiver")
			}
		})
	}
}

func TestBinary_Empty(t *testing.T) {
	for _, vector := range []embedding.Binary{nil, {}} {
		if vector.Dimensions() != 0 {
			t.Fatal("expected zero dimensions")
		}
		data, err := json.Marshal(vector)
		if err != nil || string(data) != `""` {
			t.Fatalf("Marshal = %s, %v", data, err)
		}
		var result embedding.Binary
		if err := json.Unmarshal(data, &result); err != nil || result == nil || len(result) != 0 {
			t.Fatalf("Unmarshal = %v, %v", result, err)
		}
	}
}

func TestEmbedding_NestedJSONRoundtrip(t *testing.T) {
	vectors := []embedding.Vector{
		embedding.VectorOf[uint8]{1, 2, 3},
		embedding.VectorOf[int8]{-1, 2, 3},
		embedding.VectorOf[float32]{1, 2, 3},
		embedding.VectorOf[float64]{1, 2, 3},
		embedding.Binary{true, false, true},
		embedding.VectorOf[float32](nil),
		nil,
	}
	type response struct {
		Value    embedding.Embedding[embedding.Vector]
		Pointer  *embedding.Embedding[embedding.Vector]
		Values   []embedding.Embedding[embedding.Vector]
		Pointers []*embedding.Embedding[embedding.Vector]
		Result   embedding.Generated[*embedding.Embedding[embedding.Vector]]
	}
	want := response{
		Value:   embedding.Embedding[embedding.Vector]{ModelID: "test", Vector: vectors[0]},
		Pointer: &embedding.Embedding[embedding.Vector]{ModelID: "test", Vector: vectors[1]},
	}
	for _, vector := range vectors {
		e := embedding.Embedding[embedding.Vector]{
			Vector: vector, ModelID: "test",
			AdditionalProperties: map[string]any{"source": "test"},
			CreatedAt:            time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		want.Values = append(want.Values, e)
		want.Pointers = append(want.Pointers, &e)
	}
	want.Pointers = append(want.Pointers, nil)
	want.Result.Embeddings = want.Pointers
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got response
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("nested roundtrip = %+v, %v; want %+v", got, err, want)
	}
}

func TestGeneratedEmbeddings_Properties_Roundtrip(t *testing.T) {
	var result embedding.GeneratedOf[float32]
	if len(result.Embeddings) != 0 || result.Usage != nil || result.AdditionalProperties != nil {
		t.Fatalf("default result = %+v", result)
	}
	first := &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 2, 3}}
	second := &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{4, 5, 6}}
	result.Embeddings = append(result.Embeddings, first, second)
	usage := &message.UsageDetails{InputTokenCount: 42, TotalTokenCount: 42}
	result.Usage = usage
	props := map[string]any{"key": "value"}
	result.AdditionalProperties = props
	if len(result.Embeddings) != 2 || result.Embeddings[0] != first || result.Embeddings[1] != second || result.Usage != usage {
		t.Fatalf("result = %+v", result)
	}
	props["key"] = "changed"
	if result.AdditionalProperties["key"] != "changed" {
		t.Fatal("properties must be borrowed")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded embedding.GeneratedOf[float32]
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, result) {
		t.Fatalf("result roundtrip = %+v, %v", decoded, err)
	}
}

func TestGeneratedEmbeddings_CustomOutputTypeRoundtrip(t *testing.T) {
	want := embedding.Generated[embedding.VectorOf[float32]]{
		Embeddings: []embedding.VectorOf[float32]{{1, 2, 3}, {4, 5, 6}},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got embedding.Generated[embedding.VectorOf[float32]]
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("custom result roundtrip = %+v, %v", got, err)
	}
}

func ExampleEmbedding() {
	e := embedding.Of[float32]{
		ModelID: "text-embedding-3-small",
		Vector:  embedding.VectorOf[float32]{0.1, 0.2, 0.3},
	}
	data, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	var result embedding.Of[float32]
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}
	fmt.Println(result.Dimensions())
	// Output: 3
}
