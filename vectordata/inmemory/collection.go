// Copyright (c) Microsoft. All rights reserved.

package inmemory

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

// Collection is a configured handle to an in-memory collection. Construct one
// with [Store.Collection] or [NewCollection]. Its schema is fixed at construction.
type Collection[K comparable, R any] struct {
	store  *Store[K, R]
	name   string
	model  *providerservices.CollectionModel
	schema recordSchema
}

// NewCollection creates a handle backed by its own independent store.
// The collection does not exist until [Collection.EnsureExists] is called.
func NewCollection[K comparable, R any](name string, options vectordata.CollectionOptions) (*Collection[K, R], error) {
	return new(Store[K, R]).collection(name, options)
}

func (s *Store[K, R]) collection(name string, options vectordata.CollectionOptions) (*Collection[K, R], error) {
	if strings.TrimSpace(name) == "" {
		return nil, failure(name, "Collection", fmt.Errorf("collection name is required"))
	}
	if options.EmbeddingGenerator == nil {
		options.EmbeddingGenerator = s.generator
	}
	model, err := providerservices.Build[K, R](options, providerservices.Config{
		SupportsMultipleVectors: true,
		SupportsKeyType:         reflect.Type.Comparable,
		SupportsDataType:        func(reflect.Type) bool { return true },
		SupportsVectorType:      vectorTypeSupported,
		SupportsKeyAutoGeneration: func(typ reflect.Type) bool {
			return typ == reflect.TypeFor[uuid.UUID]()
		},
	})
	if err != nil {
		return nil, failure(name, "Collection", err)
	}
	schema := newRecordSchema(reflect.TypeFor[R](), model)
	for _, field := range schema.vectors {
		if field.vector.Type != field.vector.EmbeddingType && field.vector.EmbeddingGenerator == nil {
			return nil, failure(name, "Collection", fmt.Errorf("property %q requires an embedding generator; in-memory storage cannot generate embeddings itself", field.name))
		}
	}
	return &Collection[K, R]{store: s, name: name, model: model, schema: schema}, nil
}

// Name returns the collection name.
func (c *Collection[K, R]) Name() string { return c.name }

// Exists reports whether the collection exists.
func (c *Collection[K, R]) Exists(ctx context.Context) (bool, error) {
	return c.store.CollectionExists(ctx, c.name)
}

// EnsureExists creates the collection if it does not already exist.
func (c *Collection[K, R]) EnsureExists(ctx context.Context) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return failure(c.name, "EnsureExists", err)
	}
	if c.store.collections == nil {
		c.store.collections = make(map[string]map[K]*storedRecord[R])
	}
	if _, exists := c.store.collections[c.name]; !exists {
		c.store.collections[c.name] = make(map[K]*storedRecord[R])
	}
	return nil
}

// EnsureDeleted deletes the collection if it exists.
func (c *Collection[K, R]) EnsureDeleted(ctx context.Context) error {
	return c.store.EnsureCollectionDeleted(ctx, c.name)
}

// Get returns the stored record, or found=false for a missing key.
// A missing collection is an error. Native vectors are always included.
// IncludeVectors=true is unsupported when any field requires embedding generation.
func (c *Collection[K, R]) Get(ctx context.Context, key K, options *vectordata.RecordRetrievalOptions) (R, bool, error) {
	var zero R
	if err := ctx.Err(); err != nil {
		return zero, false, failure(c.name, "Get", err)
	}
	include := options != nil && options.IncludeVectors
	if err := c.validateRetrieval(include); err != nil {
		return zero, false, failure(c.name, "Get", err)
	}
	if err := validateKey(reflect.ValueOf(key)); err != nil {
		return zero, false, failure(c.name, "Get", err)
	}
	c.store.mu.RLock()
	records, exists := c.store.collections[c.name]
	record, found := records[key]
	c.store.mu.RUnlock()
	if !exists {
		return zero, false, failure(c.name, "Get", missingCollection(c.name))
	}
	if !found {
		return zero, false, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, false, failure(c.name, "Get", err)
	}
	return record.Record, true, nil
}

// Delete deletes a record if it exists. A missing collection is an error.
func (c *Collection[K, R]) Delete(ctx context.Context, key K) error {
	if err := ctx.Err(); err != nil {
		return failure(c.name, "Delete", err)
	}
	if err := validateKey(reflect.ValueOf(key)); err != nil {
		return failure(c.name, "Delete", err)
	}
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return failure(c.name, "Delete", err)
	}
	records, exists := c.store.collections[c.name]
	if !exists {
		return failure(c.name, "Delete", missingCollection(c.name))
	}
	delete(records, key)
	return nil
}

