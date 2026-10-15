// Copyright (c) Microsoft. All rights reserved.

package inmemory_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
)

type document struct {
	ID     string    `vectordata:"key"`
	Text   string    `vectordata:"data"`
	Vector []float32 `vectordata:"vector,dimensions=4"`
}

var allRecords = vectordata.Filter{Operator: vectordata.NotEqual, Property: "ID", Value: nil}

func createCollection[K comparable, R any](t *testing.T, options vectordata.CollectionOptions) *inmemory.Collection[K, R] {
	t.Helper()
	collection, err := inmemory.NewCollection[K, R]("records", options)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	return collection
}

func collect[T any](t *testing.T, sequence iter.Seq2[T, error]) []T {
	t.Helper()
	var result []T
	for value, err := range sequence {
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	return result
}

func sequenceError[T any](t *testing.T, sequence iter.Seq2[T, error]) error {
	t.Helper()
	var failure error
	count := 0
	for _, err := range sequence {
		count++
		failure = err
		if err == nil {
			t.Fatal("expected an error, got a result")
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one error, got %d", count)
	}
	return failure
}

func TestStore_LifecycleAndIsolation(t *testing.T) {
	var store inmemory.Store[string, document]
	ctx := t.Context()
	for _, name := range []string{"", " \t\n"} {
		if _, err := store.Collection(name, vectordata.CollectionOptions{}); err == nil {
			t.Fatalf("accepted collection name %q", name)
		}
	}
	first, err := store.Collection("first", vectordata.CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	same, err := store.Collection("first", vectordata.CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Name() != "first" {
		t.Fatalf("name = %q", first.Name())
	}
	if exists, err := first.Exists(ctx); err != nil || exists {
		t.Fatalf("uncreated collection exists = %v, err = %v", exists, err)
	}
	if names := collect(t, store.ListCollectionNames(ctx)); len(names) != 0 {
		t.Fatalf("uncreated collections were listed: %v", names)
	}
	if err := first.EnsureDeleted(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := first.EnsureExists(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Upsert(ctx, document{ID: "id", Text: "original"}); err != nil {
		t.Fatal(err)
	}
	if record, found, err := same.Get(ctx, "id", nil); err != nil || !found || record.Text != "original" {
		t.Fatalf("shared handle Get = %v, %v, %v", record, found, err)
	}
	other := inmemory.New[string, document](inmemory.Config{})
	if exists, err := other.CollectionExists(ctx, "first"); err != nil || exists {
		t.Fatalf("independent store shares data: %v, %v", exists, err)
	}
	for name, err := range store.ListCollectionNames(ctx) {
		if err != nil || name != "first" {
			t.Fatalf("name = %q, err = %v", name, err)
		}
		if err := store.EnsureCollectionDeleted(ctx, name); err != nil {
			t.Fatal(err)
		}
		break
	}
	if exists, err := same.Exists(ctx); err != nil || exists {
		t.Fatalf("deleted collection exists = %v, err = %v", exists, err)
	}
	if err := same.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := first.Get(ctx, "id", nil); err != nil || found {
		t.Fatalf("old handle retained deleted records: found = %v, err = %v", found, err)
	}
	if _, hybrid := first.(vectordata.KeywordHybridSearcher[document]); hybrid {
		t.Fatal("in-memory collection advertises unsupported hybrid search")
	}
}

func TestCollection_CRUD(t *testing.T) {
	c := createCollection[string, document](t, vectordata.CollectionOptions{})
	ctx := t.Context()
	input := document{ID: "first", Text: "initial", Vector: []float32{1, 2, 3, 4}}
	if err := c.Upsert(ctx, input); err != nil {
		t.Fatal(err)
	}
	got, found, err := c.Get(ctx, "first", nil)
	if err != nil || !found || !reflect.DeepEqual(got, input) {
		t.Fatalf("Get = %+v, %v, %v", got, found, err)
	}
	include := &vectordata.RecordRetrievalOptions{IncludeVectors: true}
	got, found, err = c.Get(ctx, "first", include)
	if err != nil || !found || !reflect.DeepEqual(got, input) {
		t.Fatalf("Get with vectors = %+v, %v, %v", got, found, err)
	}
	input.Vector[0], got.Vector[1] = 100, 200
	stored, _, err := c.Get(ctx, "first", include)
	if err != nil || !slices.Equal(stored.Vector, []float32{100, 200, 3, 4}) {
		t.Fatalf("stored vector references were not retained: %v, %v", stored.Vector, err)
	}
	if _, found, err := c.Get(ctx, "missing", nil); err != nil || found {
		t.Fatalf("missing Get: %v, %v", found, err)
	}
	if err := c.Delete(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(ctx, document{ID: "first", Text: "replaced"}, document{ID: "second"}, document{ID: "first", Text: "last"}); err != nil {
		t.Fatal(err)
	}
	batch := collect(t, vectordata.GetBatch(ctx, c, []string{"missing", "first", "second"}, nil))
	if len(batch) != 2 || batch[0].Text != "last" || batch[0].Vector != nil || batch[1].ID != "second" {
		t.Fatalf("batch = %+v", batch)
	}
	if err := vectordata.DeleteBatch(ctx, c, []string{"missing", "first", "second"}); err != nil {
		t.Fatal(err)
	}
	if records := collect(t, c.GetFiltered(ctx, allRecords, 10, nil)); len(records) != 0 {
		t.Fatalf("records remain after deletion: %v", records)
	}
}

func TestCollection_MissingAndInvalidRecords(t *testing.T) {
	c, err := inmemory.NewCollection[string, document]("missing", vectordata.CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := c.Upsert(ctx); err != nil {
		t.Fatalf("empty upsert: %v", err)
	}
	_, _, getErr := c.Get(ctx, "id", nil)
	for name, err := range map[string]error{
		"Get":         getErr,
		"Delete":      c.Delete(ctx, "id"),
		"Upsert":      c.Upsert(ctx, document{ID: "id"}),
		"Search":      sequenceError(t, c.Search(ctx, []float32{1, 0, 0, 0}, 1, nil)),
		"GetFiltered": sequenceError(t, c.GetFiltered(ctx, allRecords, 1, nil)),
	} {
		var storeErr *vectordata.Error
		if !errors.As(err, &storeErr) || storeErr.SystemName != "inmemory" ||
			storeErr.CollectionName != "missing" || storeErr.OperationName != name || storeErr.Err == nil {
			t.Fatalf("%s error = %+v", name, err)
		}
	}
	if err := c.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(ctx, document{ID: "valid"}, document{ID: "invalid", Vector: []float32{1}}); err == nil {
		t.Fatal("accepted wrong vector dimensions")
	}
	if _, found, err := c.Get(ctx, "valid", nil); err != nil || found {
		t.Fatalf("invalid batch partially committed: %v, %v", found, err)
	}
	pointers := createCollection[string, *document](t, vectordata.CollectionOptions{})
	if err := pointers.Upsert(ctx, nil); err == nil {
		t.Fatal("accepted nil record")
	}
}

func TestCollection_RetainsRecordReferences(t *testing.T) {
	type node struct {
		Values []int
		Next   *node
	}
	type record struct {
		ID       string `vectordata:"key"`
		Root     *node
		Alias    *node
		Metadata map[string]any
		Array    [1][]string
		When     time.Time
		private  int
	}
	root := &node{Values: []int{1, 2}}
	root.Next = root
	metadata := map[string]any{"nested": []string{"original"}}
	metadata["self"] = metadata
	input := &record{
		ID: "id", Root: root, Alias: root, Metadata: metadata,
		Array: [1][]string{{"original"}}, When: time.Now(), private: 42,
	}
	c := createCollection[string, *record](t, vectordata.CollectionOptions{})
	if err := c.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	root.Values[0] = 99
	input.Array[0][0] = "changed"
	input.Metadata["nested"].([]string)[0] = "changed"
	got, found, err := c.Get(t.Context(), "id", nil)
	if err != nil || !found || got != input {
		t.Fatalf("stored record reference changed: %p, want %p; found = %v, err = %v", got, input, found, err)
	}
	if got.Root != root || got.Root.Next != root || got.Alias != root || got.Root.Values[0] != 99 ||
		got.Array[0][0] != "changed" || got.Metadata["nested"].([]string)[0] != "changed" ||
		!got.When.Equal(input.When) || got.private != 42 {
		t.Fatalf("record data was not retained: %+v", got)
	}
	got.Metadata["self"].(map[string]any)["through-cycle"] = true
	if input.Metadata["through-cycle"] != true {
		t.Fatal("returned record no longer shares the original map")
	}
}

func TestCollection_AcceptsReferenceAndPrivateState(t *testing.T) {
	type record struct {
		ID       string `vectordata:"key"`
		Callback func() bool
		Channel  chan int
		hidden   *int
	}
	c := createCollection[string, record](t, vectordata.CollectionOptions{})
	called := false
	input := record{
		ID: "id", Channel: make(chan int), hidden: new(42),
		Callback: func() bool { called = true; return true },
	}
	if err := c.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	got, found, err := c.Get(t.Context(), "id", nil)
	if err != nil || !found || got.Callback == nil || !got.Callback() || !called ||
		got.Channel != input.Channel || got.hidden != input.hidden {
		t.Fatalf("reference or private state changed: %+v, %v, %v", got, found, err)
	}
}

func TestCollection_ReturnsStoredRecordsWithVectors(t *testing.T) {
	c := createCollection[string, *document](t, vectordata.CollectionOptions{})
	input := &document{ID: "id", Vector: []float32{1, 0, 0, 0}}
	if err := c.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	for _, include := range []bool{false, true} {
		got, found, err := c.Get(t.Context(), "id", &vectordata.RecordRetrievalOptions{IncludeVectors: include})
		if err != nil || !found || got != input {
			t.Fatalf("Get: %p, want %p; found = %v, err = %v", got, input, found, err)
		}
		search := collect(t, c.Search(t.Context(), input.Vector, 1, &vectordata.SearchOptions{IncludeVectors: include}))
		filtered := collect(t, c.GetFiltered(t.Context(), allRecords, 1, &vectordata.FilteredRecordRetrievalOptions{IncludeVectors: include}))
		batch := collect(t, vectordata.GetBatch(t.Context(), c, []string{"id"}, &vectordata.RecordRetrievalOptions{IncludeVectors: include}))
		if len(search) != 1 || search[0].Record != input ||
			len(filtered) != 1 || filtered[0] != input || len(batch) != 1 || batch[0] != input ||
			!slices.Equal(input.Vector, []float32{1, 0, 0, 0}) {
			t.Fatalf("retrieval copied or changed the record with IncludeVectors=%v", include)
		}
	}
}

func dynamicOptions(keyType reflect.Type) vectordata.CollectionOptions {
	return vectordata.CollectionOptions{Definition: &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: keyType},
		&vectordata.DataProperty{Name: "Text", Type: reflect.TypeFor[string]()},
		&vectordata.VectorProperty{Name: "Vector", Type: reflect.TypeFor[[]float32](), Dimensions: 4},
	}}}
}

func TestCollection_DynamicRecords(t *testing.T) {
	type dynamic map[string]any
	c := createCollection[string, dynamic](t, dynamicOptions(reflect.TypeFor[string]()))
	input := dynamic{"ID": "id", "Text": "hello", "Vector": []float32{1, 2, 3, 4}, "Other": map[string]int{"value": 1}}
	if err := c.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	input["Text"] = "changed"
	input["Other"].(map[string]int)["value"] = 2
	got, found, err := c.Get(t.Context(), "id", nil)
	if err != nil || !found || got["Text"] != "changed" || got["Other"].(map[string]int)["value"] != 2 {
		t.Fatalf("Get = %v, %v, %v", got, found, err)
	}
	if vector, ok := got["Vector"].([]float32); !ok || !slices.Equal(vector, []float32{1, 2, 3, 4}) {
		t.Fatal("native dynamic vector was excluded")
	}
	got["Text"] = "returned"
	if input["Text"] != "returned" {
		t.Fatal("returned dynamic record is not the original map")
	}
	for name, invalid := range map[string]dynamic{
		"nil": nil, "missing-key": {"Text": "hello"}, "nil-key": {"ID": nil},
		"wrong-key": {"ID": 1}, "wrong-data": {"ID": "id", "Text": 1},
		"wrong-vector": {"ID": "id", "Vector": []float64{1, 2, 3, 4}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.Upsert(t.Context(), invalid); err == nil {
				t.Fatal("accepted invalid dynamic record")
			}
		})
	}
	pointers := createCollection[string, *dynamic](t, dynamicOptions(reflect.TypeFor[string]()))
	if err := pointers.Upsert(t.Context(), nil); err == nil {
		t.Fatal("accepted nil pointer to map")
	}
	var nilMap dynamic
	if err := pointers.Upsert(t.Context(), &nilMap); err == nil {
		t.Fatal("accepted pointer to nil map")
	}
	if err := pointers.Upsert(t.Context(), &input); err != nil {
		t.Fatal(err)
	}
}

func TestCollection_UUIDKeys(t *testing.T) {
	type record struct {
		ID   uuid.UUID `vectordata:"key"`
		Text string    `vectordata:"data"`
	}
	for _, auto := range []*bool{nil, new(true), new(false)} {
		name := "default"
		if auto != nil {
			name = fmt.Sprint(*auto)
		}
		t.Run(name, func(t *testing.T) {
			options := vectordata.CollectionOptions{Definition: &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", IsAutoGenerated: auto},
			}}}
			c := createCollection[uuid.UUID, record](t, options)
			input := record{Text: "generated"}
			explicit := record{ID: uuid.New(), Text: "explicit"}
			if err := c.Upsert(t.Context(), input, explicit); err != nil {
				t.Fatal(err)
			}
			if input.ID != uuid.Nil {
				t.Fatal("upsert unexpectedly changed a plain struct value")
			}
			got := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Equal, Property: "Text", Value: "generated"}, 1, nil))
			if len(got) != 1 || (got[0].ID != uuid.Nil) != (auto == nil || *auto) {
				t.Fatalf("generated key = %v, auto = %v", got, auto)
			}
			if stored, found, err := c.Get(t.Context(), explicit.ID, nil); err != nil || !found || stored != explicit {
				t.Fatalf("explicit key changed: %+v, %v, %v", stored, found, err)
			}
			pointers := createCollection[uuid.UUID, *record](t, options)
			pointer := &record{Text: "pointer"}
			if err := pointers.Upsert(t.Context(), pointer); err != nil {
				t.Fatal(err)
			}
			if (pointer.ID != uuid.Nil) != (auto == nil || *auto) {
				t.Fatalf("input key = %v, auto = %v", pointer.ID, auto)
			}
			if stored, found, err := pointers.Get(t.Context(), pointer.ID, nil); err != nil || !found || stored != pointer {
				t.Fatalf("generated key does not retrieve the original record: %p, %v, %v", stored, found, err)
			}
			repeated := &record{Text: "repeated"}
			if err := pointers.Upsert(t.Context(), repeated, repeated); err != nil {
				t.Fatal(err)
			}
			duplicates := collect(t, pointers.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Equal, Property: "Text", Value: "repeated"}, 2, nil))
			if len(duplicates) != 1 || duplicates[0] != repeated {
				t.Fatal("repeated reference received multiple keys or was copied")
			}
		})
	}
	dynamic := createCollection[uuid.UUID, map[string]any](t, dynamicOptions(reflect.TypeFor[uuid.UUID]()))
	input := map[string]any{"Text": "missing key"}
	if err := dynamic.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	got := collect(t, dynamic.GetFiltered(t.Context(), allRecords, 1, nil))
	if len(got) != 1 || got[0]["ID"] == uuid.Nil || input["ID"] != got[0]["ID"] {
		t.Fatalf("dynamic key was not generated: %v", got)
	}
	type dynamicRecord map[string]any
	pointers := createCollection[uuid.UUID, *dynamicRecord](t, dynamicOptions(reflect.TypeFor[uuid.UUID]()))
	pointer := dynamicRecord{"Text": "pointer map"}
	if err := pointers.Upsert(t.Context(), &pointer); err != nil {
		t.Fatal(err)
	}
	id, ok := pointer["ID"].(uuid.UUID)
	if !ok || id == uuid.Nil {
		t.Fatalf("input map key was not generated: %v", pointer)
	}
	if stored, found, err := pointers.Get(t.Context(), id, nil); err != nil || !found || stored != &pointer {
		t.Fatalf("generated key does not retrieve the original map pointer: %p, %v, %v", stored, found, err)
	}
}

