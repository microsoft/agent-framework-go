// Copyright (c) Microsoft. All rights reserved.

// Package format defines response format descriptions shared by agents and tools.
package format

// ResponseFormat represents the response format that is desired by the caller.
type ResponseFormat struct {
	// Kind is the format type.
	// For example, "text" or "json".
	Kind string

	Name        string
	Description string
	Strict      bool

	// Schema is the schema that defines the format.
	Schema any
}