// Upsert retains records without cloning them, generating embeddings once per
// generated property for the entire batch. Collection dictionary updates are
// committed atomically after preparation succeeds. Duplicate keys use the last
// record. Changes to input records are not rolled back on failure.
//
// Empty batches are no-ops. Nonempty batches require an existing collection.
// After embedding generation, auto-generated UUIDs replace uuid.Nil in records.
// Use pointer or map records to observe generated keys on the input; plain struct
// values are copied by value.
func (c *Collection[K, R]) Upsert(ctx context.Context, records ...R) error {
	if err := ctx.Err(); err != nil {
		return failure(c.name, "Upsert", err)
	}
	if len(records) == 0 {
		return nil
	}
	batch := make([]*storedRecord[R], len(records))
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return failure(c.name, "Upsert", err)
		}
		if err := c.schema.validateRecord(reflect.ValueOf(record)); err != nil {
			return failure(c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
		batch[i] = &storedRecord[R]{Record: record}
	}
	for _, field := range c.schema.vectors {
		if field.vector.Type == field.vector.EmbeddingType {
			continue
		}
		result, err := providerservices.GenerateEmbeddings(ctx, field.vector, records)
		if err != nil {
			return failure(c.name, "Upsert", err)
		}
		for i, output := range result.Embeddings {
			vector, ok := nativeVector(output)
			if !ok || vector == nil {
				return failure(c.name, "Upsert", fmt.Errorf("property %q: generator returned an invalid embedding at index %d", field.name, i))
			}
			if err := validateVector(vector, field.vector.Dimensions); err != nil {
				return failure(c.name, "Upsert", fmt.Errorf("property %q: %w", field.name, err))
			}
			if batch[i].Vectors == nil {
				batch[i].Vectors = make(map[string][]float32)
			}
			batch[i].Vectors[field.name] = slices.Clone(vector)
		}
	}
	exists, err := c.Exists(ctx)
	if err != nil {
		return failure(c.name, "Upsert", err)
	}
	if !exists {
		return failure(c.name, "Upsert", missingCollection(c.name))
	}
	updates := make(map[K]*storedRecord[R], len(batch))
	for i, record := range batch {
		if err := ctx.Err(); err != nil {
			return failure(c.name, "Upsert", err)
		}
		value := reflect.ValueOf(&record.Record).Elem()
		if c.schema.keyAutoGenerated {
			key, _, err := c.schema.key.read(value)
			if err != nil {
				return failure(c.name, "Upsert", err)
			}
			if !key.IsValid() || key.Interface() == uuid.Nil {
				id, err := uuid.NewRandom()
				if err != nil {
					return failure(c.name, "Upsert", err)
				}
				target := value
				if target.Kind() == reflect.Struct {
					target = target.Addr()
				}
				if err := c.schema.key.property.SetValue(target.Interface(), id); err != nil {
					return failure(c.name, "Upsert", err)
				}
			}
		}
		key, err := recordKey[K](c.schema.key, value)
		if err != nil {
			return failure(c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
		updates[key] = record
	}
	return c.commit(ctx, updates, false, "Upsert")
}

func (c *Collection[K, R]) commit(ctx context.Context, updates map[K]*storedRecord[R], create bool, operation string) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return failure(c.name, operation, err)
	}
	records, exists := c.store.collections[c.name]
	if !exists {
		if !create {
			return failure(c.name, operation, missingCollection(c.name))
		}
		if c.store.collections == nil {
			c.store.collections = make(map[string]map[K]*storedRecord[R])
		}
		records = make(map[K]*storedRecord[R])
		c.store.collections[c.name] = records
	}
	maps.Copy(records, updates)
	return nil
}

func (c *Collection[K, R]) snapshot(ctx context.Context) ([]*storedRecord[R], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.store.mu.RLock()
	defer c.store.mu.RUnlock()
	records, exists := c.store.collections[c.name]
	if !exists {
		return nil, missingCollection(c.name)
	}
	result := make([]*storedRecord[R], 0, len(records))
	for _, record := range records {
		result = append(result, record)
	}
	return result, nil
}

func (c *Collection[K, R]) validateRetrieval(includeVectors bool) error {
	if includeVectors && c.model.EmbeddingGenerationRequired {
		return fmt.Errorf("IncludeVectors is not supported when the collection has generated vector fields")
	}
	return nil
}

var _ vectordata.Collection[string, any] = (*Collection[string, any])(nil)
