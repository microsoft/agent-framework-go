// Copyright (c) Microsoft. All rights reserved.

package providerservices_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

func TestVectorHelpers(t *testing.T) {
	testVectorHelpers(t, float32(1), float32(2), []float64{1, 2})
	testVectorHelpers(t, float64(1), float64(2), []float32{1, 2})
	testVectorHelpers(t, uint8(1), uint8(2), []int8{1, 2})
	testVectorHelpers(t, int8(1), int8(2), []uint8{1, 2})
	type namedFloat float32
	testVectorHelpers(t, namedFloat(1), namedFloat(2), []float32{1, 2})
	testVectorHelpers(t, "one", "two", []byte{1, 2})
}

func testVectorHelpers[T, U any](t *testing.T, first, second T, other []U) {
	t.Helper()
	type namedSlice []T
	t.Run(reflect.TypeFor[T]().String(), func(t *testing.T) {
		for _, test := range []struct {
			name      string
			value     any
			supported bool
			want      []T
		}{
			{"slice", []T{first, second}, true, []T{first, second}},
			{"vector", embedding.VectorOf[T]{first, second}, true, []T{first, second}},
			{"embedding", embedding.Of[T]{Vector: embedding.VectorOf[T]{first, second}}, true, []T{first, second}},
			{"embedding pointer", &embedding.Of[T]{Vector: embedding.VectorOf[T]{first, second}}, true, []T{first, second}},
			{"nil slice", []T(nil), true, nil},
			{"nil vector", embedding.VectorOf[T](nil), true, nil},
			{"empty embedding", embedding.Of[T]{}, true, nil},
			{"nil embedding pointer", (*embedding.Of[T])(nil), true, nil},
			{"pointer to empty embedding", &embedding.Of[T]{}, true, nil},
			{"empty slice", []T{}, true, []T{}},
			{"untyped nil", nil, false, nil},
			{"other slice", other, false, nil},
			{"other vector", embedding.VectorOf[U](other), false, nil},
			{"other embedding", embedding.Of[U]{Vector: other}, false, nil},
			{"other embedding pointer", &embedding.Of[U]{Vector: other}, false, nil},
			{"array", [2]T{first, second}, false, nil},
			{"named slice", namedSlice{first, second}, false, nil},
			{"scalar", first, false, nil},
		} {
			t.Run(test.name, func(t *testing.T) {
				if supported := providerservices.SupportsVector[T](reflect.TypeOf(test.value)); supported != test.supported {
					t.Fatalf("type support = %v, want %v", supported, test.supported)
				}
				vector, ok := providerservices.AsVector[T](test.value)
				if ok != test.supported || !reflect.DeepEqual(vector, test.want) {
					t.Fatalf("unwrapped vector = (%#v, %v), want (%#v, %v)", vector, ok, test.want, test.supported)
				}
				if len(vector) != 0 {
					vector[0] = second
					again, _ := providerservices.AsVector[T](test.value)
					if !reflect.DeepEqual(again[0], second) {
						t.Fatal("unwrapping cloned the vector")
					}
				}
			})
		}
	})
}

type batchEmbeddingRecord struct {
	ID   string `vectordata:"key"`
	Text string `vectordata:"vector,dimensions=2"`
}

func buildEmbeddingProperty[R any](t *testing.T, options *vectordata.Definition) *providerservices.VectorPropertyModel {
	t.Helper()
	model, err := buildModel[string, R](options)
	if err != nil {
		t.Fatal(err)
	}
	property, err := model.VectorProperty("")
	if err != nil {
		t.Fatal(err)
	}
	return property
}

