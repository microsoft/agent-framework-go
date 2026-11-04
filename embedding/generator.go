// Copyright (c) Microsoft. All rights reserved.

package embedding

import (
	"context"
	"fmt"
)

// Generator generates one embedding for each input, preserving input order.
// Implementations must support concurrent requests, honor context cancellation,
// validate provider-specific constraints, and treat input slices, input values,
// and options as read-only.
// Nil and empty input slices both represent an empty batch.
//
// TEmbedding specifies the output representation. Use *Of[T]
// for a statically typed numeric vector, *Embedding[Vector] for a polymorphic
// vector, or a custom output type.
type Generator[TInput, TEmbedding any] interface {
	Generate(ctx context.Context, values []TInput, options *Options) (*Generated[TEmbedding], error)
}

// Generate generates exactly one embedding. A nil generator or input is invalid.
// It returns an error if the generator returns a nil result, the wrong number of
// embeddings, or a nil embedding. Interface nil checks do not reject typed nil
// values.
func Generate[TInput, TEmbedding any](ctx context.Context, generator Generator[TInput, TEmbedding], value TInput, options *Options) (TEmbedding, error) {
	var zero TEmbedding
	if generator == nil {
		return zero, fmt.Errorf("embedding: generator is required")
	}
	if any(value) == nil {
		return zero, fmt.Errorf("embedding: input value is required")
	}
	result, err := generator.Generate(ctx, []TInput{value}, options)
	if err != nil {
		return zero, err
	}
	if result == nil {
		return zero, fmt.Errorf("embedding: generator returned a nil collection of embeddings")
	}
	if len(result.Embeddings) != 1 {
		return zero, fmt.Errorf("embedding: expected 1 embedding, got %d", len(result.Embeddings))
	}
	if any(result.Embeddings[0]) == nil {
		return zero, fmt.Errorf("embedding: generator returned a nil embedding")
	}
	return result.Embeddings[0], nil
}

// GenerateVector generates a single embedding and returns its vector without
// copying it. The generator's output determines the vector element type.
func GenerateVector[TElement, TInput any](ctx context.Context, generator Generator[TInput, *Of[TElement]], value TInput, options *Options) ([]TElement, error) {
	result, err := Generate(ctx, generator, value, options)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("embedding: generator returned a nil embedding")
	}
	return result.Vector, nil
}

// Pair associates an input value with its generated embedding.
type Pair[TInput, TEmbedding any] struct {
	Value     TInput
	Embedding TEmbedding
}

// GenerateAndZip generates embeddings and pairs them with inputs in order.
// An empty input returns an empty, non-nil slice without invoking the generator.
// A result count different from the input count returns an error.
func GenerateAndZip[TInput, TEmbedding any](ctx context.Context, generator Generator[TInput, TEmbedding], values []TInput, options *Options) ([]Pair[TInput, TEmbedding], error) {
	if generator == nil {
		return nil, fmt.Errorf("embedding: generator is required")
	}
	if len(values) == 0 {
		return []Pair[TInput, TEmbedding]{}, nil
	}
	result, err := generator.Generate(ctx, values, options)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("embedding: generator returned a nil collection of embeddings")
	}
	if len(result.Embeddings) != len(values) {
		return nil, fmt.Errorf("embedding: expected %d embeddings, got %d", len(values), len(result.Embeddings))
	}
	pairs := make([]Pair[TInput, TEmbedding], len(values))
	for i, value := range values {
		pairs[i] = Pair[TInput, TEmbedding]{Value: value, Embedding: result.Embeddings[i]}
	}
	return pairs, nil
}
