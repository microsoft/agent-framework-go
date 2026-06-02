// Copyright (c) Microsoft. All rights reserved.

package workflow

// OutputTag identifies the kind of output that an [OutputEvent] represents.
// It is a thin string wrapper with value equality, matching the .NET OutputTag struct.
// The constructor is unexported; use the well-known singleton [OutputTagIntermediate].
type OutputTag struct {
	value string
}

// OutputTagIntermediate is the tag denoting an intermediate workflow output.
// It is emitted by executors registered via [Builder.WithIntermediateOutputFrom].
// Terminal (non-intermediate) outputs carry no tags.
var OutputTagIntermediate = OutputTag{value: "intermediate"}

// String returns the string value of the tag.
func (t OutputTag) String() string {
	return t.value
}
