// Copyright (c) Microsoft. All rights reserved.

package vectordata

import (
	"context"
	"fmt"
	"iter"
	"reflect"
)

type typedCollection[K, R any] struct {
	raw Collection[any, any]
}

func (c *typedCollection[K, R]) Name() string { return c.raw.Name() }

func (c *typedCollection[K, R]) Exists(ctx context.Context) (bool, error) {
	return c.raw.Exists(ctx)
}

func (c *typedCollection[K, R]) EnsureExists(ctx context.Context) error {
	return c.raw.EnsureExists(ctx)
}

func (c *typedCollection[K, R]) EnsureDeleted(ctx context.Context) error {
	return c.raw.EnsureDeleted(ctx)
}

func (c *typedCollection[K, R]) Get(ctx context.Context, key K, options *RecordRetrievalOptions) (R, bool, error) {
	value, found, err := c.raw.Get(ctx, key, options)
	var zero R
	if err != nil || !found {
		return zero, found, err
	}
	record, err := typedRecord[R](value)
	return record, found, err
}

func (c *typedCollection[K, R]) GetFiltered(ctx context.Context, filter Filter, top int, options *FilteredRecordRetrievalOptions) iter.Seq2[R, error] {
	return func(yield func(R, error) bool) {
		for value, err := range c.raw.GetFiltered(ctx, filter, top, options) {
			var record R
			if err == nil {
				record, err = typedRecord[R](value)
			}
			if !yield(record, err) || err != nil {
				return
			}
		}
	}
}

func (c *typedCollection[K, R]) Delete(ctx context.Context, key K) error {
	return c.raw.Delete(ctx, key)
}

func (c *typedCollection[K, R]) Upsert(ctx context.Context, records ...R) error {
	return c.raw.Upsert(ctx, untypedValues(records)...)
}

func (c *typedCollection[K, R]) Search(ctx context.Context, value any, top int, options *SearchOptions) iter.Seq2[SearchResult[R], error] {
	return func(yield func(SearchResult[R], error) bool) {
		yieldSearchResults(c.raw.Search(ctx, value, top, options), yield)
	}
}

func (c *typedCollection[K, R]) GetBatch(ctx context.Context, keys []K, options *RecordRetrievalOptions) iter.Seq2[R, error] {
	return func(yield func(R, error) bool) {
		for value, err := range GetBatch(ctx, c.raw, untypedValues(keys), options) {
			var record R
			if err == nil {
				record, err = typedRecord[R](value)
			}
			if !yield(record, err) || err != nil {
				return
			}
		}
	}
}

func (c *typedCollection[K, R]) DeleteBatch(ctx context.Context, keys []K) error {
	return DeleteBatch(ctx, c.raw, untypedValues(keys))
}

type typedHybridCollection[K, R any] struct {
	*typedCollection[K, R]
	hybrid KeywordHybridSearcher[any]
}

func (c *typedHybridCollection[K, R]) HybridSearch(ctx context.Context, value any, keywords []string, top int, options *HybridSearchOptions) iter.Seq2[SearchResult[R], error] {
	return func(yield func(SearchResult[R], error) bool) {
		yieldSearchResults(c.hybrid.HybridSearch(ctx, value, keywords, top, options), yield)
	}
}

func yieldSearchResults[R any](results iter.Seq2[SearchResult[any], error], yield func(SearchResult[R], error) bool) {
	for result, err := range results {
		var typed SearchResult[R]
		if err == nil {
			typed.Record, err = typedRecord[R](result.Record)
			if err == nil {
				typed.Score = result.Score
			}
		}
		if !yield(typed, err) || err != nil {
			return
		}
	}
}

func typedRecord[R any](value any) (R, error) {
	if record, ok := value.(R); ok {
		return record, nil
	}
	var zero R
	if value == nil {
		switch reflect.TypeFor[R]().Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
			return zero, nil
		}
	}
	return zero, fmt.Errorf("vectordata: provider returned record type %T, want %v", value, reflect.TypeFor[R]())
}

func untypedValues[T any](values []T) []any {
	if values == nil {
		return nil
	}
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
