// Copyright (c) Microsoft. All rights reserved.

package providerservices

import (
	"context"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/embedding"
)

// SupportsVector reports whether typ is []T, [embedding.VectorOf] with T
// elements, or [embedding.Of] with T elements (a value or pointer).
// A nil type is unsupported. Providers opt in by choosing T.
func SupportsVector[T any](typ reflect.Type) bool {
	switch typ {
	case reflect.TypeFor[[]T](), reflect.TypeFor[embedding.VectorOf[T]](),
		reflect.TypeFor[embedding.Of[T]](), reflect.TypeFor[*embedding.Of[T]]():
		return true
	default:
		return false
	}
}

// AsVector unwraps the representations supported by [SupportsVector] for T.
// The returned slice is borrowed, not cloned or normalized. Supported typed nils
// return (nil, true); untyped nil and unsupported values return (nil, false).
// Element types must match T exactly; no conversion is performed.
// Providers remain responsible for nil handling and dimension validation.
func AsVector[T any](value any) ([]T, bool) {
	switch vector := value.(type) {
	case []T:
		return vector, true
	case embedding.VectorOf[T]:
		return vector, true
	case embedding.Of[T]:
		return vector.Vector, true
	case *embedding.Of[T]:
		if vector == nil {
			return nil, true
		}
		return vector.Vector, true
	default:
		return nil, false
	}
}

// GenerateEmbeddings reads a vector property's inputs from records and invokes
// its configured generator once, with nil generation options. The property must
// have a record accessor configured by [Build]. Missing values become nil;
// the generator binding validates input compatibility before invoking the generator.
//
// A successful result must contain exactly one embedding per record, in input
// order. Empty batches are forwarded to the generator, preserving nil versus
// empty input slices. Context, embeddings, usage, additional properties, and
// provider errors are preserved, including partial results returned with an error.
//
// Records and their contents are read-only. This helper does not assign generated
// values to records, clone embeddings, or close the generator. Providers decide
// when client-side generation is needed and validate, convert, and store outputs.
func GenerateEmbeddings[R any](ctx context.Context, property *VectorPropertyModel, records []R) (*embedding.Generated[any], error) {
	if property == nil {
		return nil, fmt.Errorf("vectordata: vector property is required")
	}
	var inputs []any
	if records != nil {
		inputs = make([]any, len(records))
	}
	for i, record := range records {
		value, found, err := property.Value(record)
		if err != nil {
			return nil, err
		}
		if found {
			inputs[i] = value
		}
	}
	result, err := property.EmbeddingGenerator.Generate(ctx, inputs, nil)
	if err != nil {
		return result, err
	}
	if result == nil || len(result.Embeddings) != len(records) {
		return nil, fmt.Errorf("property %q: generator must return exactly %d embeddings", property.Name, len(records))
	}
	return result, nil
}
