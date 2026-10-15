// Copyright (c) Microsoft. All rights reserved.

package providerservices

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/embedding"
)

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
