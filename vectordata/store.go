// Copyright (c) Microsoft. All rights reserved.

package vectordata

import (
	"context"
	"fmt"
	"io"
	"iter"
	"reflect"
)

// StoreProvider implements the database operations behind a [Store].
// Implementations must support concurrent calls and treat inputs as read-only.
type StoreProvider interface {
	// OpenCollection returns a handle without creating the collection.
	// keyType and recordType describe the caller's Go types. The provider must
	// validate their compatibility with the schema and retain the actual record
	// types across operations; the any parameters are only a dispatch boundary.
	// Optional batch, hybrid-search, and io.Closer capabilities are preserved
	// by the store's typed collection adapters.
	OpenCollection(name string, keyType, recordType reflect.Type, definition *Definition) (Collection[any, any], error)

	ListCollectionNames(ctx context.Context) iter.Seq2[string, error]
	CollectionExists(ctx context.Context, name string) (bool, error)
	EnsureCollectionDeleted(ctx context.Context, name string) error
}

// Store manages collections that may have different key and record types.
// Create one with [NewStore] or a database provider's constructor. Use
// [Store.DynamicCollection] for map records or [GetCollection] for typed records.
// Go 1.27 also supports the generic Collection method.
type Store struct {
	provider StoreProvider
}

// NewStore wraps a database provider. It panics if provider is nil.
func NewStore(provider StoreProvider) *Store {
	if provider == nil {
		panic("vector store provider is required")
	}
	return &Store{provider: provider}
}

// DynamicCollection returns a handle for map records without creating the
// collection. The definition must be non-nil and specify explicit property types.
// Keys use the schema's key type. This method is available on all supported Go
// versions. Call [Collection.EnsureExists] to create the collection.
func (s *Store) DynamicCollection(name string, definition *Definition) (Collection[any, map[string]any], error) {
	if definition == nil {
		return nil, fmt.Errorf("vectordata: dynamic collections require a definition")
	}
	return GetCollection[any, map[string]any](s, name, definition)
}

// GetCollection returns a typed handle without creating the collection.
// A nil definition uses the record's vectordata tags.
// Providers validate the requested types and schema. The returned collection
// preserves record identity, provider errors, and optional provider capabilities.
// It reports an error if a provider returns a record of an incompatible type.
//
// This function supports Go 1.26 callers. On Go 1.27, s.Collection[TKey, TRecord]
// provides the same operation as a generic method.
func GetCollection[TKey, TRecord any](s *Store, name string, definition *Definition) (Collection[TKey, TRecord], error) {
	if s == nil || s.provider == nil {
		return nil, fmt.Errorf("vectordata: store is not initialized")
	}
	raw, err := s.provider.OpenCollection(name, reflect.TypeFor[TKey](), reflect.TypeFor[TRecord](), definition)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("vectordata: provider returned a nil collection")
	}
	collection := &typedCollection[TKey, TRecord]{raw: raw}
	closer, canClose := raw.(io.Closer)
	if hybrid, ok := raw.(KeywordHybridSearcher[any]); ok {
		collection := &typedHybridCollection[TKey, TRecord]{typedCollection: collection, hybrid: hybrid}
		if canClose {
			return &struct {
				*typedHybridCollection[TKey, TRecord]
				io.Closer
			}{collection, closer}, nil
		}
		return collection, nil
	}
	if canClose {
		return &struct {
			*typedCollection[TKey, TRecord]
			io.Closer
		}{collection, closer}, nil
	}
	return collection, nil
}

// ListCollectionNames returns the names of the collections in the store.
func (s *Store) ListCollectionNames(ctx context.Context) iter.Seq2[string, error] {
	return s.provider.ListCollectionNames(ctx)
}

// CollectionExists reports whether the named collection exists.
func (s *Store) CollectionExists(ctx context.Context, name string) (bool, error) {
	return s.provider.CollectionExists(ctx, name)
}

// EnsureCollectionDeleted deletes the named collection if it exists.
func (s *Store) EnsureCollectionDeleted(ctx context.Context, name string) error {
	return s.provider.EnsureCollectionDeleted(ctx, name)
}
