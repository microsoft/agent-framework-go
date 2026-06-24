// Copyright (c) Microsoft. All rights reserved.

package jsonx

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// UnmarshalDiscriminatedUnionSlice unmarshals a JSON array of union types into a slice of type T.
// The types map should map from a discriminator key to the corresponding reflect.Type of T.
func UnmarshalDiscriminatedUnionSlice[T any, K comparable](data []byte, types map[K]reflect.Type) ([]T, error) {
	return UnmarshalDiscriminatedUnionSliceWithFallback[T, K](data, types, nil)
}

// UnmarshalDiscriminatedUnionSliceWithFallback unmarshals a JSON array of union types into a slice of type T.
// The fallback function is used when an item has a missing or unsupported discriminator value.
func UnmarshalDiscriminatedUnionSliceWithFallback[T any, K comparable](
	data []byte,
	types map[K]reflect.Type,
	fallback func(json.RawMessage) (T, error),
) ([]T, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	// Create appropriate content instances based on type.
	out := make([]T, 0, len(items))
	for _, item := range items {
		var header struct {
			Type *K
		}
		if err := json.Unmarshal(item, &header); err != nil {
			return nil, err
		}
		if header.Type == nil {
			if fallback == nil {
				var zero K
				return nil, fmt.Errorf("unsupported content type: %v", zero)
			}
			value, err := fallback(item)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
			continue
		}
		typ, ok := types[*header.Type]
		if !ok {
			if fallback == nil {
				return nil, fmt.Errorf("unsupported content type: %v", *header.Type)
			}
			value, err := fallback(item)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
			continue
		}

		value := reflect.New(typ).Interface().(T)
		if err := json.Unmarshal(item, value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}