func TestCollection_KeyTypes(t *testing.T) {
	type record struct {
		ID any `vectordata:"key"`
	}
	c := createCollection[any, record](t, vectordata.CollectionOptions{})
	valid := []any{
		"string", 42, int64(43), uint64(44), true,
		[2]int{1, 2},
		struct{ Part string }{"part"},
		time.Now(), new(1),
		struct{ Part any }{new(1)},
		make(chan int),
	}
	for _, key := range valid {
		if err := c.Upsert(t.Context(), record{ID: key}); err != nil {
			t.Fatalf("key %T: %v", key, err)
		}
		if _, found, err := c.Get(t.Context(), key, nil); err != nil || !found {
			t.Fatalf("key %T: found = %v, err = %v", key, found, err)
		}
	}
	for _, key := range []any{nil, []int{1}, (*int)(nil), (chan int)(nil), struct{ Part any }{[]int{1}}, math.NaN()} {
		if err := c.Upsert(t.Context(), record{ID: key}); err == nil {
			t.Fatalf("accepted key %T", key)
		}
		if _, _, err := c.Get(t.Context(), key, nil); err == nil {
			t.Fatalf("Get accepted key %T", key)
		}
		if err := c.Delete(t.Context(), key); err == nil {
			t.Fatalf("Delete accepted key %T", key)
		}
	}
	type pointerKey struct {
		ID *int `vectordata:"key"`
	}
	pointers := createCollection[*int, pointerKey](t, vectordata.CollectionOptions{})
	key := new(1)
	if err := pointers.Upsert(t.Context(), pointerKey{ID: key}); err != nil {
		t.Fatal(err)
	}
	*key = 2
	if got, found, err := pointers.Get(t.Context(), key, nil); err != nil || !found || got.ID != key {
		t.Fatalf("pointer key identity was not preserved: %+v, %v, %v", got, found, err)
	}
}

