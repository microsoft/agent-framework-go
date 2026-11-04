// Copyright (c) Microsoft. All rights reserved.

package vectordata

import (
	"fmt"
	"reflect"
)

// FilterOperator identifies a portable filter operation. Provider support for
// individual operations and property types varies.
type FilterOperator string

const (
	Equal              FilterOperator = "eq"
	NotEqual           FilterOperator = "ne"
	GreaterThan        FilterOperator = "gt"
	GreaterThanOrEqual FilterOperator = "ge"
	LessThan           FilterOperator = "lt"
	LessThanOrEqual    FilterOperator = "le"
	Contains           FilterOperator = "contains"
	In                 FilterOperator = "in"
	And                FilterOperator = "and"
	Or                 FilterOperator = "or"
	Not                FilterOperator = "not"
)

// Filter is a provider-translatable predicate over record property names.
// Comparisons use Property and Value. Equal and NotEqual support nil values.
// Contains tests whether a collection-valued property contains Value. In tests
// whether the property's value occurs in a slice or array supplied as Value.
//
// And and Or combine two or more Operands; Not negates a single operand.
// Logical nodes do not use Property or Value. Providers must return errors for
// unsupported operations instead of dropping predicates or filtering only after
// retrieving a limited result set. Filters and values are read-only.
type Filter struct {
	Operator FilterOperator
	Property string
	Value    any
	Operands []Filter
}

// Validate checks the structure of a filter. A nil receiver means no filter.
// Property existence, data-type compatibility, and operator support are
// validated by the provider.
func (f *Filter) Validate() error {
	return validateFilter(f, make(map[*Filter]bool))
}

func validateFilter(f *Filter, visiting map[*Filter]bool) error {
	if f == nil {
		return nil
	}
	if visiting[f] {
		return fmt.Errorf("vectordata: filter contains a cycle")
	}
	visiting[f] = true
	defer delete(visiting, f)
	switch f.Operator {
	case And, Or, Not:
		if f.Property != "" || f.Value != nil {
			return fmt.Errorf("vectordata: logical filter %q cannot specify a property or value", f.Operator)
		}
		if f.Operator == Not && len(f.Operands) != 1 || f.Operator != Not && len(f.Operands) < 2 {
			return fmt.Errorf("vectordata: invalid operand count for filter %q", f.Operator)
		}
		for i := range f.Operands {
			if err := validateFilter(&f.Operands[i], visiting); err != nil {
				return err
			}
		}
	case Equal, NotEqual, GreaterThan, GreaterThanOrEqual, LessThan, LessThanOrEqual, Contains, In:
		if f.Property == "" || len(f.Operands) != 0 {
			return fmt.Errorf("vectordata: filter %q requires a property and no operands", f.Operator)
		}
		if f.Operator == In {
			valueType := reflect.TypeOf(f.Value)
			if valueType == nil || valueType.Kind() != reflect.Slice && valueType.Kind() != reflect.Array {
				return fmt.Errorf("vectordata: in filter requires a slice or array value")
			}
		}
	default:
		return fmt.Errorf("vectordata: unsupported filter operator %q", f.Operator)
	}
	return nil
}
