// Copyright (c) Microsoft. All rights reserved.

package embedding_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/embedding"
)

type testGenerator[I, E any] struct {
	generate func(context.Context, []I, *embedding.Options) (*embedding.Generated[E], error)
}

func (g *testGenerator[I, E]) Generate(ctx context.Context, values []I, options *embedding.Options) (*embedding.Generated[E], error) {
	return g.generate(ctx, values, options)
}

func TestEmbeddingGeneratorExtensions_InvalidArgs(t *testing.T) {
	ctx := t.Context()
	var generator embedding.Generator[string, *embedding.Of[float32]]
	if _, err := embedding.Generate(ctx, generator, "hello", nil); err == nil {
		t.Fatal("expected nil generator error")
	}
	if _, err := embedding.GenerateVector[float32](ctx, generator, "hello", nil); err == nil {
		t.Fatal("expected nil generator error")
	}
	if _, err := embedding.GenerateAndZip(ctx, generator, []string{}, nil); err == nil {
		t.Fatal("expected nil generator error even for empty input")
	}
	g := &testGenerator[any, *embedding.Of[float32]]{}
	if _, err := embedding.Generate(ctx, g, nil, nil); err == nil {
		t.Fatal("expected nil input error")
	}
}

func TestEmbeddingGeneratorExtensions_Generate_TypedNilInput(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
	}{
		{"pointer", (*string)(nil)},
		{"slice", []byte(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 2, 3}}
			g := &testGenerator[any, *embedding.Of[float32]]{
				generate: func(_ context.Context, values []any, _ *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					if len(values) != 1 || values[0] == nil {
						t.Fatalf("typed nil input was not forwarded: %v", values)
					}
					return &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{want}}, nil
				},
			}
			result, err := embedding.Generate(t.Context(), g, test.value, nil)
			if err != nil || result != want {
				t.Fatalf("Generate = %+v, %v", result, err)
			}
		})
	}
}

func TestEmbeddingGeneratorExtensions_Generate_ReturnsSingleEmbedding(t *testing.T) {
	ctx := t.Context()
	options := &embedding.Options{ModelID: new("test")}
	wantVector := embedding.VectorOf[float32]{1, 2, 3}
	want := &embedding.Of[float32]{Vector: wantVector, ModelID: "test"}
	g := &testGenerator[string, *embedding.Of[float32]]{
		generate: func(gotCtx context.Context, values []string, gotOptions *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			if gotCtx != ctx || gotOptions != options || !slices.Equal(values, []string{"hello"}) {
				t.Fatal("generation arguments were not preserved")
			}
			return &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{want}}, nil
		},
	}
	result, err := embedding.Generate(ctx, g, "hello", options)
	if err != nil || result != want {
		t.Fatalf("Generate = %+v, %v", result, err)
	}
	vector, err := embedding.GenerateVector(ctx, g, "hello", options)
	if err != nil || !slices.Equal(vector, wantVector) || &vector[0] != &wantVector[0] {
		t.Fatalf("GenerateVector = %v, %v", vector, err)
	}
}

func TestEmbeddingGeneratorExtensions_ZeroDimensions(t *testing.T) {
	ctx := t.Context()
	options := &embedding.Options{Dimensions: new(0)}
	want := &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{}}
	calls := 0
	g := &testGenerator[string, *embedding.Of[float32]]{
		generate: func(gotCtx context.Context, values []string, gotOptions *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			calls++
			if gotCtx != ctx || !slices.Equal(values, []string{"hello"}) ||
				gotOptions != options || gotOptions.Dimensions == nil || *gotOptions.Dimensions != 0 {
				t.Fatal("zero dimensions were not forwarded unchanged")
			}
			return &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{want}}, nil
		},
	}
	if result, err := embedding.Generate(ctx, g, "hello", options); err != nil || result != want {
		t.Fatalf("Generate = %+v, %v", result, err)
	}
	if vector, err := embedding.GenerateVector[float32](ctx, g, "hello", options); err != nil || vector == nil || len(vector) != 0 {
		t.Fatalf("GenerateVector = %v, %v", vector, err)
	}
	pairs, err := embedding.GenerateAndZip(ctx, g, []string{"hello"}, options)
	if err != nil || len(pairs) != 1 || pairs[0].Value != "hello" || pairs[0].Embedding != want {
		t.Fatalf("GenerateAndZip = %+v, %v", pairs, err)
	}
	if calls != 3 {
		t.Fatalf("generator calls = %d, want 3", calls)
	}
}