func TestCollection_NativeVectorRepresentations(t *testing.T) {
	type record struct {
		ID        int                         `vectordata:"key"`
		Slice     []float32                   `vectordata:"vector,dimensions=2"`
		Vector    embedding.VectorOf[float32] `vectordata:"vector,dimensions=2"`
		Embedding embedding.Of[float32]       `vectordata:"vector,dimensions=2"`
		Pointer   *embedding.Of[float32]      `vectordata:"vector,dimensions=2"`
	}
	c := createCollection[int, record](t, vectordata.CollectionOptions{})
	input := record{
		ID: 1, Slice: []float32{1, 0}, Vector: embedding.VectorOf[float32]{1, 0},
		Embedding: embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 0}},
		Pointer:   &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 0}},
	}
	if err := c.Upsert(t.Context(), input, record{ID: 2}); err != nil {
		t.Fatal(err)
	}
	for _, property := range []string{"Slice", "Vector", "Embedding", "Pointer"} {
		got := collect(t, c.Search(t.Context(), input.Pointer, 10, &vectordata.SearchOptions{VectorProperty: property}))
		if len(got) != 1 || *got[0].Score != 1 || !reflect.DeepEqual(got[0].Record, input) {
			t.Fatalf("%s results = %+v", property, got)
		}
	}
}

