// Copyright (c) Microsoft. All rights reserved.

package embedding

import "maps"

// Options configures an embedding generation request. Nil options use provider
// defaults. Generators must not mutate the options; use [Options.Clone] when
// changes are needed.
type Options struct {
	// Dimensions requests a vector size. Nil uses the provider default.
	// Explicit zero is preserved. Supported values are provider-specific.
	Dimensions *int `json:",omitzero"`
	// ModelID overrides the generator's default model. Nil leaves it unchanged.
	ModelID              *string        `json:",omitzero"`
	AdditionalProperties map[string]any `json:",omitzero"`
	// RawRepresentationFactory creates provider-specific request options for
	// the supplied generator.
	// Providers may modify the returned value, so return a fresh instance on
	// each call. Providers apply the other Options fields to that instance.
	RawRepresentationFactory func(any) any `json:"-"`
}

// Clone returns an independent options object and a shallow copy of its
// AdditionalProperties map. Values within the map and the factory are shared.
// A nil receiver produces default options.
func (o *Options) Clone() *Options {
	if o == nil {
		return &Options{}
	}
	clone := *o
	if o.Dimensions != nil {
		clone.Dimensions = new(*o.Dimensions)
	}
	if o.ModelID != nil {
		clone.ModelID = new(*o.ModelID)
	}
	clone.AdditionalProperties = maps.Clone(o.AdditionalProperties)
	return &clone
}