func TestEmbeddingGeneratorExtensions_ProviderDimensionError(t *testing.T) {
	options := &embedding.Options{Dimensions: new(-1)}
	wantErr := errors.New("provider rejected dimensions")
	calls := 0
	g := &testGenerator[string, *embedding.Of[float32]]{
		generate: func(_ context.Context, _ []string, gotOptions *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			calls++
			if gotOptions != options || gotOptions.Dimensions == nil || *gotOptions.Dimensions != -1 {
				t.Fatal("dimensions were not forwarded to the provider unchanged")
			}
			return nil, wantErr
		},
	}
	if result, err := embedding.Generate(t.Context(), g, "hello", options); err != wantErr || result != nil {
		t.Fatalf("Generate = %+v, %v", result, err)
	}
	if vector, err := embedding.GenerateVector[float32](t.Context(), g, "hello", options); err != wantErr || vector != nil {
		t.Fatalf("GenerateVector = %v, %v", vector, err)
	}
	if pairs, err := embedding.GenerateAndZip(t.Context(), g, []string{"hello"}, options); err != wantErr || pairs != nil {
		t.Fatalf("GenerateAndZip = %+v, %v", pairs, err)
	}
	if calls != 3 {
		t.Fatalf("generator calls = %d, want 3", calls)
	}
}

func TestEmbeddingGeneratorExtensions_Generate_InvalidResults(t *testing.T) {
	e := &embedding.Of[float32]{}
	for _, result := range []*embedding.GeneratedOf[float32]{
		nil,
		{},
		{Embeddings: []*embedding.Of[float32]{e, e}},
	} {
		g := &testGenerator[string, *embedding.Of[float32]]{
			generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
				return result, nil
			},
		}
		if value, err := embedding.Generate(t.Context(), g, "hello", nil); err == nil || value != nil {
			t.Fatalf("Generate = %+v, %v; expected error", value, err)
		}
		if value, err := embedding.GenerateVector[float32](t.Context(), g, "hello", nil); err == nil || value != nil {
			t.Fatalf("GenerateVector = %+v, %v; expected error", value, err)
		}
	}
}

func TestEmbeddingGeneratorExtensions_Generate_NilEmbedding(t *testing.T) {
	g := &testGenerator[string, any]{
		generate: func(context.Context, []string, *embedding.Options) (*embedding.Generated[any], error) {
			return &embedding.Generated[any]{Embeddings: []any{nil}}, nil
		},
	}
	if got, err := embedding.Generate(t.Context(), g, "hello", nil); err == nil || got != nil {
		t.Fatalf("Generate = %v, %v; expected nil embedding error", got, err)
	}
}

func TestEmbeddingGeneratorExtensions_Generate_TypedNilEmbedding(t *testing.T) {
	g := &testGenerator[string, *embedding.Of[float32]]{
		generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			return &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{nil}}, nil
		},
	}
	if got, err := embedding.Generate(t.Context(), g, "hello", nil); err != nil || got != nil {
		t.Fatalf("Generate = %v, %v; expected typed nil embedding", got, err)
	}
	if got, err := embedding.GenerateVector[float32](t.Context(), g, "hello", nil); err == nil || got != nil {
		t.Fatalf("GenerateVector = %v, %v; expected nil embedding error", got, err)
	}
}

