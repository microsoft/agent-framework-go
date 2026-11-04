// Copyright (c) Microsoft. All rights reserved.

package featureusage

type taggedValue struct {
	value any
	index int
}

// Tag carries a workflow feature index through Builder construction without
// changing the resulting executor binding's RawValue.
func Tag(value any, index int) any {
	return taggedValue{value: value, index: index}
}

// Unwrap returns a tagged RawValue and its workflow feature index.
func Unwrap(value any) (rawValue any, index int, ok bool) {
	tagged, ok := value.(taggedValue)
	if !ok {
		return value, 0, false
	}
	return tagged.value, tagged.index, true
}
