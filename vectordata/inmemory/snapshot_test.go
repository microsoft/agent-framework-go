// Copyright (c) Microsoft. All rights reserved.

package inmemory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
)

type snapshotStream struct {
	bytes.Buffer
	closed bool
}

func (s *snapshotStream) Close() error {
	s.closed = true
	return nil
}

func TestSnapshot_RoundTripAndMerge(t *testing.T) {
	ctx := t.Context()
	source := inmemory.New(inmemory.Config{})
	c, err := vectordata.GetCollection[string, document](source.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	inputs := []document{
		{ID: "new", Text: "new", Vector: []float32{1, 0, 0, 0}},
		{ID: "existing", Text: "replaced", Vector: []float32{0, 1, 0, 0}},
	}
	if err := c.Upsert(ctx, inputs...); err != nil {
		t.Fatal(err)
	}
	var stream snapshotStream
	if err := source.Export(ctx, "records", &stream); err != nil {
		t.Fatal(err)
	}
	target := inmemory.New(inmemory.Config{})
	existing, err := vectordata.GetCollection[string, document](target.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := existing.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	if err := existing.Upsert(ctx, document{ID: "kept"}, document{ID: "existing", Text: "old"}); err != nil {
		t.Fatal(err)
	}
	imported, err := inmemory.Import[string, document](ctx, target, &stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stream.closed || imported.Name() != "records" {
		t.Fatal("snapshot closed its stream or lost the collection name")
	}
	for _, input := range inputs {
		got, found, err := existing.Get(ctx, input.ID, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
		if err != nil || !found || !reflect.DeepEqual(got, input) {
			t.Fatalf("restored record = %+v, %v, %v", got, found, err)
		}
	}
	if records := collect(t, imported.GetFiltered(ctx, allRecords, 10, nil)); len(records) != 3 {
		t.Fatalf("merge discarded records: %v", records)
	}
	fresh := inmemory.New(inmemory.Config{})
	empty, err := inmemory.Import[string, document](ctx, fresh, strings.NewReader(`{"name":"empty","records":[]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := empty.Exists(ctx); err != nil || !exists {
		t.Fatalf("empty collection was not created: %v, %v", exists, err)
	}
	var output bytes.Buffer
	if err := fresh.Export(ctx, "empty", &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "{\"name\":\"empty\",\"records\":[]}\n" {
		t.Fatalf("empty snapshot = %s", &output)
	}
}

func TestSnapshot_GeneratedVectorsWithoutRegeneration(t *testing.T) {
	calls := 0
	generator := generatorFunc[string, *embedding.Of[float32]](func(ctx context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
		calls++
		return textGenerator().Generate(ctx, values, options)
	})
	config := inmemory.Config{EmbeddingGenerator: embedding.Bind(generator)}
	source := inmemory.New(config)
	c, err := vectordata.GetCollection[string, embeddedDocument](source.Store, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(t.Context(), embeddedDocument{ID: "east", Text: "east"}, embeddedDocument{ID: "north", Text: "north"}); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := source.Export(t.Context(), "generated", &buffer); err != nil {
		t.Fatal(err)
	}
	target := inmemory.New(config)
	imported, err := inmemory.Import[string, embeddedDocument](t.Context(), target, &buffer, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, imported.Search(t.Context(), []float32{1, 0}, 2, nil))
	if len(got) != 2 || got[0].Record.Text != "east" || *got[0].Score != 1 || *got[1].Score != 0 || calls != 1 {
		t.Fatalf("restored generated vectors = %+v, generator calls = %d", got, calls)
	}
	for _, vectors := range []string{`{}`, `{"Text":null}`, `{"Text":[1]}`, `{"Text":[1,0],"Other":[1,0]}`} {
		_, err := inmemory.Import[string, embeddedDocument](t.Context(), target, strings.NewReader(`{"name":"generated","records":[{"record":{"ID":"bad","Text":"east"},"vectors":`+vectors+`}]}`), nil)
		if err == nil {
			t.Fatalf("accepted invalid sidecars %s", vectors)
		}
	}
	if calls != 1 {
		t.Fatal("import invoked the generator")
	}
}

func TestSnapshot_DynamicPropertyTypes(t *testing.T) {
	type dynamic map[string]any
	type pointer *dynamic
	options := dynamicDefinition(reflect.TypeFor[int]())
	source := inmemory.New(inmemory.Config{})
	c, err := vectordata.GetCollection[int, pointer](source.Store, "dynamic", options)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	record := dynamic{"ID": 42, "Text": "hello", "Vector": []float32{1, 0, 0, 0}, "Unmapped": []int{1}}
	nullRecord := dynamic{"ID": 43, "Vector": []float32(nil)}
	if err := c.Upsert(t.Context(), pointer(&record), pointer(&nullRecord)); err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if err := source.Export(t.Context(), "dynamic", &snapshot); err != nil {
		t.Fatal(err)
	}
	target := inmemory.New(inmemory.Config{})
	imported, err := inmemory.Import[int, pointer](t.Context(), target, &snapshot, options)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := imported.Get(t.Context(), 42, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found {
		t.Fatalf("Get: %v, %v", found, err)
	}
	if (*got)["ID"] != 42 || (*got)["Text"] != "hello" ||
		!slices.Equal((*got)["Vector"].([]float32), []float32{1, 0, 0, 0}) ||
		!reflect.DeepEqual((*got)["Unmapped"], []any{float64(1)}) {
		t.Fatalf("decoded property types = %v", *got)
	}
	null, found, err := imported.Get(t.Context(), 43, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || reflect.TypeOf((*null)["Vector"]) != reflect.TypeFor[[]float32]() || (*null)["Vector"].([]float32) != nil {
		t.Fatalf("typed nil vector was not preserved: %v, %v, %v", null, found, err)
	}
	for _, record := range []string{`{"ID":"wrong"}`, `{"ID":null}`, `{"Text":"missing key"}`, `{"ID":1,"Text":42}`} {
		if _, err := inmemory.Import[int, pointer](t.Context(), target, strings.NewReader(`{"name":"dynamic","records":[{"record":`+record+`}]}`), options); err == nil {
			t.Fatalf("accepted invalid dynamic snapshot %s", record)
		}
	}
}

func TestSnapshot_InvalidInputsAreAtomic(t *testing.T) {
	store := inmemory.New(inmemory.Config{})
	c, err := vectordata.GetCollection[string, document](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(t.Context(), document{ID: "kept", Text: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []string{
		``, `null`, `{`, `{}`, `{"name":"records"}`, `{"name":"records","records":null}`,
		`{"name":"","records":[]}`, `{"name":"records","records":{}}`,
		`{"name":"records","records":[null]}`, `{"name":"records","records":[{}]}`,
		`{"name":"records","records":[{"record":null}]}`,
		`{"name":"records","records":[{"record":{"ID":"new","Vector":[1]}}]}`,
		`{"name":"records","records":[{"record":{"ID":"new"},"vectors":{"unused":[1,2]}}]}`,
		`{"name":"records","records":[]} {}`, `{"name":"records","records":[],"unknown":true}`,
		`{"name":"records","records":[{"record":{"ID":"new"}},{"record":{"ID":"kept","Vector":[1]}}]}`,
	} {
		if _, err := inmemory.Import[string, document](t.Context(), store, strings.NewReader(snapshot), nil); err == nil {
			t.Fatalf("accepted malformed snapshot %q", snapshot)
		}
		records := collect(t, c.GetFiltered(t.Context(), allRecords, 10, nil))
		if len(records) != 1 || records[0].ID != "kept" || records[0].Text != "unchanged" {
			t.Fatalf("invalid snapshot changed records: %+v", records)
		}
	}
}

type marshalingRecord struct {
	ID   string `vectordata:"key"`
	Text string `vectordata:"data"`
}

func (r *marshalingRecord) MarshalJSON() ([]byte, error) {
	r.Text = "serialized"
	return json.Marshal(struct{ ID, Text string }{r.ID, r.Text})
}

func TestSnapshot_ExportUsesStoredRecordsAndReportsEncodingErrors(t *testing.T) {
	store := inmemory.New(inmemory.Config{})
	c, err := vectordata.GetCollection[string, *marshalingRecord](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	inputRecord := &marshalingRecord{ID: "id", Text: "original"}
	if err := c.Upsert(t.Context(), inputRecord); err != nil {
		t.Fatal(err)
	}
	if err := store.Export(t.Context(), "records", io.Discard); err != nil {
		t.Fatal(err)
	}
	got, found, err := c.Get(t.Context(), "id", nil)
	if err != nil || !found || got != inputRecord || got.Text != "serialized" {
		t.Fatalf("custom marshaler did not receive the stored record: %+v, %v, %v", got, found, err)
	}
	type cyclicRecord struct {
		ID   string `vectordata:"key"`
		Self *cyclicRecord
	}
	cyclicStore := inmemory.New(inmemory.Config{})
	cyclic, err := vectordata.GetCollection[string, *cyclicRecord](cyclicStore.Store, "cycles", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cyclic.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	input := &cyclicRecord{ID: "id"}
	input.Self = input
	if err := cyclic.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	var encodingError *json.UnsupportedValueError
	if err := cyclicStore.Export(t.Context(), "cycles", io.Discard); !errors.As(err, &encodingError) {
		t.Fatalf("expected encoding error for cycle, got %v", err)
	}
}

type failingIO struct{ err error }

func (f failingIO) Read([]byte) (int, error)  { return 0, f.err }
func (f failingIO) Write([]byte) (int, error) { return 0, f.err }

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(buffer []byte) (int, error) { return f(buffer) }

func TestSnapshot_IOAndCancellation(t *testing.T) {
	store := inmemory.New(inmemory.Config{})
	c, err := vectordata.GetCollection[string, document](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Export(t.Context(), "missing", io.Discard); err == nil {
		t.Fatal("exported a missing collection")
	}
	if err := store.Export(t.Context(), "records", nil); err == nil {
		t.Fatal("accepted nil writer")
	}
	if _, err := inmemory.Import[string, document](t.Context(), store, nil, nil); err == nil {
		t.Fatal("accepted nil reader")
	}
	want := errors.New("IO failed")
	if err := store.Export(t.Context(), "records", failingIO{want}); !errors.Is(err, want) {
		t.Fatalf("export IO error = %v", err)
	}
	if _, err := inmemory.Import[string, document](t.Context(), store, failingIO{want}, nil); !errors.Is(err, want) {
		t.Fatalf("import IO error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Export(ctx, "records", io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("export cancellation = %v", err)
	}
	if _, err := inmemory.Import[string, document](ctx, store, failingIO{want}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("import cancellation = %v", err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	data := strings.NewReader(`{"name":"canceled","records":[{"record":{"ID":"id"}}]}`)
	reads := 0
	reader := readerFunc(func(buffer []byte) (int, error) {
		reads++
		cancel()
		return data.Read(buffer)
	})
	if _, err := inmemory.Import[string, document](ctx, store, reader, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation while reading = %v", err)
	}
	if reads != 1 {
		t.Fatalf("import continued reading after cancellation: %d reads", reads)
	}
	if exists, err := store.CollectionExists(t.Context(), "canceled"); err != nil || exists {
		t.Fatalf("canceled import created a collection: %v, %v", exists, err)
	}
}
