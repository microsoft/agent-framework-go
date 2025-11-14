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
	// Unmarshal just the type fields to determine content types.
	var items []struct {
		Type K
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	// Create appropriate content instances based on type.
	out := make([]T, 0, len(items))
	for _, item := range items {
		typ, ok := types[item.Type]
		if !ok {
			return nil, fmt.Errorf("unsupported content type: %v", item.Type)
		}
		out = append(out, reflect.New(typ).Interface().(T))
	}

	// Unmarshal the full data into the content instances.
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
