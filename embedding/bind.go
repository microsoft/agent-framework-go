// Copyright (c) Microsoft. All rights reserved.

package embedding

import (
	"context"
	"fmt"
	"reflect"
)

// BoundGenerator adapts a typed [Generator] to Generator[any, any] while retaining
// its declared input and output types. Create it with [Bind]; its zero value is
// invalid for generation. A binding is immutable and can be shared across callers.
// It borrows the generator and does not close it or expose its optional interfaces.
type BoundGenerator struct {
	inputType     reflect.Type
	embeddingType reflect.Type
	generate      func(context.Context, []any, *Options) (*Generated[any], error)
}

// Bind binds generator's input and output types without
// changing the generator. It panics if generator is nil. Interface nil checks
// do not reject typed nil values. The generator must support concurrent calls.
func Bind[I, E any](generator Generator[I, E]) *BoundGenerator {
	if generator == nil {
		panic("embedding: generator is required")
	}
	return &BoundGenerator{
		inputType:     reflect.TypeFor[I](),
		embeddingType: reflect.TypeFor[E](),
		generate: func(ctx context.Context, values []any, options *Options) (*Generated[any], error) {
			var inputs []I
			if values != nil {
				inputs = make([]I, len(values))
			}
			for i, value := range values {
				input, ok := value.(I)
				if !ok {
					return nil, fmt.Errorf("embedding: input at index %d has type %T, expected %v", i, value, reflect.TypeFor[I]())
				}
				inputs[i] = input
			}
			result, err := generator.Generate(ctx, inputs, options)
			if result == nil {
				return nil, err
			}
			var outputs []any
			if result.Embeddings != nil {
				outputs = make([]any, len(result.Embeddings))
				for i, value := range result.Embeddings {
					outputs[i] = value
				}
			}
			return &Generated[any]{
				Embeddings:           outputs,
				Usage:                result.Usage,
				AdditionalProperties: result.AdditionalProperties,
			}, err
		},
	}
}

// InputType returns the generator's declared input type, or nil for an
// uninitialized binding.
func (g *BoundGenerator) InputType() reflect.Type { return g.inputType }

// EmbeddingType returns the generator's declared embedding output type, or nil
// for an uninitialized binding.
func (g *BoundGenerator) EmbeddingType() reflect.Type { return g.embeddingType }

// Generate checks all inputs before making one call to the typed generator.
// Each input must satisfy InputType without conversion; an untyped nil is invalid.
// Typed nils are forwarded normally. Nil and empty slices remain distinct, and
// empty batches are forwarded to the generator rather than short-circuited.
//
// Context and options are forwarded unchanged. The input and output slices are
// shallow copies; records, embeddings, usage, and additional properties are not
// cloned. Provider results and errors, including partial results, are preserved.
// A pre-canceled context, nil binding, or zero-value binding returns an error
// without invoking the generator.
func (g *BoundGenerator) Generate(ctx context.Context, values []any, options *Options) (*Generated[any], error) {
	if g == nil || g.generate == nil {
		return nil, fmt.Errorf("embedding: generator must be created with Bind")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return g.generate(ctx, values, options)
}