func TestEmbeddingGenerator_CustomOutputType(t *testing.T) {
	type typedEmbedding struct {
		ModelID string
		Vector  embedding.VectorOf[float32]
	}
	outputs := []typedEmbedding{
		{ModelID: "test", Vector: embedding.VectorOf[float32]{1, 2, 3}},
		{ModelID: "test", Vector: embedding.VectorOf[float32]{4, 5, 6}},
	}
	g := &testGenerator[string, typedEmbedding]{
		generate: func(_ context.Context, values []string, _ *embedding.Options) (*embedding.Generated[typedEmbedding], error) {
			return &embedding.Generated[typedEmbedding]{Embeddings: outputs[:len(values)]}, nil
		},
	}
	var generator embedding.Generator[string, typedEmbedding] = g
	result, err := embedding.Generate(t.Context(), generator, "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelID != "test" || !slices.Equal(result.Vector, outputs[0].Vector) || &result.Vector[0] != &outputs[0].Vector[0] {
		t.Fatalf("custom Generate = %+v", result)
	}
	inputs := []string{"first", "second"}
	pairs, err := embedding.GenerateAndZip(t.Context(), generator, inputs, nil)
	if err != nil || len(pairs) != 2 {
		t.Fatalf("custom GenerateAndZip = %+v, %v", pairs, err)
	}
	for i, pair := range pairs {
		if pair.Value != inputs[i] || pair.Embedding.ModelID != outputs[i].ModelID ||
			!slices.Equal(pair.Embedding.Vector, outputs[i].Vector) {
			t.Fatalf("custom pair[%d] = %+v", i, pair)
		}
	}
}

func TestEmbeddingGeneratorExtensions_Generate_PolymorphicVectors(t *testing.T) {
	for _, vector := range []embedding.Vector{nil, embedding.VectorOf[float64]{1, 2, 3}, embedding.Binary{true}, customVector{1}} {
		want := &embedding.Embedding[embedding.Vector]{Vector: vector}
		g := &testGenerator[string, *embedding.Embedding[embedding.Vector]]{
			generate: func(context.Context, []string, *embedding.Options) (*embedding.Generated[*embedding.Embedding[embedding.Vector]], error) {
				return &embedding.Generated[*embedding.Embedding[embedding.Vector]]{Embeddings: []*embedding.Embedding[embedding.Vector]{want}}, nil
			},
		}
		if got, err := embedding.Generate(t.Context(), g, "hello", nil); err != nil || got != want {
			t.Fatalf("Generate = %+v, %v", got, err)
		}
	}
}

func TestEmbeddingGeneratorExtensions_GenerateVector_PreservesEmptyVectors(t *testing.T) {
	for _, vector := range []embedding.VectorOf[float32]{nil, {}, {1, 2, 3}} {
		g := &testGenerator[string, *embedding.Of[float32]]{
			generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
				return &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{{Vector: vector}}}, nil
			},
		}
		got, err := embedding.GenerateVector[float32](t.Context(), g, "hello", nil)
		if err != nil || !slices.Equal(got, vector) || (got == nil) != (vector == nil) {
			t.Fatalf("GenerateVector = %v, %v; want %v", got, err, vector)
		}
	}
}

func TestEmbeddingGeneratorExtensions_GenerateVector_ExplicitElementType(t *testing.T) {
	want := embedding.VectorOf[float64]{1, 2, 3}
	g := &testGenerator[string, *embedding.Of[float64]]{
		generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float64], error) {
			return &embedding.GeneratedOf[float64]{Embeddings: []*embedding.Of[float64]{{Vector: want}}}, nil
		},
	}
	got, err := embedding.GenerateVector[float64](t.Context(), g, "hello", nil)
	if err != nil || !slices.Equal(got, want) || &got[0] != &want[0] {
		t.Fatalf("GenerateVector = %v, %v", got, err)
	}
}

func TestEmbeddingGeneratorExtensions_GenerateAndZip_ReturnsExpectedList(t *testing.T) {
	for _, count := range []int{0, 1, 10} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			values := make([]string, count)
			embeddings := make([]*embedding.Of[float32], count)
			for i := range values {
				values[i] = fmt.Sprintf("hello %d", i)
				embeddings[i] = &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{float32(i), float32(i + 1), float32(i + 2), float32(i + 3)}}
			}
			calls := 0
			options := &embedding.Options{Dimensions: new(4)}
			ctx := t.Context()
			g := &testGenerator[string, *embedding.Of[float32]]{
				generate: func(gotCtx context.Context, got []string, gotOptions *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					calls++
					if gotCtx != ctx || gotOptions != options || &got[0] != &values[0] {
						t.Fatal("generation arguments were not preserved")
					}
					return &embedding.GeneratedOf[float32]{Embeddings: embeddings}, nil
				},
			}
			pairs, err := embedding.GenerateAndZip(ctx, g, values, options)
			if err != nil || pairs == nil || len(pairs) != count {
				t.Fatalf("pairs = %+v, %v", pairs, err)
			}
			for i, pair := range pairs {
				if pair.Value != values[i] || pair.Embedding != embeddings[i] {
					t.Fatalf("pair[%d] = %+v", i, pair)
				}
			}
			if (count == 0 && calls != 0) || (count > 0 && calls != 1) {
				t.Fatalf("calls = %d", calls)
			}
		})
	}
}

