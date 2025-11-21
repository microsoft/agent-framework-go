// Copyright (c) Microsoft. All rights reserved.

package param

import (
	"encoding/json"
	"fmt"
)

func NewOpt[T comparable](v T) Opt[T] {
	return Opt[T]{value: v, status: included}
}

// Null creates optional field with the JSON value "null".
//
// To set a struct to null, use [NullStruct].
func Null[T comparable]() Opt[T] { return Opt[T]{status: null} }

type status int8

const (
	omitted status = iota
	null
	included
)

// Opt represents an optional parameter of type T. Use
// the [Opt.Valid] method to confirm.
type Opt[T comparable] struct {
	value T
	// indicates whether the field should be omitted, null, or valid
	status status
}

// Valid returns true if the value is not "null" or omitted.
//
// To check if explicitly null, use [Opt.Null].
func (o Opt[T]) Valid() bool {
	var empty Opt[T]
	return o.status == included || o != empty && o.status != null
}

// Value returns the underlying value and a boolean indicating whether it is valid.
func (o Opt[T]) Value() (T, bool) {
	return o.value, o.Valid()
}

func (o Opt[T]) MustValue() T {
	if !o.Valid() {
		panic("attempted to get value of invalid Opt")
	}
	return o.value
}

func (o Opt[T]) Or(v T) T {
	if o.Valid() {
		return o.value
	}
	return v
}

func (o Opt[T]) OrOpt(v Opt[T]) Opt[T] {
	if o.Valid() {
		return o
	}
	return v
}

func (o Opt[T]) String() string {
	if o.null() {
		return "null"
	}
	switch v := any(o.value).(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", o.value)
	}
}

func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if !o.Valid() {
		return []byte("null"), nil
	}
	return json.Marshal(o.value)
}

func (o *Opt[T]) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		o.status = null
		return nil
	}

	var value *T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	if value == nil {
		o.status = omitted
		return nil
	}

	o.status = included
	o.value = *value
	return nil
}

func (o Opt[T]) null() bool { return o.status == null }
