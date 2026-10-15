// Copyright (c) Microsoft. All rights reserved.

package vectordata

import "fmt"

// RecordRetrievalOptions controls retrieval of one or more records.
// IncludeVectors defaults to false, requesting vector exclusion. Providers may
// document exceptions, such as in-memory storage returning native vectors directly.
type RecordRetrievalOptions struct {
	IncludeVectors bool
}

// SearchOptions controls vector search. Property names refer to record fields,
// not storage names. An empty VectorProperty selects the sole vector property;
// no vectors or multiple vectors require an explicit selection or an error.
// A nil Filter does not filter records. Skip defaults to zero.
//
// ScoreThreshold includes scores equal to the threshold. Similarity functions
// discard lower scores; distance functions discard higher scores. The score
// range and supported threshold behavior depend on the provider.
// IncludeVectors defaults to false; provider-specific exceptions may apply.
type SearchOptions struct {
	Filter         *Filter
	VectorProperty string
	Skip           int
	IncludeVectors bool
	ScoreThreshold *float64
}

// Validate checks provider-independent option constraints. A nil receiver uses
// defaults. Providers are responsible for validating property selections and
// support for filters and score thresholds.
func (o *SearchOptions) Validate() error {
	if o == nil {
		return nil
	}
	if o.Skip < 0 {
		return fmt.Errorf("vectordata: skip must be nonnegative")
	}
	return o.Filter.Validate()
}

// HybridSearchOptions controls combined vector and keyword search.
// AdditionalProperty selects a full-text-indexed data property by record name.
// If empty, the collection must have exactly one such property.
// ScoreThreshold applies to the provider's combined relevance score.
type HybridSearchOptions struct {
	Filter             *Filter
	VectorProperty     string
	AdditionalProperty string
	Skip               int
	IncludeVectors     bool
	ScoreThreshold     *float64
}

// Validate checks provider-independent option constraints. A nil receiver uses
// defaults. Providers validate property and hybrid-search support.
func (o *HybridSearchOptions) Validate() error {
	if o == nil {
		return nil
	}
	if o.Skip < 0 {
		return fmt.Errorf("vectordata: skip must be nonnegative")
	}
	return o.Filter.Validate()
}

// Sort orders filtered records by a record property. The zero value of
// Descending requests ascending order. Multiple sorts are applied in slice order.
type Sort struct {
	Property   string
	Descending bool
}

// FilteredRecordRetrievalOptions controls retrieval by a filter without vector
// ranking. With no OrderBy, result order is provider-dependent.
// IncludeVectors defaults to false; provider-specific exceptions may apply.
type FilteredRecordRetrievalOptions struct {
	Skip           int
	OrderBy        []Sort
	IncludeVectors bool
}

// Validate checks pagination and ordering. A nil receiver uses defaults.
func (o *FilteredRecordRetrievalOptions) Validate() error {
	if o == nil {
		return nil
	}
	if o.Skip < 0 {
		return fmt.Errorf("vectordata: skip must be nonnegative")
	}
	for i, sort := range o.OrderBy {
		if sort.Property == "" {
			return fmt.Errorf("vectordata: sort at index %d requires a property", i)
		}
	}
	return nil
}
