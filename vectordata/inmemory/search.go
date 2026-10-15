// Copyright (c) Microsoft. All rights reserved.

package inmemory

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/internal/recordvalue"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

// Search performs an exact vector search after filtering, then applies the
// inclusive score threshold, ordering, Skip, and top. Execution is lazy and uses
// a snapshot of stored record references. Similarities sort descending; distances
// ascending. Scores have float32 precision. Null record vectors are skipped.
// Results retain native vectors regardless of IncludeVectors.
func (c *Collection[K, R]) Search(ctx context.Context, searchValue any, top int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[R], error] {
	return func(yield func(vectordata.SearchResult[R], error) bool) {
		results, err := c.search(ctx, searchValue, top, options)
		if err != nil {
			yield(vectordata.SearchResult[R]{}, failure(c.name, "Search", err))
			return
		}
		for _, result := range results {
			if err := ctx.Err(); err != nil {
				yield(vectordata.SearchResult[R]{}, failure(c.name, "Search", err))
				return
			}
			if !yield(vectordata.SearchResult[R]{Record: result.record.Record, Score: new(float64(result.score))}, nil) {
				return
			}
		}
	}
}

type scoredRecord[R any] struct {
	record *storedRecord[R]
	score  float32
}

func (c *Collection[K, R]) search(ctx context.Context, searchValue any, top int, options *vectordata.SearchOptions) ([]scoredRecord[R], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if recordvalue.IsNil(reflect.ValueOf(searchValue)) || top <= 0 {
		return nil, fmt.Errorf("search requires a non-nil value and positive top")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if options == nil {
		options = &vectordata.SearchOptions{}
	}
	if err := c.validateRetrieval(options.IncludeVectors); err != nil {
		return nil, err
	}
	property, err := c.model.VectorProperty(options.VectorProperty)
	if err != nil {
		return nil, err
	}
	descending, err := sortDescending(property.DistanceFunction)
	if err != nil {
		return nil, err
	}
	matches, err := c.schema.compileFilter(options.Filter)
	if err != nil {
		return nil, err
	}
	input, native := nativeVector(searchValue)
	if !native {
		if property.SearchEmbeddingGenerator == nil {
			return nil, fmt.Errorf("property %q has no compatible generator for search input %T", property.Name, searchValue)
		}
		output, err := embedding.Generate(ctx, property.SearchEmbeddingGenerator, searchValue, nil)
		if err != nil {
			return nil, err
		}
		input, native = nativeVector(output)
	}
	if !native || input == nil {
		return nil, fmt.Errorf("search requires a non-nil float32 vector")
	}
	if err := validateVector(input, property.Dimensions); err != nil {
		return nil, err
	}
	records, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	field := c.schema.fields[property.Name]
	results := make([]scoredRecord[R], 0, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match, err := matches(record.Record)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		vector := record.Vectors[property.Name]
		if property.Type == property.EmbeddingType {
			value, found, err := field.read(reflect.ValueOf(record.Record))
			if err != nil {
				return nil, err
			}
			if !found || !value.IsValid() {
				continue
			}
			vector, _ = nativeVector(value.Interface())
			if vector == nil {
				continue
			}
		}
		if err := validateVector(vector, property.Dimensions); err != nil {
			return nil, fmt.Errorf("property %q: %w", property.Name, err)
		}
		score := compareVectors(input, vector, property.DistanceFunction)
		if options.ScoreThreshold != nil {
			threshold := *options.ScoreThreshold
			if descending && !(float64(score) >= threshold) || !descending && !(float64(score) <= threshold) {
				continue
			}
		}
		results = append(results, scoredRecord[R]{record: record, score: score})
	}
	slices.SortStableFunc(results, func(a, b scoredRecord[R]) int {
		if descending {
			return cmp.Compare(b.score, a.score)
		}
		return cmp.Compare(a.score, b.score)
	})
	start := min(options.Skip, len(results))
	return results[start : start+min(top, len(results)-start)], nil
}

// GetFiltered returns each stored matching record after multi-column
// ordering, Skip, and top. Supported ordered values are booleans, strings,
// numbers, UUIDs, types with Compare(T) int methods, and nullable pointers to
// these types. Nulls sort before non-null values in ascending order.
// Without OrderBy, order is unspecified.
// Strings use Go's lexicographic ordering, without culture-specific collation.
// Property names refer to Go fields or dynamic schema properties, not storage names.
// Results retain native vectors regardless of IncludeVectors.
func (c *Collection[K, R]) GetFiltered(ctx context.Context, filter vectordata.Filter, top int, options *vectordata.FilteredRecordRetrievalOptions) iter.Seq2[R, error] {
	return func(yield func(R, error) bool) {
		records, err := c.filtered(ctx, filter, top, options)
		var zero R
		if err != nil {
			yield(zero, failure(c.name, "GetFiltered", err))
			return
		}
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				yield(zero, failure(c.name, "GetFiltered", err))
				return
			}
			if !yield(record, nil) {
				return
			}
		}
	}
}

func (c *Collection[K, R]) filtered(ctx context.Context, filter vectordata.Filter, top int, options *vectordata.FilteredRecordRetrievalOptions) ([]R, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if top <= 0 {
		return nil, fmt.Errorf("top must be positive")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if options == nil {
		options = &vectordata.FilteredRecordRetrievalOptions{}
	}
	if err := c.validateRetrieval(options.IncludeVectors); err != nil {
		return nil, err
	}
	matches, err := c.schema.compileFilter(&filter)
	if err != nil {
		return nil, err
	}
	records := func(yield func(R, error) bool) {
		var zero R
		snapshot, err := c.snapshot(ctx)
		if err != nil {
			yield(zero, err)
			return
		}
		for _, record := range snapshot {
			if err := ctx.Err(); err != nil {
				yield(zero, err)
				return
			}
			match, err := matches(record.Record)
			if err != nil {
				yield(zero, err)
				return
			}
			if match && !yield(record.Record, nil) {
				return
			}
		}
	}
	result, err := providerservices.SortRecords(records, options.OrderBy, func(name string) (providerservices.FilterProperty, error) {
		field, found := c.schema.fields[name]
		if !found {
			return providerservices.FilterProperty{}, fmt.Errorf("sort property %q was not found or does not support ordering", name)
		}
		return field.localProperty(), nil
	})
	if err != nil {
		return nil, err
	}
	start := min(options.Skip, len(result))
	return result[start : start+min(top, len(result)-start)], nil
}
