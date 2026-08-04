// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"encoding/json"
	"errors"
	"reflect"
)

// PortableValue represents a value that can be exported / imported to a workflow,
// e.g. through an external request/response, or through checkpointing.
// The zero PortableValue is invalid.
type PortableValue struct {
	_      [0]func() // disallow ==
	any    any
	cache  any
	TypeID TypeID
	// TODO: Optimize small values so they avoid allocations, like in slog.Value.
}

// AnyPortableValue returns a [PortableValue] for the supplied value.
//
// If the supplied value is of type PortableValue, it is returned
// unmodified.
func AnyPortableValue(v any) PortableValue {
	switch val := v.(type) {
	case PortableValue:
		return val
	case *PortableValue:
		if val == nil {
			panic("workflow: PortableValue cannot wrap nil")
		}
		return *val
	default:
		if v == nil {
			panic("workflow: PortableValue cannot wrap nil")
		}
		return PortableValue{any: v, TypeID: NewTypeID(reflect.TypeOf(v))}
	}
}

// PortableValueAs attempts to convert the supplied [PortableValue] to the requested type T.
func PortableValueAs[T any](v PortableValue) (T, bool) {
	if reflect.TypeFor[T]() == reflect.TypeFor[PortableValue]() {
		return any(v).(T), true
	}
	if !v.Is(reflect.TypeFor[T]()) {
		var zero T
		return zero, false
	}
	return v.Any().(T), true
}

// Any returns v's value as an any.
func (v *PortableValue) Any() any {
	if v.cache != nil {
		return v.cache
	}
	raw, ok := v.any.(json.RawMessage)
	if !ok {
		return v.any
	}
	if decoded, ok := decodeKnownPortableType(v.TypeID, raw); ok {
		v.cache = decoded
		return decoded
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		v.cache = decoded
		return decoded
	}
	return v.any
}

func decodeKnownPortableType(typeID TypeID, raw json.RawMessage) (any, bool) {
	if typeID.PackageName == "" {
		switch typeID.TypeName {
		case "string":
			return decodePortableJSON[string](raw)
		case "bool":
			return decodePortableJSON[bool](raw)
		case "int":
			return decodePortableJSON[int](raw)
		case "int8":
			return decodePortableJSON[int8](raw)
		case "int16":
			return decodePortableJSON[int16](raw)
		case "int32":
			return decodePortableJSON[int32](raw)
		case "int64":
			return decodePortableJSON[int64](raw)
		case "uint":
			return decodePortableJSON[uint](raw)
		case "uint8":
			return decodePortableJSON[uint8](raw)
		case "uint16":
			return decodePortableJSON[uint16](raw)
		case "uint32":
			return decodePortableJSON[uint32](raw)
		case "uint64":
			return decodePortableJSON[uint64](raw)
		case "float32":
			return decodePortableJSON[float32](raw)
		case "float64":
			return decodePortableJSON[float64](raw)
		}
	}
	if typ, ok := runtimeTypeForTypeID(typeID); ok {
		if decoded, ok := decodePortableJSONType(typ, raw); ok {
			return decoded, true
		}
	}
	return nil, false
}

func decodePortableJSONType(typ reflect.Type, raw json.RawMessage) (any, bool) {
	if typ == nil {
		return nil, false
	}
	target := reflect.New(typ)
	if err := json.Unmarshal(raw, target.Interface()); err != nil {
		return nil, false
	}
	return target.Elem().Interface(), true
}

func decodePortableJSON[T any](raw json.RawMessage) (any, bool) {
	var decoded T
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

// Is reports whether the value is of the specified type.
// If the value is stored in a delayed deserialized form, it will attempt to
// deserialize it to the requested type.
func (v *PortableValue) Is(typ reflect.Type) bool {
	if v == nil {
		return false
	}
	if typ == reflect.TypeFor[PortableValue]() {
		return true
	}
	if v.cache != nil && reflect.TypeOf(v.cache).AssignableTo(typ) {
		return true
	}
	raw, ok := v.any.(json.RawMessage)
	if !ok {
		// not a delayed deserialization
		return v.any != nil && reflect.TypeOf(v.any).AssignableTo(typ)
	}
	target := reflect.New(typ)
	// Either we have no cache, or the types are incompatible; see if we can deserialize to the requested type
	if err := json.Unmarshal(raw, target.Interface()); err != nil {
		return false
	}
	deserialized := target.Elem().Interface()
	if deserialized == nil || !reflect.TypeOf(deserialized).AssignableTo(typ) {
		return false
	}
	v.cache = deserialized
	return true
}

// As returns the contained value and true when it is assignable to typ (or can
// be delayed-deserialized to a value assignable to typ); otherwise it returns
// nil and false. No conversion is performed: when typ is an interface, the
// returned value is the concrete value assignable to that interface. It is the
// comma-ok extraction counterpart to [PortableValue.Is].
func (v *PortableValue) As(typ reflect.Type) (any, bool) {
	if v.Is(typ) {
		return v.Any(), true
	}
	return nil, false
}

// Delayed reports whether the value is stored in a delayed deserialized form.
func (v *PortableValue) Delayed() bool {
	if v.any == nil || v.cache != nil {
		return false
	}
	_, ok := v.any.(json.RawMessage)
	return ok
}

func (v PortableValue) MarshalJSON() ([]byte, error) {
	if v.any == nil {
		return nil, errors.New("cannot marshal zero PortableValue")
	}
	value := v.Any()
	if raw, ok := value.(json.RawMessage); ok {
		return json.Marshal(portableValueJSON{TypeID: v.TypeID, Value: raw})
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	typeID := v.TypeID
	if typeID == (TypeID{}) {
		typeID = NewTypeID(reflect.TypeOf(value))
	}
	return json.Marshal(portableValueJSON{TypeID: typeID, Value: data})
}

func (v *PortableValue) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return errors.New("cannot unmarshal null PortableValue")
	}
	var wire portableValueJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*v = PortableValue{any: wire.Value, TypeID: wire.TypeID}
	return nil
}

type portableValueJSON struct {
	TypeID TypeID
	Value  json.RawMessage
}