func TestEmbeddingGeneratorExtensions_GenerateAndZip_EmptySkipsGenerator(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, g := range []*testGenerator[string, *embedding.Of[float32]]{nil, {}} {
		for _, values := range [][]string{nil, {}} {
			pairs, err := embedding.GenerateAndZip(ctx, g, values, nil)
			if err != nil || pairs == nil || len(pairs) != 0 {
				t.Fatalf("empty batch = %+v, %v", pairs, err)
			}
		}
	}
}

func TestEmbeddingGeneratorExtensions_GenerateAndZip_InvalidResults(t *testing.T) {
	for _, result := range []*embedding.GeneratedOf[float32]{
		nil, {}, {Embeddings: []*embedding.Of[float32]{{}, {}}},
	} {
		g := &testGenerator[string, *embedding.Of[float32]]{
			generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
				return result, nil
			},
		}
		if pairs, err := embedding.GenerateAndZip(t.Context(), g, []string{"hello"}, nil); err == nil || pairs != nil {
			t.Fatalf("pairs = %+v, %v; expected error", pairs, err)
		}
	}
}

func TestEmbeddingGeneratorExtensions_GenerateAndZip_PreservesNilElements(t *testing.T) {
	g := &testGenerator[string, *embedding.Of[float32]]{
		generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			return &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{nil}}, nil
		},
	}
	pairs, err := embedding.GenerateAndZip(t.Context(), g, []string{"hello"}, nil)
	if err != nil || len(pairs) != 1 || pairs[0].Value != "hello" || pairs[0].Embedding != nil {
		t.Fatalf("pairs = %+v, %v", pairs, err)
	}
}

func TestEmbeddingGeneratorExtensions_PropagatesErrorsAndCancellation(t *testing.T) {
	for _, want := range []error{errors.New("provider failed"), context.Canceled, context.DeadlineExceeded} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		g := &testGenerator[string, *embedding.Of[float32]]{
			generate: func(gotCtx context.Context, _ []string, _ *embedding.Options) (*embedding.GeneratedOf[float32], error) {
				if gotCtx != ctx || gotCtx.Err() != context.Canceled {
					t.Fatal("context was not forwarded")
				}
				return &embedding.GeneratedOf[float32]{}, want
			},
		}
		if _, err := embedding.Generate(ctx, g, "hello", nil); err != want {
			t.Fatalf("Generate error = %v, want %v", err, want)
		}
		if _, err := embedding.GenerateVector[float32](ctx, g, "hello", nil); err != want {
			t.Fatalf("GenerateVector error = %v, want %v", err, want)
		}
		if _, err := embedding.GenerateAndZip(ctx, g, []string{"hello"}, nil); err != want {
			t.Fatalf("GenerateAndZip error = %v, want %v", err, want)
		}
	}
}

func TestEmbeddingGenerator_ConcurrentCalls(t *testing.T) {
	g := &testGenerator[string, *embedding.Of[float32]]{
		generate: func(ctx context.Context, values []string, options *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return &embedding.GeneratedOf[float32]{
				Embeddings: []*embedding.Of[float32]{{Vector: embedding.VectorOf[float32]{float32(len(values[0]))}}},
			}, nil
		},
	}
	options := &embedding.Options{Dimensions: new(1)}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			v, err := embedding.GenerateVector[float32](t.Context(), g, "hello", options)
			if err != nil || !slices.Equal(v, []float32{5}) {
				t.Errorf("concurrent GenerateVector = %v, %v", v, err)
			}
		})
	}
	wg.Wait()
}
