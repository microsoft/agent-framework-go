// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// OutputTag identifies the kind of output represented by an [OutputEvent].
// Terminal outputs are untagged; intermediate outputs carry [OutputTagIntermediate].
type OutputTag string

// OutputTagIntermediate marks an output as intermediate rather than terminal.
const OutputTagIntermediate OutputTag = "intermediate"

// Value returns the string identifier of the tag.
func (t OutputTag) Value() string {
	return string(t)
}

func (t OutputTag) String() string {
	return string(t)
}

// MarshalJSON implements [json.Marshaler].
func (t OutputTag) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(t))
}

// UnmarshalJSON implements [json.Unmarshaler].
func (t *OutputTag) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("workflow: output tag value cannot be empty")
	}
	*t = OutputTag(value)
	return nil
}

func cloneOutputExecutors(in map[string]map[OutputTag]struct{}) map[string]map[OutputTag]struct{} {
	out := make(map[string]map[OutputTag]struct{}, len(in))
	for id, tags := range in {
		out[id] = maps.Clone(tags)
	}
	return out
}

func outputTagsFromSet(tags map[OutputTag]struct{}) []OutputTag {
	if len(tags) == 0 {
		return nil
	}
	out := slices.Collect(maps.Keys(tags))
	slices.Sort(out)
	return out
}
