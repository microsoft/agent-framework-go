// Copyright (c) Microsoft. All rights reserved.

// Package inmemory implements an in-process vector store for testing and
// prototyping. Each store owns independent collections; nothing is persisted
// unless explicitly exported.
//
// Collections support structs, pointers to structs, and map[string]any records.
// Keys must be non-nil comparable values without NaN. Records are retained and
// returned without cloning: pointer and map records, and reference-valued fields
// in struct records, share their underlying data with the caller. Plain structs
// are copied by value. Collection dictionaries support concurrent operations.
// Shared record data is not synchronized: callers must prevent concurrent
// mutation, including upserts that generate keys on the same record. Custom JSON
// marshalers also receive the stored records directly.
//
// Native vectors are []float32, embedding.VectorOf[float32], embedding.Of[float32],
// and *embedding.Of[float32]. Searches scan all matching records, regardless of
// index hints. Supported metrics are cosine similarity (the default), cosine
// distance, dot product similarity, and Euclidean distance. Hybrid search is not
// supported. Result order among equal scores is unspecified.
//
// Filters and sorting order primitive values and UUIDs. After dereferencing
// pointers and interfaces, a type T with a Compare(T) int method uses that method
// in preference to primitive ordering. Comparison methods must not mutate their
// operands. UUIDs use lexicographic byte ordering. Equality uses Equal(T) bool
// methods when available, otherwise [reflect.DeepEqual]. Nil values are compared
// without calling methods.
//
// Native vectors are always returned, regardless of IncludeVectors. Generated
// vectors are stored separately from their input fields; IncludeVectors=true is
// unsupported for collections with generated fields. Generated UUIDs are written
// into records. Use pointer or map records to observe those keys on the input.
package inmemory

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"sync"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
)

// Config configures an in-memory store.
type Config struct {
	// EmbeddingGenerator supplies the default generator for collection handles.
	// Definition and property configuration may override it.
	EmbeddingGenerator *embedding.BoundGenerator
}

// Store holds independently named collections with different key and record
// types. Create it with [New]. Its embedded [vectordata.Store] provides collection
// factories and lifecycle operations; pass that field to provider-independent
// APIs. A Store must not be copied after use. Its methods support concurrent use.
type Store struct {
	*vectordata.Store
	provider *store
}

type store struct {
	mu          sync.RWMutex
	collections map[string]*collectionData
	generator   *embedding.BoundGenerator
}

type collectionData struct {
	recordType reflect.Type
	records    map[any]*storedRecord
}

// New creates a store. The generator is borrowed, not closed by the store.
func New(config Config) *Store {
	provider := &store{generator: config.EmbeddingGenerator}
	return &Store{Store: vectordata.NewStore(provider), provider: provider}
}

func (s *store) OpenCollection(name string, keyType, recordType reflect.Type, definition *vectordata.Definition) (vectordata.Collection[any, any], error) {
	return newCollection[any, any](s, name, keyType, recordType, definition)
}

// ListCollectionNames lazily returns a snapshot of the existing collection names.
// The order is unspecified.
func (s *store) ListCollectionNames(ctx context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		if err := ctx.Err(); err != nil {
			yield("", failure("", "ListCollectionNames", err))
			return
		}
		s.mu.RLock()
		names := make([]string, 0, len(s.collections))
		for name := range s.collections {
			names = append(names, name)
		}
		s.mu.RUnlock()
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				yield("", failure("", "ListCollectionNames", err))
				return
			}
			if !yield(name, nil) {
				return
			}
		}
	}
}

// CollectionExists reports whether the named collection exists.
func (s *store) CollectionExists(ctx context.Context, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, failure(name, "CollectionExists", err)
	}
	s.mu.RLock()
	_, exists := s.collections[name]
	s.mu.RUnlock()
	return exists, nil
}

// EnsureCollectionDeleted removes the collection and all of its records.
// Deleting a missing collection succeeds. Existing handles can recreate it.
func (s *store) EnsureCollectionDeleted(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return failure(name, "EnsureCollectionDeleted", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return failure(name, "EnsureCollectionDeleted", err)
	}
	delete(s.collections, name)
	return nil
}

func (s *store) snapshot(ctx context.Context, name string, recordType reflect.Type) ([]*storedRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, exists := s.collections[name]
	if !exists {
		return nil, missingCollection(name)
	}
	if recordType != nil && data.recordType != recordType {
		return nil, incompatibleCollection(name, data.recordType, recordType)
	}
	records := make([]*storedRecord, 0, len(data.records))
	for _, record := range data.records {
		records = append(records, record)
	}
	return records, nil
}

type storedRecord struct {
	Record  any                  `json:"record"`
	Vectors map[string][]float32 `json:"vectors,omitempty"`
}

func failure(name, operation string, err error) error {
	return &vectordata.Error{
		SystemName: "inmemory", CollectionName: name, OperationName: operation, Err: err,
	}
}

func missingCollection(name string) error {
	return fmt.Errorf("collection %q does not exist", name)
}

func incompatibleCollection(name string, existing, requested reflect.Type) error {
	return fmt.Errorf("collection %q has record type %v, not %v", name, existing, requested)
}

var _ vectordata.StoreProvider = (*store)(nil)
