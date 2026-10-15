// Copyright (c) Microsoft. All rights reserved.

// Package recordvalue provides shared local filtering and ordering semantics.
package recordvalue

import (
	"bytes"
	"cmp"
	"fmt"
	"reflect"

	"github.com/google/uuid"
)

// ScalarType unwraps pointer types, stopping at the first repeated type.
func ScalarType(typ reflect.Type) reflect.Type {
	seen := make(map[reflect.Type]bool)
	for typ.Kind() == reflect.Pointer && !seen[typ] {
		seen[typ] = true
		typ = typ.Elem()
	}
	return typ
}

// Indirect unwraps pointers and interfaces. Nil becomes an invalid value;
// cycles stop at the first repeated pointer.
func Indirect(value reflect.Value) reflect.Value {
	type pointerVisit struct {
		typ reflect.Type
		ptr uintptr
	}

	seen := make(map[pointerVisit]bool)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}
		}
		if value.Kind() == reflect.Pointer {
			visit := pointerVisit{typ: value.Type(), ptr: uintptr(value.UnsafePointer())}
			if seen[visit] {
				return value
			}
			seen[visit] = true
		}
		value = value.Elem()
	}
	return value
}

// IsNil reports whether value is invalid or contains nil without dereferencing.
func IsNil(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Slice, reflect.Pointer, reflect.Interface:
		return value.IsNil()
	case reflect.UnsafePointer:
		return value.Pointer() == 0
	default:
		return false
	}
}

// Equal compares unwrapped values using Equal(T) bool or reflect.DeepEqual.
func Equal(x, y reflect.Value) bool {
	x, y = Indirect(x), Indirect(y)
	if IsNil(x) || IsNil(y) {
		return IsNil(x) && IsNil(y)
	}
	if x.Type() == y.Type() {
		if method, ok := comparisonMethod(x.Type(), "Equal", reflect.TypeFor[bool]()); ok {
			return method.Func.Call([]reflect.Value{x, y})[0].Bool()
		}
	}
	return reflect.DeepEqual(x.Interface(), y.Interface())
}

// OrderableType reports whether a type supports local ordering. Interface
// values are checked at comparison time.
func OrderableType(typ reflect.Type) bool {
	typ = ScalarType(typ)
	if typ == reflect.TypeFor[uuid.UUID]() {
		return true
	}
	switch typ.Kind() {
	case reflect.Interface, reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	default:
		_, ok := comparisonMethod(typ, "Compare", reflect.TypeFor[int]())
		return ok
	}
}

func comparisonMethod(typ reflect.Type, name string, result reflect.Type) (reflect.Method, bool) {
	method, ok := typ.MethodByName(name)
	if !ok || !method.Func.IsValid() || method.Type.IsVariadic() || method.Type.NumIn() != 2 ||
		method.Type.In(1) != typ || method.Type.NumOut() != 1 || method.Type.Out(0) != result {
		return reflect.Method{}, false
	}
	return method, true
}

// Compare orders values already unwrapped by Indirect, with nil values first.
func Compare(x, y reflect.Value) (int, error) {
	if !x.IsValid() {
		if !y.IsValid() {
			return 0, nil
		}
		return -1, nil
	}
	if !y.IsValid() {
		return 1, nil
	}
	if x.Type() != y.Type() {
		return 0, fmt.Errorf("cannot compare %v and %v", x.Type(), y.Type())
	}
	if x.Type() == reflect.TypeFor[uuid.UUID]() {
		a, b := x.Interface().(uuid.UUID), y.Interface().(uuid.UUID)
		return bytes.Compare(a[:], b[:]), nil
	}
	if method, ok := comparisonMethod(x.Type(), "Compare", reflect.TypeFor[int]()); ok {
		comparison := method.Func.Call([]reflect.Value{x, y})[0].Int()
		return cmp.Compare(comparison, 0), nil
	}
	switch x.Kind() {
	case reflect.Bool:
		if x.Bool() == y.Bool() {
			return 0, nil
		}
		if x.Bool() {
			return 1, nil
		}
		return -1, nil
	case reflect.String:
		return cmp.Compare(x.String(), y.String()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(x.Int(), y.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmp.Compare(x.Uint(), y.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return cmp.Compare(x.Float(), y.Float()), nil
	default:
		return 0, fmt.Errorf("type %v does not support ordering", x.Type())
	}
}
