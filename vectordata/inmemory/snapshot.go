// Copyright (c) Microsoft. All rights reserved.

package inmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/internal/recordvalue"
)

// Export writes a JSON snapshot of the named collection, including native and
// generated vectors. The snapshot contains a name and an array of records, not
// a schema or generator configuration. Records must support encoding/json.
// It does not close the writer. Record order in the snapshot is unspecified.
func (s *Store) Export(ctx context.Context, name string, writer io.Writer) error {
	if writer == nil {
		return failure(name, "Export", fmt.Errorf("writer is required"))
	}
	records, err := s.provider.snapshot(ctx, name, nil)
	if err != nil {
		return failure(name, "Export", err)
	}
	snapshot := struct {
		Name    string          `json:"name"`
		Records []*storedRecord `json:"records"`
	}{name, records}
	if err := json.NewEncoder(contextWriter{ctx, writer}).Encode(snapshot); err != nil {
		return failure(name, "Export", err)
	}
	if err := ctx.Err(); err != nil {
		return failure(name, "Export", err)
	}
	return nil
}

// Import reads a JSON snapshot, creates its named collection if necessary, and
// merges records, replacing matching keys. A nil definition uses the record's
// vectordata tags; otherwise the supplied definition configures the collection.
// Generated vectors are restored without invoking a generator. Invalid snapshots
// do not modify the store. The reader is not closed.
//
// Dynamic properties are decoded using their schema types. Other interface
// values follow encoding/json's usual decoding rules.
func Import[K comparable, R any](ctx context.Context, s *Store, reader io.Reader, definition *vectordata.Definition) (vectordata.Collection[K, R], error) {
	if err := ctx.Err(); err != nil {
		return nil, failure("", "Import", err)
	}
	if reader == nil {
		return nil, failure("", "Import", fmt.Errorf("reader is required"))
	}
	if s == nil || s.provider == nil {
		return nil, failure("", "Import", fmt.Errorf("store is required"))
	}
	var snapshot struct {
		Name    string `json:"name"`
		Records []struct {
			Record  json.RawMessage      `json:"record"`
			Vectors map[string][]float32 `json:"vectors"`
		} `json:"records"`
	}
	decoder := json.NewDecoder(contextReader{ctx, reader})
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, failure("", "Import", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("snapshot must contain exactly one JSON value")
		}
		return nil, failure(snapshot.Name, "Import", err)
	}
	if snapshot.Records == nil {
		return nil, failure(snapshot.Name, "Import", fmt.Errorf("snapshot records must be an array"))
	}
	collection, err := newCollection[K, R](s.provider, snapshot.Name, reflect.TypeFor[K](), reflect.TypeFor[R](), definition)
	if err != nil {
		return nil, err
	}
	updates := make(map[any]*storedRecord, len(snapshot.Records))
	for i, source := range snapshot.Records {
		if err := ctx.Err(); err != nil {
			return nil, failure(snapshot.Name, "Import", err)
		}
		record, err := decodeRecord[R](collection.schema, source.Record)
		if err != nil {
			return nil, failure(snapshot.Name, "Import", fmt.Errorf("record %d: %w", i, err))
		}
		value := reflect.ValueOf(record)
		if err := collection.schema.validateRecord(value); err != nil {
			return nil, failure(snapshot.Name, "Import", err)
		}
		key, err := recordKey(collection.schema.key, value, collection.keyType)
		if err != nil {
			return nil, failure(snapshot.Name, "Import", err)
		}
		vectors := make(map[string][]float32)
		for _, field := range collection.schema.vectors {
			if field.vector.Type == field.vector.EmbeddingType {
				continue
			}
			vector, found := source.Vectors[field.name]
			if !found {
				return nil, failure(snapshot.Name, "Import", fmt.Errorf("record %d: missing generated vector %q", i, field.name))
			}
			if err := validateVector(vector, field.vector.Dimensions); err != nil {
				return nil, failure(snapshot.Name, "Import", fmt.Errorf("property %q: %w", field.name, err))
			}
			vectors[field.name] = slices.Clone(vector)
		}
		if len(vectors) != len(source.Vectors) {
			return nil, failure(snapshot.Name, "Import", fmt.Errorf("record %d contains unknown generated vectors", i))
		}
		updates[key] = &storedRecord{Record: record, Vectors: vectors}
	}
	if err := collection.commit(ctx, updates, true, "Import"); err != nil {
		return nil, err
	}
	return collection, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(buffer []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(buffer)
}

func decodeRecord[R any](schema recordSchema, data []byte) (R, error) {
	var record R
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return record, fmt.Errorf("record is required and must not be null")
	}
	if schema.typ.Kind() != reflect.Map {
		err := json.Unmarshal(data, &record)
		return record, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return record, err
	}
	if fields == nil {
		return record, fmt.Errorf("record must not be null")
	}
	value := reflect.MakeMapWithSize(schema.typ, len(fields))
	for name, raw := range fields {
		typ := reflect.TypeFor[any]()
		if field, known := schema.fields[name]; known {
			typ = field.typ
		}
		target := reflect.New(typ)
		if err := json.Unmarshal(raw, target.Interface()); err != nil {
			return record, fmt.Errorf("property %q: %w", name, err)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && !recordvalue.IsNil(target.Elem()) {
			value.SetMapIndex(reflect.ValueOf(name), reflect.Zero(reflect.TypeFor[any]()))
		} else {
			value.SetMapIndex(reflect.ValueOf(name), target.Elem())
		}
	}
	if reflect.TypeFor[R]().Kind() == reflect.Pointer {
		pointer := reflect.New(schema.typ)
		pointer.Elem().Set(value)
		value = reflect.New(reflect.TypeFor[R]()).Elem()
		value.Set(pointer)
	}
	return value.Interface().(R), nil
}
