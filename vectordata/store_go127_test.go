// Copyright (c) Microsoft. All rights reserved.

//go:build go1.27

package vectordata_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
)

func TestStore_GenericMethod(t *testing.T) {
	type document struct {
		ID string `vectordata:"key"`
	}
	type counter struct {
		ID int `vectordata:"key"`
	}
	store := inmemory.New(inmemory.Config{})
	documents, err := store.Collection[string, document]("documents", nil)
	if err != nil {
		t.Fatal(err)
	}
	facade := store.Store
	counters, err := facade.Collection[int, *counter]("counters", nil)
	if err != nil {
		t.Fatal(err)
	}
	dynamic, err := store.DynamicCollection("dynamic", &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, collection := range []interface{ EnsureExists(context.Context) error }{documents, counters, dynamic} {
		if err := collection.EnsureExists(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := documents.Upsert(t.Context(), document{ID: "one"}); err != nil {
		t.Fatal(err)
	}
	value := &counter{ID: 2}
	if err := counters.Upsert(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	if err := dynamic.Upsert(t.Context(), map[string]any{"ID": "three"}); err != nil {
		t.Fatal(err)
	}
	if got, found, err := counters.Get(t.Context(), 2, nil); err != nil || !found || got != value {
		t.Fatalf("generic method lost record identity: (%v, %v, %v)", got, found, err)
	}
	fallback, err := vectordata.GetCollection[string, document](store.Store, "documents", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, found, err := fallback.Get(t.Context(), "one", nil); err != nil || !found || got.ID != "one" {
		t.Fatalf("method and function do not share storage: (%v, %v, %v)", got, found, err)
	}
}