func TestGenerateEmbeddings_RecordsAndMetadata(t *testing.T) {
	for _, test := range []struct {
		name    string
		build   func(*vectordata.Definition) (*providerservices.CollectionModel, error)
		records []any
	}{
		{"struct", buildModel[string, batchEmbeddingRecord], []any{
			batchEmbeddingRecord{ID: "one", Text: "first"}, batchEmbeddingRecord{ID: "two", Text: "second"},
		}},
		{"pointer", buildModel[string, *batchEmbeddingRecord], []any{
			&batchEmbeddingRecord{ID: "one", Text: "first"}, &batchEmbeddingRecord{ID: "two", Text: "second"},
		}},
		{"map", buildModel[string, map[string]any], []any{
			map[string]any{"ID": "one", "Text": "first", "text": "storage name"},
			map[string]any{"ID": "two", "Text": "second"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			calls := 0
			outputs := []*embedding.Of[float64]{
				{Vector: embedding.VectorOf[float64]{1, 2}}, {Vector: embedding.VectorOf[float64]{3, 4}},
			}
			usage := &message.UsageDetails{}
			metadata := map[string]any{"request": "one"}
			generator := &bindingGenerator[string, *embedding.Of[float64]]{
				generate: func(got context.Context, values []string, options *embedding.Options) (*embedding.GeneratedOf[float64], error) {
					calls++
					if got != ctx || options != nil || !slices.Equal(values, []string{"first", "second"}) {
						t.Fatalf("generation inputs = %v, options=%v, same context=%v", values, options, got == ctx)
					}
					return &embedding.GeneratedOf[float64]{
						Embeddings: outputs, Usage: usage, AdditionalProperties: metadata,
					}, nil
				},
			}
			model, err := test.build(&vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator), Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.VectorProperty{Name: "Text", Type: reflect.TypeFor[string](), Dimensions: 2, StorageName: "text"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			property, err := model.VectorProperty("")
			if err != nil {
				t.Fatal(err)
			}
			result, err := providerservices.GenerateEmbeddings(ctx, property, test.records)
			if err != nil || result == nil || len(result.Embeddings) != 2 || calls != 1 {
				t.Fatalf("generation = %+v, %v; calls=%d", result, err, calls)
			}
			if result.Embeddings[0] != outputs[0] || result.Embeddings[1] != outputs[1] || result.Usage != usage {
				t.Fatal("generation did not preserve embeddings and usage")
			}
			result.AdditionalProperties["shared"] = true
			if metadata["shared"] != true {
				t.Fatal("generation cloned additional properties")
			}
			for i, record := range test.records {
				value, found, err := property.Value(record)
				if err != nil || !found || value != []string{"first", "second"}[i] {
					t.Fatalf("input record changed: %v, %v, %v", value, found, err)
				}
			}
		})
	}
}

func TestGenerateEmbeddings_EmptyBatches(t *testing.T) {
	for name, records := range map[string][]batchEmbeddingRecord{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			generator := &bindingGenerator[string, []float32]{
				generate: func(_ context.Context, values []string, _ *embedding.Options) (*embedding.Generated[[]float32], error) {
					calls++
					if len(values) != 0 || (values == nil) != (records == nil) {
						t.Fatalf("empty input = %#v", values)
					}
					return &embedding.Generated[[]float32]{}, nil
				},
			}
			property := buildEmbeddingProperty[batchEmbeddingRecord](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
			result, err := providerservices.GenerateEmbeddings(t.Context(), property, records)
			if err != nil || result == nil || len(result.Embeddings) != 0 || calls != 1 {
				t.Fatalf("empty generation = %+v, %v; calls=%d", result, err, calls)
			}
		})
	}
}

func TestGenerateEmbeddings_TypedNilAndOutputPolicies(t *testing.T) {
	type record struct {
		ID   string  `vectordata:"key"`
		Text *string `vectordata:"vector,dimensions=2"`
	}
	text := new("value")
	output := &embedding.Of[float64]{Vector: embedding.VectorOf[float64]{1}}
	generator := &bindingGenerator[*string, *embedding.Of[float64]]{
		generate: func(_ context.Context, values []*string, _ *embedding.Options) (*embedding.GeneratedOf[float64], error) {
			if len(values) != 2 || values[0] != nil || values[1] != text {
				t.Fatalf("typed nullable inputs = %v", values)
			}
			return &embedding.GeneratedOf[float64]{Embeddings: []*embedding.Of[float64]{nil, output}}, nil
		},
	}
	property := buildEmbeddingProperty[record](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
	result, err := providerservices.GenerateEmbeddings(t.Context(), property, []record{{ID: "nil"}, {ID: "present", Text: text}})
	if err != nil || result == nil || len(result.Embeddings) != 2 ||
		result.Embeddings[0] != (*embedding.Of[float64])(nil) || result.Embeddings[1] != output {
		t.Fatalf("provider-controlled output validation = %+v, %v", result, err)
	}
}

func TestGenerateEmbeddings_InvalidCounts(t *testing.T) {
	for name, output := range map[string]*embedding.Generated[[]float32]{
		"nil":      nil,
		"empty":    {},
		"too few":  {Embeddings: [][]float32{{1, 2}}},
		"too many": {Embeddings: [][]float32{{1, 2}, {3, 4}, {5, 6}}},
	} {
		t.Run(name, func(t *testing.T) {
			generator := &bindingGenerator[string, []float32]{
				generate: func(context.Context, []string, *embedding.Options) (*embedding.Generated[[]float32], error) {
					return output, nil
				},
			}
			property := buildEmbeddingProperty[batchEmbeddingRecord](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
			result, err := providerservices.GenerateEmbeddings(t.Context(), property, []batchEmbeddingRecord{{Text: "first"}, {Text: "second"}})
			if err == nil || result != nil {
				t.Fatalf("invalid count = %+v, %v", result, err)
			}
		})
	}
}

func TestGenerateEmbeddings_InvalidRecords(t *testing.T) {
	for _, test := range []struct {
		name    string
		build   func(*vectordata.Definition) (*providerservices.CollectionModel, error)
		records []any
	}{
		{"wrong record type", buildModel[string, batchEmbeddingRecord], []any{batchEmbeddingRecord{Text: "first"}, struct{}{}}},
		{"nil root pointer", buildModel[string, *batchEmbeddingRecord], []any{(*batchEmbeddingRecord)(nil)}},
		{"nil root map", buildModel[string, map[string]any], []any{map[string]any(nil)}},
		{"missing value", buildModel[string, map[string]any], []any{map[string]any{}}},
		{"untyped nil", buildModel[string, map[string]any], []any{map[string]any{"Text": nil}}},
		{"wrong dynamic value", buildModel[string, map[string]any], []any{map[string]any{"Text": 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			generator := &bindingGenerator[string, []float32]{
				generate: func(context.Context, []string, *embedding.Options) (*embedding.Generated[[]float32], error) {
					t.Fatal("invalid records reached the generator")
					return nil, nil
				},
			}
			model, err := test.build(&vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator), Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.VectorProperty{Name: "Text", Type: reflect.TypeFor[string](), Dimensions: 2},
			}})
			if err != nil {
				t.Fatal(err)
			}
			property, err := model.VectorProperty("")
			if err != nil {
				t.Fatal(err)
			}
			if result, err := providerservices.GenerateEmbeddings(t.Context(), property, test.records); err == nil || result != nil {
				t.Fatalf("invalid records = %+v, %v", result, err)
			}
		})
	}
}

func TestGenerateEmbeddings_InvalidConfiguration(t *testing.T) {
	records := []batchEmbeddingRecord{{Text: "value"}}
	for name, property := range map[string]*providerservices.VectorPropertyModel{"nil": nil, "unconfigured": {}} {
		t.Run(name, func(t *testing.T) {
			if result, err := providerservices.GenerateEmbeddings(t.Context(), property, records); err == nil || result != nil {
				t.Fatalf("invalid property = %+v, %v", result, err)
			}
		})
	}
	property := buildEmbeddingProperty[batchEmbeddingRecord](t,
		&vectordata.Definition{Properties: []vectordata.Property{
			&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
			&vectordata.VectorProperty{Name: "Text", Type: reflect.TypeFor[string](), Dimensions: 2, EmbeddingType: reflect.TypeFor[[]float32]()},
		}},
	)
	if result, err := providerservices.GenerateEmbeddings(t.Context(), property, records); err == nil || result != nil {
		t.Fatalf("missing generator = %+v, %v", result, err)
	}
}

func TestGenerateEmbeddings_CancellationAndProviderErrors(t *testing.T) {
	t.Run("pre-canceled", func(t *testing.T) {
		generator := &bindingGenerator[string, []float32]{
			generate: func(context.Context, []string, *embedding.Options) (*embedding.Generated[[]float32], error) {
				t.Fatal("canceled call reached the generator")
				return nil, nil
			},
		}
		property := buildEmbeddingProperty[batchEmbeddingRecord](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if result, err := providerservices.GenerateEmbeddings(ctx, property, []batchEmbeddingRecord{{Text: "value"}}); !errors.Is(err, context.Canceled) || result != nil {
			t.Fatalf("canceled generation = %+v, %v", result, err)
		}
	})
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider error", true: "partial result"}[partial], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := errors.New("provider failed")
			output := &embedding.Of[float64]{Vector: embedding.VectorOf[float64]{1, 2}}
			usage := &message.UsageDetails{}
			generator := &bindingGenerator[string, *embedding.Of[float64]]{
				generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float64], error) {
					cancel()
					if partial {
						return &embedding.GeneratedOf[float64]{Embeddings: []*embedding.Of[float64]{output}, Usage: usage}, want
					}
					return nil, want
				},
			}
			property := buildEmbeddingProperty[batchEmbeddingRecord](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
			result, err := providerservices.GenerateEmbeddings(ctx, property, []batchEmbeddingRecord{{Text: "first"}, {Text: "second"}})
			if !errors.Is(err, want) || errors.Is(err, context.Canceled) {
				t.Fatalf("provider error was replaced: %v", err)
			}
			if partial {
				if result == nil || len(result.Embeddings) != 1 || result.Embeddings[0] != output || result.Usage != usage {
					t.Fatalf("partial result was lost: %+v", result)
				}
			} else if result != nil {
				t.Fatalf("unexpected partial result: %+v", result)
			}
		})
	}
}
