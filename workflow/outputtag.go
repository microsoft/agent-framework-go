// Copyright (c) Microsoft. All rights reserved.

package workflow

// OutputTag identifies the kind of output that an [OutputEvent] represents.
// Tags accumulate across repeated [Builder.WithOutputFrom] calls; an executor
// registered without a tag carries an empty set (terminal/regular output).
// Use the predefined [OutputTag.Intermediate] singleton rather than
// constructing raw OutputTag values directly.
type OutputTag struct {
	value string
}

// outputTagIntermediate is the value used for the Intermediate tag.
const outputTagIntermediate = "intermediate"

// Intermediate is the tag that marks an output as an intermediate workflow
// output — one emitted by an executor registered via
// [Builder.WithIntermediateOutputFrom]. Terminal (non-intermediate) outputs
// carry no tag.
var Intermediate = OutputTag{value: outputTagIntermediate}

// String returns the string identifier of the tag.
func (t OutputTag) String() string { return t.value }

// IsIntermediate reports whether the event carries the [Intermediate] tag.
func (e OutputEvent) IsIntermediate() bool {
	for _, t := range e.Tags {
		if t == Intermediate {
			return true
		}
	}
	return false
}
