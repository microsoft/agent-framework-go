// Copyright (c) Microsoft. All rights reserved.

package providerservices

import (
	"fmt"
	"iter"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/internal/recordvalue"
)

// SortRecords collects records in stable, multi-property order for local
// evaluation. It resolves and validates all sort properties before enumerating
// records, then reads and normalizes each sort key once per record. A missing
// property value is treated as nil. With no ordering, enumeration order is
// preserved and resolve may be nil.
//
// Ordering uses Compare(T) int methods, primitive ordering, or UUID byte ordering.
// Pointers and interfaces are unwrapped. Nil values precede non-nil values in
// ascending order; strings use lexicographic ordering and NaN precedes other
// floating-point values.
//
// The iterator controls filtering and cancellation and must not be nil. Iterator,
// reader, and comparison errors return no records. Pagination belongs to the
// provider and must follow sorting. Remote providers can use backend ordering
// instead of this helper.
//
// Inputs are read-only. The result owns its slice but shares record contents.
// Readers and comparison methods must not mutate records.
func SortRecords[R any](records iter.Seq2[R, error], orderBy []vectordata.Sort, resolve func(string) (FilterProperty, error)) ([]R, error) {
	if records == nil {
		return nil, fmt.Errorf("vectordata: record iterator is required")
	}
	options := vectordata.FilteredRecordRetrievalOptions{OrderBy: orderBy}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if len(orderBy) > 0 && resolve == nil {
		return nil, fmt.Errorf("vectordata: sort property resolver is required")
	}
	properties := make([]FilterProperty, len(orderBy))
	for i, order := range orderBy {
		property, err := resolve(order.Property)
		if err != nil {
			return nil, err
		}
		if property.Type == nil || property.Value == nil {
			return nil, fmt.Errorf("vectordata: sort property %q requires a type and value reader", order.Property)
		}
		if !recordvalue.OrderableType(property.Type) {
			return nil, fmt.Errorf("sort property %q was not found or does not support ordering", order.Property)
		}
		properties[i] = property
	}
	type row struct {
		record R
		values []reflect.Value
	}
	var rows []row
	for record, err := range records {
		if err != nil {
			return nil, err
		}
		values := make([]reflect.Value, len(properties))
		for i, property := range properties {
			value, found, err := property.Value(record)
			if err != nil {
				return nil, err
			}
			if found {
				values[i] = recordvalue.Indirect(reflect.ValueOf(value))
			}
			if _, err := recordvalue.Compare(values[i], values[i]); err != nil {
				return nil, fmt.Errorf("sort property %q: %w", orderBy[i].Property, err)
			}
		}
		rows = append(rows, row{record: record, values: values})
	}
	var sortError error
	slices.SortStableFunc(rows, func(a, b row) int {
		if sortError != nil {
			return 0
		}
		for i, order := range orderBy {
			comparison, err := recordvalue.Compare(a.values[i], b.values[i])
			if err != nil {
				sortError = err
				return 0
			}
			if comparison != 0 {
				if order.Descending {
					return -comparison
				}
				return comparison
			}
		}
		return 0
	})
	if sortError != nil {
		return nil, sortError
	}
	result := make([]R, len(rows))
	for i, row := range rows {
		result[i] = row.record
	}
	return result, nil
}
