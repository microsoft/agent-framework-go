// Copyright (c) Microsoft. All rights reserved.

package providerservices

import (
	"fmt"
	"reflect"
)

type propertyAccessor struct {
	recordType reflect.Type
	info       propertyInfo
	index      []int
}

func (a *propertyAccessor) configureAccessor(recordType reflect.Type, info propertyInfo, index []int) {
	a.recordType = recordType
	a.info = info
	a.index = index
}

// Value reads a property from a record using accessors configured by [Build].
// See [PropertyModel.Value] for ownership and missing-value semantics.
func (a *propertyAccessor) Value(record any) (any, bool, error) {
	root, err := a.recordValue(record)
	if err != nil {
		return nil, false, err
	}
	if root.Kind() == reflect.Map {
		value := root.MapIndex(reflect.ValueOf(a.info.name))
		if !value.IsValid() {
			return nil, false, nil
		}
		value = value.Elem()
		if !value.IsValid() {
			return nil, true, nil
		}
		if !value.Type().AssignableTo(a.info.typ) {
			return nil, true, fmt.Errorf("vectordata: property %q has type %v, expected %v", a.info.name, value.Type(), a.info.typ)
		}
		return value.Interface(), true, nil
	}
	value, err := root.FieldByIndexErr(a.index)
	if err != nil {
		return nil, false, nil
	}
	return value.Interface(), true, nil
}

// SetValue writes a property using accessors configured by [Build].
// See [PropertyModel.SetValue] for assignment and mutation semantics.
func (a *propertyAccessor) SetValue(record, value any) error {
	root, err := a.recordValue(record)
	if err != nil {
		return err
	}
	assignment := reflect.ValueOf(value)
	if assignment.IsValid() && !assignment.Type().AssignableTo(a.info.typ) {
		return fmt.Errorf("vectordata: property %q cannot accept %v, expected %v", a.info.name, assignment.Type(), a.info.typ)
	}
	if root.Kind() == reflect.Map {
		if !assignment.IsValid() {
			assignment = reflect.Zero(reflect.TypeFor[any]())
		}
		root.SetMapIndex(reflect.ValueOf(a.info.name), assignment)
		return nil
	}
	if !root.CanSet() {
		return fmt.Errorf("vectordata: property %q cannot be assigned; pass a pointer to the record", a.info.name)
	}
	if !assignment.IsValid() {
		switch a.info.typ.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
			assignment = reflect.Zero(a.info.typ)
		default:
			return fmt.Errorf("vectordata: property %q of type %v cannot be nil", a.info.name, a.info.typ)
		}
	}
	for i, index := range a.index {
		field := root.Field(index)
		if i == len(a.index)-1 {
			if !field.CanSet() {
				return fmt.Errorf("vectordata: property %q cannot be assigned; pass a pointer to the record", a.info.name)
			}
			field.Set(assignment)
			return nil
		}
		if field.Kind() == reflect.Pointer {
			if field.IsNil() {
				if !field.CanSet() {
					return fmt.Errorf("vectordata: property %q has an inaccessible embedded pointer", a.info.name)
				}
				field.Set(reflect.New(field.Type().Elem()))
			}
			field = field.Elem()
		}
		root = field
	}
	return fmt.Errorf("vectordata: property %q has no field", a.info.name)
}

func (a *propertyAccessor) recordValue(record any) (reflect.Value, error) {
	if a.recordType == nil {
		return reflect.Value{}, fmt.Errorf("vectordata: property accessors must be configured by Build")
	}
	value := reflect.ValueOf(record)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() == reflect.Map && value.IsNil() {
		return reflect.Value{}, fmt.Errorf("vectordata: record must not be nil")
	}
	if value.Type() != a.recordType {
		return reflect.Value{}, fmt.Errorf("vectordata: property %q requires record type %v, got %v", a.info.name, a.recordType, value.Type())
	}
	return value, nil
}
