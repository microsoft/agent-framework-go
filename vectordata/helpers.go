// Copyright (c) Microsoft. All rights reserved.

package vectordata

import (
	"context"
	"fmt"
	"iter"
)

// GetBatch retrieves the found records for keys, omitting missing records.
// If collection implements a GetBatch method with this signature, it is used.
// Otherwise records are retrieved sequentially in key order. Native batch
// providers may return records in a different order. Work begins on iteration.
// A nil or empty keys slice is an empty batch and does not call the provider.
func GetBatch[TKey, TRecord any](ctx context.Context, collection Collection[TKey, TRecord], keys []TKey, options *RecordRetrievalOptions) iter.Seq2[TRecord, error] {
	return func(yield func(TRecord, error) bool) {
		var zero TRecord
		if collection == nil {
			yield(zero, fmt.Errorf("vectordata: collection is required"))
			return
		}
		if err := ctx.Err(); err != nil {
			yield(zero, err)
			return
		}
		if len(keys) == 0 {
			return
		}
		if batch, ok := collection.(interface {
			GetBatch(context.Context, []TKey, *RecordRetrievalOptions) iter.Seq2[TRecord, error]
		}); ok {
			for record, err := range batch.GetBatch(ctx, keys, options) {
				if err != nil {
					yield(zero, err)
					return
				}
				if !yield(record, nil) {
					return
				}
			}
			return
		}
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				yield(zero, err)
				return
			}
			record, found, err := collection.Get(ctx, key, options)
			if err != nil {
				yield(zero, err)
				return
			}
			if found && !yield(record, nil) {
				return
			}
		}
	}
}

// DeleteBatch deletes keys, ignoring missing records. If collection implements
// DeleteBatch(context.Context, []TKey) error, it is used; otherwise keys are
// deleted sequentially, stopping at the first error. A failed batch may have
// deleted some keys and can be retried. Nil and empty slices do not call the
// provider.
func DeleteBatch[TKey, TRecord any](ctx context.Context, collection Collection[TKey, TRecord], keys []TKey) error {
	if collection == nil {
		return fmt.Errorf("vectordata: collection is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	if batch, ok := collection.(interface {
		DeleteBatch(context.Context, []TKey) error
	}); ok {
		return batch.DeleteBatch(ctx, keys)
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := collection.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}
