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
// with [NewCollection]. Its schema is fixed at construction. A shared [Store]
// also creates collections through its embedded [vectordata.Store].
type Collection[K comparable, R any] struct {
	store      *store
	name       string
	model      *providerservices.CollectionModel
	schema     recordSchema
	keyType    reflect.Type
	recordType reflect.Type
}

// NewCollection creates a handle backed by its own independent store.
// A nil definition uses the record's vectordata tags.
// The collection does not exist until [Collection.EnsureExists] is called.
func NewCollection[K comparable, R any](name string, definition *vectordata.Definition) (*Collection[K, R], error) {
	return newCollection[K, R](new(store), name, reflect.TypeFor[K](), reflect.TypeFor[R](), definition)
}

func newCollection[K comparable, R any](s *store, name string, keyType, recordType reflect.Type, definition *vectordata.Definition) (*Collection[K, R], error) {
	if strings.TrimSpace(name) == "" {
		return nil, failure(name, "Collection", fmt.Errorf("collection name is required"))
	}
	s.mu.RLock()
	data := s.collections[name]
	s.mu.RUnlock()
	if data != nil && data.recordType != recordType {
		return nil, failure(name, "Collection", incompatibleCollection(name, data.recordType, recordType))
	}
	model, err := providerservices.Build(keyType, recordType, definition, providerservices.Config{
		SupportsMultipleVectors: true,
		SupportsKeyType:         reflect.Type.Comparable,
		SupportsDataType:        func(reflect.Type) bool { return true },
		SupportsVectorType:      providerservices.SupportsVector[float32],
		SupportsKeyAutoGeneration: func(typ reflect.Type) bool {
			return typ == reflect.TypeFor[uuid.UUID]()
		},
		DefaultEmbeddingGenerator: s.generator,
	})
	if err != nil {
		return nil, failure(name, "Collection", err)
	}
	schema := newRecordSchema(recordType, model)
	for _, field := range schema.vectors {
		if field.vector.Type != field.vector.EmbeddingType && field.vector.EmbeddingGenerator == nil {
			return nil, failure(name, "Collection", fmt.Errorf("property %q requires an embedding generator; in-memory storage cannot generate embeddings itself", field.name))
		}
	}
	return &Collection[K, R]{store: s, name: name, model: model, schema: schema, keyType: keyType, recordType: recordType}, nil
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
		c.store.collections = make(map[string]*collectionData)
	}
	if data, exists := c.store.collections[c.name]; exists {
		if data.recordType != c.recordType {
			return failure(c.name, "EnsureExists", incompatibleCollection(c.name, data.recordType, c.recordType))
		}
	} else {
		c.store.collections[c.name] = &collectionData{recordType: c.recordType, records: make(map[any]*storedRecord)}
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
	data, exists := c.store.collections[c.name]
	var record *storedRecord
	if exists && data.recordType == c.recordType {
		record = data.records[key]
	}
	c.store.mu.RUnlock()
	if !exists {
		return zero, false, failure(c.name, "Get", missingCollection(c.name))
	}
	if data.recordType != c.recordType {
		return zero, false, failure(c.name, "Get", incompatibleCollection(c.name, data.recordType, c.recordType))
	}
	if record == nil {
		return zero, false, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, false, failure(c.name, "Get", err)
	}
	result, err := collectionRecord[R](record.Record)
	if err != nil {
		return zero, false, failure(c.name, "Get", err)
	}
	return result, true, nil
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
	data, exists := c.store.collections[c.name]
	if !exists {
		return failure(c.name, "Delete", missingCollection(c.name))
	}
	if data.recordType != c.recordType {
		return failure(c.name, "Delete", incompatibleCollection(c.name, data.recordType, c.recordType))
	}
	delete(data.records, key)
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
	batch := make([]*storedRecord, len(records))
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return failure(c.name, "Upsert", err)
		}
		value := reflect.ValueOf(record)
		if !value.IsValid() || !value.Type().AssignableTo(c.recordType) {
			return failure(c.name, "Upsert", fmt.Errorf("record %d has type %T, want %v", i, record, c.recordType))
		}
		if value.Type() != c.recordType {
			typed := reflect.New(c.recordType).Elem()
			typed.Set(value)
			value = typed
		}
		if err := c.schema.validateRecord(value); err != nil {
			return failure(c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
		batch[i] = &storedRecord{Record: value.Interface()}
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
			vector, ok := providerservices.AsVector[float32](output)
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
	updates := make(map[any]*storedRecord, len(batch))
	for i, record := range batch {
		if err := ctx.Err(); err != nil {
			return failure(c.name, "Upsert", err)
		}
		value := reflect.ValueOf(record.Record)
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
					target = reflect.New(value.Type())
					target.Elem().Set(value)
				}
				if err := c.schema.key.property.SetValue(target.Interface(), id); err != nil {
					return failure(c.name, "Upsert", err)
				}
				if value.Kind() == reflect.Struct {
					value = target.Elem()
					record.Record = value.Interface()
				}
			}
		}
		key, err := recordKey(c.schema.key, value, c.keyType)
		if err != nil {
			return failure(c.name, "Upsert", fmt.Errorf("record %d: %w", i, err))
		}
		updates[key] = record
	}
	return c.commit(ctx, updates, false, "Upsert")
}

func (c *Collection[K, R]) commit(ctx context.Context, updates map[any]*storedRecord, create bool, operation string) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return failure(c.name, operation, err)
	}
	data, exists := c.store.collections[c.name]
	if !exists {
		if !create {
			return failure(c.name, operation, missingCollection(c.name))
		}
		if c.store.collections == nil {
			c.store.collections = make(map[string]*collectionData)
		}
		data = &collectionData{recordType: c.recordType, records: make(map[any]*storedRecord)}
		c.store.collections[c.name] = data
	} else if data.recordType != c.recordType {
		return failure(c.name, operation, incompatibleCollection(c.name, data.recordType, c.recordType))
	}
	maps.Copy(data.records, updates)
	return nil
}

func (c *Collection[K, R]) snapshot(ctx context.Context) ([]*storedRecord, error) {
	return c.store.snapshot(ctx, c.name, c.recordType)
}

func (c *Collection[K, R]) validateRetrieval(includeVectors bool) error {
	if includeVectors && c.model.EmbeddingGenerationRequired {
		return fmt.Errorf("IncludeVectors is not supported when the collection has generated vector fields")
	}
	return nil
}

var _ vectordata.Collection[string, any] = (*Collection[string, any])(nil)