func TestCollection_EmbeddedFields(t *testing.T) {
	type KeyFields struct {
		ID uuid.UUID `vectordata:"key"`
	}
	type record struct {
		*KeyFields
		when   time.Time
		fixed  [2]int
		opaque any
	}
	c := createCollection[uuid.UUID, record](t, vectordata.CollectionOptions{})
	input := record{when: time.Now(), fixed: [2]int{1, 2}, opaque: "immutable"}
	if err := c.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	got := collect(t, c.GetFiltered(t.Context(), allRecords, 1, nil))
	if len(got) != 1 || got[0].KeyFields == nil || got[0].ID == uuid.Nil ||
		input.KeyFields != nil || !got[0].when.Equal(input.when) || got[0].fixed != input.fixed || got[0].opaque != input.opaque {
		t.Fatalf("embedded key or immutable fields were not preserved: %+v", got)
	}
	type VectorFields struct {
		Vector []float32 `vectordata:"vector,dimensions=2"`
	}
	type nullable struct {
		ID string `vectordata:"key"`
		*VectorFields
	}
	native := createCollection[string, nullable](t, vectordata.CollectionOptions{})
	if err := native.Upsert(t.Context(), nullable{ID: "nil"}, nullable{ID: "value", VectorFields: &VectorFields{Vector: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	nilRecord, found, err := native.Get(t.Context(), "nil", nil)
	if err != nil || !found || nilRecord.VectorFields != nil {
		t.Fatalf("nil embedded pointer changed: %+v, %v, %v", nilRecord, found, err)
	}
	valueRecord, found, err := native.Get(t.Context(), "value", nil)
	if err != nil || !found || valueRecord.VectorFields == nil || !slices.Equal(valueRecord.Vector, []float32{1, 0}) {
		t.Fatalf("embedded vector was not retained: %+v, %v, %v", valueRecord, found, err)
	}
	type hiddenFields struct {
		ID uuid.UUID `vectordata:"key"`
	}
	type inaccessible struct{ *hiddenFields }
	private := createCollection[uuid.UUID, inaccessible](t, vectordata.CollectionOptions{})
	if err := private.Upsert(t.Context(), inaccessible{}); err == nil {
		t.Fatal("accepted generation through an inaccessible embedded pointer")
	}
}

func TestCollection_ConcurrentCallsAndSnapshotIteration(t *testing.T) {
	c := createCollection[string, document](t, vectordata.CollectionOptions{})
	ctx := t.Context()
	var work sync.WaitGroup
	for i := range 20 {
		work.Go(func() {
			key := fmt.Sprint(i)
			if err := c.Upsert(ctx, document{ID: key, Text: "before", Vector: []float32{1, 0, 0, 0}}); err != nil {
				t.Error(err)
			}
			if _, found, err := c.Get(ctx, key, nil); err != nil || !found {
				t.Errorf("concurrent Get: %v, %v", found, err)
			}
			for range 2 {
				if err := c.EnsureExists(ctx); err != nil {
					t.Error(err)
				}
			}
		})
	}
	work.Wait()
	seen := 0
	for record, err := range c.GetFiltered(ctx, allRecords, 100, nil) {
		if err != nil || record.Text != "before" {
			t.Fatalf("snapshot record = %+v, err = %v", record, err)
		}
		seen++
		if seen == 1 {
			if err := c.EnsureDeleted(ctx); err != nil {
				t.Fatal(err)
			}
			if err := c.EnsureExists(ctx); err != nil {
				t.Fatal(err)
			}
			if err := c.Upsert(ctx, document{ID: "new", Text: "after"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if seen != 20 {
		t.Fatalf("snapshot yielded %d records", seen)
	}
}

func TestCollection_Cancellation(t *testing.T) {
	c := createCollection[string, document](t, vectordata.CollectionOptions{})
	store := inmemory.New[string, document](inmemory.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, getErr := c.Get(ctx, "id", nil)
	_, existsErr := c.Exists(ctx)
	_, storeExistsErr := store.CollectionExists(ctx, "name")
	for _, err := range []error{
		getErr, existsErr, storeExistsErr, c.Upsert(ctx, document{ID: "id"}), c.Upsert(ctx),
		c.Delete(ctx, "id"), c.EnsureExists(ctx), c.EnsureDeleted(ctx), store.EnsureCollectionDeleted(ctx, "name"),
		sequenceError(t, c.Search(ctx, []float32{1, 0, 0, 0}, 1, nil)),
		sequenceError(t, c.GetFiltered(ctx, allRecords, 1, nil)),
		sequenceError(t, store.ListCollectionNames(ctx)),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	}
}
