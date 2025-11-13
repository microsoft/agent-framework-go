// Copyright (c) Microsoft. All rights reserved.

package format

// Format represents the desired format, e.g., for agent responses or tool input/output.
type Format interface {
	// Kind if the format type.
	// For example, "text" or "json".
	Kind() string
}

type simple struct {
	kind string
}

func (s simple) Kind() string {
	return s.kind
}

// JSON represents the JSON format.
// Use [format/jsonformat] for more advanced JSON schema-based formatting.
func JSON() Format {
	return simple{kind: "json"}
}

// Text represents the plain text format.
func Text() Format {
	return simple{kind: "text"}
}
