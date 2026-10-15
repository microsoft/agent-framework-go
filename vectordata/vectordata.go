// Copyright (c) Microsoft. All rights reserved.

// Package vectordata defines provider-independent vector store contracts,
// record schemas, retrieval options, and vector and keyword-hybrid searches.
//
// Store and Collection are implemented by database providers. They must support
// concurrent calls, honor context cancellation, and treat caller-supplied schemas,
// filters, and options as read-only. Record ownership is provider-specific:
// providers document whether records are copied or retained, whether returned
// records share storage, and whether generated keys are written into inputs.
//
// Collections use typed keys and records. Records may be structs, pointers to
// structs, or map[string]any for dynamic mapping. Providers combine vectordata
// struct tags with an explicit schema when constructing collection handles.
// Search values may be vectors, embeddings, or inputs for a provider's configured
// embedding generator. Experimental model-building utilities for providers live
// in the providerservices subpackage.
//
// Iterators perform work when consumed and must stop on the first error or when
// the consumer stops. A provider which owns resources may implement io.Closer;
// cleanup and service lookup are not required by these interfaces.
package vectordata

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/embedding"
)

// Store contains collections sharing the same key and record types.
// Providers may offer differently typed stores over the same client.
type Store[TKey, TRecord any] interface {
	// Collection returns a configured handle for the named collection without
	// creating it. Call [Collection.EnsureExists] to create the collection.
	Collection(name string, options CollectionOptions) (Collection[TKey, TRecord], error)

	// ListCollectionNames returns the names of the collections in the store.
	ListCollectionNames(ctx context.Context) iter.Seq2[string, error]

	// CollectionExists reports whether the named collection exists in the store.
	CollectionExists(ctx context.Context, name string) (bool, error)

	// EnsureCollectionDeleted deletes the named collection if it exists.
	EnsureCollectionDeleted(ctx context.Context, name string) error
}

// Collection manages a named collection and its records. Providers document
// which key, data, vector, filter, and index types they support.
type Collection[TKey, TRecord any] interface {
	Searcher[TRecord]

	// Name returns the collection's name.
	Name() string

	// Exists reports whether the collection exists in the store.
	Exists(ctx context.Context) (bool, error)

	// EnsureExists creates the collection if it does not already exist.
	EnsureExists(ctx context.Context) error

	// EnsureDeleted deletes the collection if it exists.
	EnsureDeleted(ctx context.Context) error

	// Get returns the record identified by key, or found=false if the key is
	// missing. It does not create the collection.
	// Use [GetBatch] for native or sequential batch retrieval.
	Get(ctx context.Context, key TKey, options *RecordRetrievalOptions) (TRecord, bool, error)

	// GetFiltered returns up to top records matching filter.
	GetFiltered(ctx context.Context, filter Filter, top int, options *FilteredRecordRetrievalOptions) iter.Seq2[TRecord, error]

	// Delete deletes the record identified by key if it exists. It does not
	// create the collection. Use [DeleteBatch] for native or sequential batch
	// deletion.
	Delete(ctx context.Context, key TKey) error

	// Upsert inserts or updates records as one batch. It accepts individual
	// records or an expanded slice. No records, a nil slice, and an empty slice
	// represent an empty batch. Implementations should generate embeddings once
	// per batch, not once per record. Providers document whether batches are atomic.
	Upsert(ctx context.Context, records ...TRecord) error
}

// Searcher performs vector similarity searches.
// Supported representations and distance functions depend on the provider.
type Searcher[TRecord any] interface {
	// Search returns up to top records closest to searchValue, which may be a
	// vector, an embedding, or an input supported by a configured embedding
	// generator. Providers must reject nil search values, nonpositive top,
	// invalid options, and ambiguous vector-property selection rather than
	// selecting a property silently. Filtering happens before ranking, and
	// Skip is applied before taking top.
	Search(ctx context.Context, searchValue any, top int, options *SearchOptions) iter.Seq2[SearchResult[TRecord], error]
}

// KeywordHybridSearcher is an optional capability combining vector similarity
// with full-text keyword search.
type KeywordHybridSearcher[TRecord any] interface {
	// HybridSearch combines vector similarity for searchValue with full-text
	// search for keywords using a full-text-indexed data property. Keywords,
	// search values, and options are read-only. Providers must reject an
	// ambiguous vector or full-text property when no property is selected explicitly.
	HybridSearch(ctx context.Context, searchValue any, keywords []string, top int, options *HybridSearchOptions) iter.Seq2[SearchResult[TRecord], error]
}

// SearchResult contains a matching record and its provider-specific score.
// A nil Score means the provider did not report a score; zero is a valid score.
// Similarity scores rank higher values first; distance scores rank lower values
// first. Scores are not normalized across providers.
type SearchResult[TRecord any] struct {
	Record TRecord
	Score  *float64
}

// CollectionOptions configures a collection handle. Definition overrides schema
// tags. EmbeddingGenerator is the provider's default embedding generator; vector
// property and definition generators take precedence, in that order.
//
// Use [embedding.Bind] to configure a typed generator. The input and
// output types may differ between vector properties. Providers resolve schema
// configuration and validate input compatibility, supported representations, and
// storage requirements when constructing a collection handle. The supplied
// definition is configuration, not a resolved runtime model, and is read-only.
type CollectionOptions struct {
	Definition         *Definition
	EmbeddingGenerator *embedding.BoundGenerator
}

// Error describes a vector store failure while preserving the original cause.
// Provider errors may use it to attach database, collection, and operation names.
type Error struct {
	SystemName     string
	StoreName      string
	CollectionName string
	OperationName  string
	Message        string
	Err            error
}

func (e *Error) Error() string {
	message := e.Message
	if e.Err != nil {
		if message != "" {
			message += ": "
		}
		message += e.Err.Error()
	}
	if message == "" {
		message = "vector store operation failed"
	}
	if e.OperationName != "" {
		return "vectordata: " + e.OperationName + ": " + message
	}
	return "vectordata: " + message
}

// Unwrap returns the underlying provider error.
func (e *Error) Unwrap() error { return e.Err }
