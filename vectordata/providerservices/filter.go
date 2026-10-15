// Copyright (c) Microsoft. All rights reserved.

package providerservices

import (
	"fmt"
	"math"
	"reflect"

	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/internal/recordvalue"
)

// FilterProperty supplies a property's type and reader for [CompileFilter] and
// [SortRecords]. It does not add the property to a collection's storage schema.
type FilterProperty struct {
	// Type is the declared property type and must not be nil.
	Type reflect.Type
	// Value reads a property without modifying the record. It must not be nil.
	// Values must be assignable to Type, or nil. A missing property returns
	// found=false and is treated as nil. Resolved [PropertyModel.Value] methods
	// can be used directly.
	Value func(record any) (value any, found bool, err error)
}

// CompileFilter compiles a filter for local, in-process record evaluation.
// A nil filter matches every record and does not require a resolver. Otherwise,
// resolve must return a [FilterProperty] for each referenced record property
// name, or an error if the provider does not allow that property. Resolution
// happens during compilation, not during record evaluation.
//
// Logical operators short-circuit in operand order. Equality and membership use
// Equal(T) bool methods when available, otherwise [reflect.DeepEqual]. Ordering
// uses Compare(T) int methods, primitive ordering, or UUID byte ordering.
// Pointers and interfaces are unwrapped; relational comparisons involving nil
// or NaN do not match. Property-read errors are returned to the caller.
//
// Filters and their values are borrowed and must remain unchanged while the
// predicate is used. Readers and comparison methods must not mutate records.
// Providers using local evaluation must apply it before ranking and limiting
// results. Remote providers should translate filters to backend queries instead.
func CompileFilter(filter *vectordata.Filter, resolve func(string) (FilterProperty, error)) (func(any) (bool, error), error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if filter == nil {
		return func(any) (bool, error) { return true, nil }, nil
	}
	if resolve == nil {
		return nil, fmt.Errorf("vectordata: filter property resolver is required")
	}
	return filterNode(filter, resolve)
}

func filterNode(filter *vectordata.Filter, resolve func(string) (FilterProperty, error)) (func(any) (bool, error), error) {
	switch filter.Operator {
	case vectordata.And, vectordata.Or, vectordata.Not:
		operands := make([]func(any) (bool, error), len(filter.Operands))
		for i := range filter.Operands {
			operand, err := filterNode(&filter.Operands[i], resolve)
			if err != nil {
				return nil, err
			}
			operands[i] = operand
		}
		return func(record any) (bool, error) {
			for _, operand := range operands {
				matches, err := operand(record)
				if err != nil {
					return false, err
				}
				switch filter.Operator {
				case vectordata.Not:
					return !matches, nil
				case vectordata.And:
					if !matches {
						return false, nil
					}
				case vectordata.Or:
					if matches {
						return true, nil
					}
				}
			}
			return filter.Operator == vectordata.And, nil
		}, nil
	}
	property, err := resolve(filter.Property)
	if err != nil {
		return nil, err
	}
	if property.Type == nil || property.Value == nil {
		return nil, fmt.Errorf("vectordata: filter property %q requires a type and value reader", filter.Property)
	}
	value := reflect.ValueOf(filter.Value)
	switch filter.Operator {
	case vectordata.Contains:
		typ := recordvalue.ScalarType(property.Type)
		if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
			return nil, fmt.Errorf("contains requires a slice or array property, got %q (%v)", filter.Property, property.Type)
		}
		if err := checkFilterValue(typ.Elem(), value); err != nil {
			return nil, fmt.Errorf("property %q: %w", filter.Property, err)
		}
	case vectordata.In:
		for i := range value.Len() {
			if err := checkFilterValue(property.Type, value.Index(i)); err != nil {
				return nil, fmt.Errorf("property %q: %w", filter.Property, err)
			}
		}
	default:
		if err := checkFilterValue(property.Type, value); err != nil {
			return nil, fmt.Errorf("property %q: %w", filter.Property, err)
		}
		if filter.Operator != vectordata.Equal && filter.Operator != vectordata.NotEqual && !recordvalue.OrderableType(property.Type) {
			return nil, fmt.Errorf("property %q of type %v does not support ordered comparisons", filter.Property, property.Type)
		}
	}
	return func(record any) (bool, error) {
		input, found, err := property.Value(record)
		if err != nil {
			return false, err
		}
		var actual reflect.Value
		if found {
			actual = reflect.ValueOf(input)
		}
		switch filter.Operator {
		case vectordata.Equal:
			return recordvalue.Equal(actual, value), nil
		case vectordata.NotEqual:
			return !recordvalue.Equal(actual, value), nil
		case vectordata.Contains:
			actual = recordvalue.Indirect(actual)
			if actual.IsValid() {
				for i := range actual.Len() {
					if recordvalue.Equal(actual.Index(i), value) {
						return true, nil
					}
				}
			}
			return false, nil
		case vectordata.In:
			for i := range value.Len() {
				if recordvalue.Equal(actual, value.Index(i)) {
					return true, nil
				}
			}
			return false, nil
		default:
			a, b := recordvalue.Indirect(actual), recordvalue.Indirect(value)
			if !a.IsValid() || !b.IsValid() {
				return false, nil
			}
			if isNaN(a) || isNaN(b) {
				return false, nil
			}
			comparison, err := recordvalue.Compare(a, b)
			if err != nil {
				return false, fmt.Errorf("property %q: %w", filter.Property, err)
			}
			switch filter.Operator {
			case vectordata.GreaterThan:
				return comparison > 0, nil
			case vectordata.GreaterThanOrEqual:
				return comparison >= 0, nil
			case vectordata.LessThan:
				return comparison < 0, nil
			default:
				return comparison <= 0, nil
			}
		}
	}, nil
}

func checkFilterValue(typ reflect.Type, value reflect.Value) error {
	typ = recordvalue.ScalarType(typ)
	value = recordvalue.Indirect(value)
	if !value.IsValid() || typ.Kind() == reflect.Interface || typ == value.Type() {
		return nil
	}
	return fmt.Errorf("filter value has type %v, expected %v", value.Type(), typ)
}

func isNaN(value reflect.Value) bool {
	return (value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64) && math.IsNaN(value.Float())
}
