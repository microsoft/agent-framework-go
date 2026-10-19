// Copyright (c) Microsoft. All rights reserved.

package vectordata_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/vectordata"
)

type storeProvider struct {
	vectordata.StoreProvider
	open func(string, reflect.Type, reflect.Type, *vectordata.Definition) (vectordata.Collection[any, any], error)
}

func (p *storeProvider) OpenCollection(name string, key, record reflect.Type, definition *vectordata.Definition) (vectordata.Collection[any, any], error) {
	return p.open(name, key, record, definition)
}

type rawCollection struct {
	vectordata.Collection[any, any]
	get      func(context.Context, any, *vectordata.RecordRetrievalOptions) (any, bool, error)
	upsert   func(context.Context, ...any) error
	delete   func(context.Context, any) error
	filtered func(context.Context, vectordata.Filter, int, *vectordata.FilteredRecordRetrievalOptions) iter.Seq2[any, error]
	search   func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[any], error]
}

func (c *rawCollection) Get(ctx context.Context, key any, options *vectordata.RecordRetrievalOptions) (any, bool, error) {
	return c.get(ctx, key, options)
}

func (c *rawCollection) Upsert(ctx context.Context, records ...any) error {
	return c.upsert(ctx, records...)
}

func (c *rawCollection) Delete(ctx context.Context, key any) error { return c.delete(ctx, key) }

func (c *rawCollection) GetFiltered(ctx context.Context, filter vectordata.Filter, top int, options *vectordata.FilteredRecordRetrievalOptions) iter.Seq2[any, error] {
	return c.filtered(ctx, filter, top, options)
}

func (c *rawCollection) Search(ctx context.Context, query any, top int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
	return c.search(ctx, query, top, options)
}

func collectionStore(raw vectordata.Collection[any, any]) *vectordata.Store {
	return vectordata.NewStore(&storeProvider{open: func(string, reflect.Type, reflect.Type, *vectordata.Definition) (vectordata.Collection[any, any], error) {
		return raw, nil
	}})
}

func TestStore_FactoryTypesAndErrors(t *testing.T) {
	definition := &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
	}}
	var gotKey, gotRecord reflect.Type
	calls := 0
	wantErr := errors.New("open failed")
	var failure error
	var raw vectordata.Collection[any, any] = &rawCollection{}
	store := vectordata.NewStore(&storeProvider{open: func(name string, key, record reflect.Type, got *vectordata.Definition) (vectordata.Collection[any, any], error) {
		calls++
		if name != "records" || got != definition {
			t.Fatal("factory changed name or definition")
		}
		gotKey, gotRecord = key, record
		return raw, failure
	}})
	if _, err := vectordata.GetCollection[string, *record](store, "records", definition); err != nil ||
		gotKey != reflect.TypeFor[string]() || gotRecord != reflect.TypeFor[*record]() {
		t.Fatalf("typed factory types = (%v, %v), error = %v", gotKey, gotRecord, err)
	}
	if _, err := store.DynamicCollection("records", definition); err != nil ||
		gotKey != reflect.TypeFor[any]() || gotRecord != reflect.TypeFor[map[string]any]() {
		t.Fatalf("dynamic factory types = (%v, %v), error = %v", gotKey, gotRecord, err)
	}
	if _, err := store.DynamicCollection("records", nil); err == nil || calls != 2 {
		t.Fatalf("dynamic collection without schema: calls=%d, error=%v", calls, err)
	}
	definition = nil
	if _, err := vectordata.GetCollection[string, *record](store, "records", nil); err != nil || calls != 3 {
		t.Fatalf("typed collection without explicit schema: calls=%d, error=%v", calls, err)
	}
	failure = wantErr
	if _, err := vectordata.GetCollection[string, *record](store, "records", definition); err != wantErr {
		t.Fatalf("provider failure = %v", err)
	}
	failure, raw = nil, nil
	if _, err := vectordata.GetCollection[string, *record](store, "records", definition); err == nil {
		t.Fatal("provider's nil collection was accepted")
	}
	for _, uninitialized := range []*vectordata.Store{nil, {}} {
		if _, err := vectordata.GetCollection[string, record](uninitialized, "records", definition); err == nil {
			t.Fatal("uninitialized store was accepted")
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("nil provider must panic")
		}
	}()
	vectordata.NewStore(nil)
}

func TestStore_TypedCRUD(t *testing.T) {
	ctx := t.Context()
	wantErr := errors.New("provider error")
	input := &record{ID: "one", Text: "text"}
	getOptions := &vectordata.RecordRetrievalOptions{IncludeVectors: true}
	calls := 0
	raw := &rawCollection{
		get: func(got context.Context, key any, options *vectordata.RecordRetrievalOptions) (any, bool, error) {
			if got != ctx || key != "one" || options != getOptions {
				t.Fatal("Get arguments changed")
			}
			return input, true, nil
		},
		upsert: func(got context.Context, values ...any) error {
			if got != ctx {
				t.Fatal("Upsert context changed")
			}
			switch calls {
			case 0:
				if values != nil {
					t.Fatal("nil batch became non-nil")
				}
			case 1:
				if values == nil || len(values) != 0 {
					t.Fatal("empty batch lost its representation")
				}
			case 2:
				if len(values) != 2 || values[0] != input || values[1].(*record) != nil {
					t.Fatalf("upsert values = %v", values)
				}
			}
			calls++
			return wantErr
		},
		delete: func(got context.Context, key any) error {
			if got != ctx || key != "one" {
				t.Fatal("Delete arguments changed")
			}
			return wantErr
		},
	}
	collection, err := vectordata.GetCollection[string, *record](collectionStore(raw), "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := collection.Get(ctx, "one", getOptions)
	if err != nil || !found || got != input {
		t.Fatalf("Get = (%v, %v, %v)", got, found, err)
	}
	for _, records := range [][]*record{nil, {}, {input, nil}} {
		if err := collection.Upsert(ctx, records...); err != wantErr {
			t.Fatalf("Upsert error = %v", err)
		}
	}
	if calls != 3 {
		t.Fatalf("Upsert calls = %d", calls)
	}
	if err := collection.Delete(ctx, "one"); err != wantErr {
		t.Fatalf("Delete error = %v", err)
	}
	for _, tc := range []struct {
		value any
		found bool
		err   error
		valid bool
	}{
		{value: "wrong", found: true},
		{value: "wrong", found: false, valid: true},
		{value: "wrong", found: true, err: wantErr},
		{value: (*record)(nil), found: true, valid: true},
		{value: nil, found: true, valid: true},
	} {
		raw.get = func(context.Context, any, *vectordata.RecordRetrievalOptions) (any, bool, error) {
			return tc.value, tc.found, tc.err
		}
		got, found, err := collection.Get(ctx, "one", nil)
		if got != nil || found != tc.found || tc.valid && err != nil || !tc.valid && err == nil ||
			tc.err != nil && err != tc.err {
			t.Fatalf("Get with %#v = (%v, %v, %v)", tc, got, found, err)
		}
	}
	valueCollection, err := vectordata.GetCollection[string, record](collectionStore(raw), "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := valueCollection.Get(ctx, "one", nil); err == nil {
		t.Fatal("nil provider value was accepted as a struct")
	}
}

func TestStore_TypedIterators(t *testing.T) {
	for _, operation := range []string{"filter", "search", "batch"} {
		for _, outcome := range []string{"success", "bad type", "provider error", "consumer stop"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				wantErr := errors.New("provider failed")
				input := &record{ID: "one"}
				calls, yielded, released := 0, 0, false
				values := func(yield func(any, error) bool) {
					calls++
					defer func() { released = true }()
					if !yield(input, nil) {
						return
					}
					yielded++
					switch outcome {
					case "bad type":
						if yield("wrong", nil) {
							t.Error("continued past a type error")
						}
					case "provider error":
						if yield("wrong", wantErr) {
							t.Error("continued past provider error")
						}
					}
				}
				options := &vectordata.SearchOptions{Skip: 2}
				filterOptions := &vectordata.FilteredRecordRetrievalOptions{Skip: 2}
				filter := vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: "one"}
				score := new(0.75)
				raw := &rawCollection{
					filtered: func(ctx context.Context, got vectordata.Filter, top int, opts *vectordata.FilteredRecordRetrievalOptions) iter.Seq2[any, error] {
						if ctx != t.Context() || !reflect.DeepEqual(got, filter) || top != 3 || opts != filterOptions {
							t.Fatal("filter arguments changed")
						}
						return values
					},
					search: func(ctx context.Context, query any, top int, opts *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
						if ctx != t.Context() || query != "query" || top != 3 || opts != options {
							t.Fatal("search arguments changed")
						}
						return func(yield func(vectordata.SearchResult[any], error) bool) {
							for value, err := range values {
								if !yield(vectordata.SearchResult[any]{Record: value, Score: score}, err) {
									return
								}
							}
						}
					},
				}
				backend := &rawBatchCollection{rawCollection: raw, getBatch: func(context.Context, []any, *vectordata.RecordRetrievalOptions) iter.Seq2[any, error] {
					return values
				}}
				collection, err := vectordata.GetCollection[string, *record](collectionStore(backend), "records", nil)
				if err != nil {
					t.Fatal(err)
				}
				var sequence iter.Seq2[*record, error]
				switch operation {
				case "filter":
					sequence = collection.GetFiltered(t.Context(), filter, 3, filterOptions)
				case "batch":
					sequence = vectordata.GetBatch(t.Context(), collection, []string{"one", "two"}, nil)
				case "search":
					sequence = func(yield func(*record, error) bool) {
						for result, err := range collection.Search(t.Context(), "query", 3, options) {
							if err == nil && result.Score != score {
								t.Error("search score identity changed")
							}
							if !yield(result.Record, err) {
								return
							}
						}
					}
				}
				if calls != 0 {
					t.Fatal("iterator started eagerly")
				}
				count := 0
				var gotErr error
				for value, err := range sequence {
					if err != nil {
						if value != nil {
							t.Error("failed result contains a record")
						}
						gotErr = err
						continue
					}
					count++
					if value != input {
						t.Fatal("record identity changed")
					}
					if outcome == "consumer stop" {
						break
					}
				}
				if count != 1 || calls != 1 || !released || outcome == "consumer stop" && yielded != 0 ||
					outcome == "provider error" && gotErr != wantErr || outcome == "bad type" && gotErr == nil ||
					(outcome == "success" || outcome == "consumer stop") && gotErr != nil {
					t.Fatalf("count=%d calls=%d released=%t yielded=%d error=%v", count, calls, released, yielded, gotErr)
				}
			})
		}
	}
}

type rawBatchCollection struct {
	*rawCollection
	getBatch    func(context.Context, []any, *vectordata.RecordRetrievalOptions) iter.Seq2[any, error]
	deleteBatch func(context.Context, []any) error
}

func (c *rawBatchCollection) GetBatch(ctx context.Context, keys []any, options *vectordata.RecordRetrievalOptions) iter.Seq2[any, error] {
	return c.getBatch(ctx, keys, options)
}

func (c *rawBatchCollection) DeleteBatch(ctx context.Context, keys []any) error {
	return c.deleteBatch(ctx, keys)
}

func TestStore_BatchDispatch(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			var getKeys, deleteKeys []any
			getCalls, deleteCalls := 0, 0
			wantErr := errors.New("delete failed")
			base := &rawCollection{
				get: func(_ context.Context, key any, _ *vectordata.RecordRetrievalOptions) (any, bool, error) {
					getCalls++
					getKeys = append(getKeys, key)
					return &record{ID: key.(string)}, true, nil
				},
				delete: func(_ context.Context, key any) error {
					deleteCalls++
					deleteKeys = append(deleteKeys, key)
					if key == "two" {
						return wantErr
					}
					return nil
				},
			}
			var raw vectordata.Collection[any, any] = base
			if native {
				raw = &rawBatchCollection{
					rawCollection: base,
					getBatch: func(_ context.Context, keys []any, _ *vectordata.RecordRetrievalOptions) iter.Seq2[any, error] {
						getCalls++
						getKeys = keys
						return func(yield func(any, error) bool) {
							for _, key := range keys {
								if !yield(&record{ID: key.(string)}, nil) {
									return
								}
							}
						}
					},
					deleteBatch: func(_ context.Context, keys []any) error {
						deleteCalls++
						deleteKeys = keys
						return wantErr
					},
				}
			}
			collection, err := vectordata.GetCollection[string, *record](collectionStore(raw), "records", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, keys := range [][]string{nil, {}} {
				for _, err := range vectordata.GetBatch(t.Context(), collection, keys, nil) {
					t.Fatalf("empty batch produced a result: %v", err)
				}
				if err := vectordata.DeleteBatch(t.Context(), collection, keys); err != nil {
					t.Fatal(err)
				}
			}
			keys := []string{"one", "two"}
			var got []string
			for record, err := range vectordata.GetBatch(t.Context(), collection, keys, nil) {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, record.ID)
			}
			if err := vectordata.DeleteBatch(t.Context(), collection, keys); err != wantErr {
				t.Fatalf("delete error = %v", err)
			}
			wantCalls := 2
			if native {
				wantCalls = 1
			}
			if !slices.Equal(got, keys) || !reflect.DeepEqual(getKeys, []any{"one", "two"}) ||
				!reflect.DeepEqual(deleteKeys, getKeys) || getCalls != wantCalls || deleteCalls != wantCalls {
				t.Fatalf("get=%v delete=%v calls=(%d,%d)", getKeys, deleteKeys, getCalls, deleteCalls)
			}
		})
	}
}

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

type hybridFunc func(context.Context, any, []string, int, *vectordata.HybridSearchOptions) iter.Seq2[vectordata.SearchResult[any], error]

func (f hybridFunc) HybridSearch(ctx context.Context, value any, keywords []string, top int, options *vectordata.HybridSearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
	return f(ctx, value, keywords, top, options)
}

func TestStore_OptionalCapabilities(t *testing.T) {
	for _, hybrid := range []bool{false, true} {
		for _, closable := range []bool{false, true} {
			t.Run(fmt.Sprintf("hybrid=%t/close=%t", hybrid, closable), func(t *testing.T) {
				base := &rawCollection{}
				input := &record{ID: "one"}
				hybridCalls, closeCalls := 0, 0
				wantErr := errors.New("close failed")
				keywords := []string{"one"}
				options := &vectordata.HybridSearchOptions{Skip: 2}
				search := hybridFunc(func(ctx context.Context, value any, got []string, top int, opts *vectordata.HybridSearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
					hybridCalls++
					if ctx != t.Context() || value != "query" || &got[0] != &keywords[0] || top != 3 || opts != options {
						t.Fatal("hybrid arguments changed")
					}
					return func(yield func(vectordata.SearchResult[any], error) bool) {
						yield(vectordata.SearchResult[any]{Record: input}, nil)
					}
				})
				closeCollection := closeFunc(func() error { closeCalls++; return wantErr })
				var raw vectordata.Collection[any, any] = base
				switch {
				case hybrid && closable:
					raw = &struct {
						*rawCollection
						vectordata.KeywordHybridSearcher[any]
						io.Closer
					}{base, search, closeCollection}
				case hybrid:
					raw = &struct {
						*rawCollection
						vectordata.KeywordHybridSearcher[any]
					}{base, search}
				case closable:
					raw = &struct {
						*rawCollection
						io.Closer
					}{base, closeCollection}
				}
				collection, err := vectordata.GetCollection[string, *record](collectionStore(raw), "records", nil)
				if err != nil {
					t.Fatal(err)
				}
				hybridCollection, hasHybrid := collection.(vectordata.KeywordHybridSearcher[*record])
				closer, hasClose := collection.(io.Closer)
				if hasHybrid != hybrid || hasClose != closable {
					t.Fatalf("capabilities = hybrid:%t close:%t", hasHybrid, hasClose)
				}
				if hybrid {
					results := hybridCollection.HybridSearch(t.Context(), "query", keywords, 3, options)
					if hybridCalls != 0 {
						t.Fatal("hybrid iterator started eagerly")
					}
					for result, err := range results {
						if err != nil || result.Record != input {
							t.Fatalf("hybrid result = (%v, %v)", result, err)
						}
					}
					if hybridCalls != 1 {
						t.Fatalf("hybrid calls = %d", hybridCalls)
					}
				}
				if closable {
					if err := closer.Close(); err != wantErr || closeCalls != 1 {
						t.Fatalf("close = %v, calls = %d", err, closeCalls)
					}
				}
			})
		}
	}
}

func TestStore_CollectionCloseDoesNotCloseStore(t *testing.T) {
	wantErr := errors.New("close failed")
	calls, collectionCalls := 0, 0
	raw := &struct {
		*rawCollection
		io.Closer
	}{&rawCollection{}, closeFunc(func() error { collectionCalls++; return nil })}
	provider := &struct {
		*storeProvider
		io.Closer
	}{&storeProvider{open: func(string, reflect.Type, reflect.Type, *vectordata.Definition) (vectordata.Collection[any, any], error) {
		return raw, nil
	}}, closeFunc(func() error { calls++; return wantErr })}
	store := vectordata.NewStore(provider)
	if _, ok := any(store).(io.Closer); ok {
		t.Fatal("store facade advertises provider-specific cleanup")
	}
	collection, err := vectordata.GetCollection[string, record](store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.(io.Closer).Close(); err != nil || collectionCalls != 1 || calls != 0 {
		t.Fatalf("collection close = %v, store calls = %d, collection calls = %d", err, calls, collectionCalls)
	}
	if err := provider.Close(); err != wantErr || calls != 1 || collectionCalls != 1 {
		t.Fatalf("provider close = %v, store calls = %d, collection calls = %d", err, calls, collectionCalls)
	}
}

type lifecycleBackend struct {
	vectordata.Collection[any, any]
	vectordata.StoreProvider
	t     *testing.T
	err   error
	calls []string
}

func (b *lifecycleBackend) check(ctx context.Context, operation, name string) {
	b.t.Helper()
	if ctx != b.t.Context() || name != "records" {
		b.t.Fatal("lifecycle arguments changed")
	}
	b.calls = append(b.calls, operation)
}

func (*lifecycleBackend) Name() string { return "records" }

func (b *lifecycleBackend) Exists(ctx context.Context) (bool, error) {
	b.check(ctx, "Exists", "records")
	return true, b.err
}

func (b *lifecycleBackend) EnsureExists(ctx context.Context) error {
	b.check(ctx, "EnsureExists", "records")
	return b.err
}

func (b *lifecycleBackend) EnsureDeleted(ctx context.Context) error {
	b.check(ctx, "EnsureDeleted", "records")
	return b.err
}

func (b *lifecycleBackend) ListCollectionNames(ctx context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		b.check(ctx, "ListCollectionNames", "records")
		if yield("records", nil) {
			yield("", b.err)
		}
	}
}

func (b *lifecycleBackend) CollectionExists(ctx context.Context, name string) (bool, error) {
	b.check(ctx, "CollectionExists", name)
	return true, b.err
}

func (b *lifecycleBackend) EnsureCollectionDeleted(ctx context.Context, name string) error {
	b.check(ctx, "EnsureCollectionDeleted", name)
	return b.err
}

func TestStore_LifecycleForwarding(t *testing.T) {
	backend := &lifecycleBackend{t: t, err: context.Canceled}
	store := vectordata.NewStore(&storeProvider{StoreProvider: backend, open: func(string, reflect.Type, reflect.Type, *vectordata.Definition) (vectordata.Collection[any, any], error) {
		return backend, nil
	}})
	collection, err := vectordata.GetCollection[string, record](store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if collection.Name() != "records" {
		t.Fatal("collection name changed")
	}
	if found, err := collection.Exists(t.Context()); !found || err != context.Canceled {
		t.Fatalf("Exists = (%v, %v)", found, err)
	}
	if err := collection.EnsureExists(t.Context()); err != context.Canceled {
		t.Fatalf("EnsureExists = %v", err)
	}
	if err := collection.EnsureDeleted(t.Context()); err != context.Canceled {
		t.Fatalf("EnsureDeleted = %v", err)
	}
	if found, err := store.CollectionExists(t.Context(), "records"); !found || err != context.Canceled {
		t.Fatalf("CollectionExists = (%v, %v)", found, err)
	}
	if err := store.EnsureCollectionDeleted(t.Context(), "records"); err != context.Canceled {
		t.Fatalf("EnsureCollectionDeleted = %v", err)
	}
	var names []string
	var listErr error
	for name, err := range store.ListCollectionNames(t.Context()) {
		if err != nil {
			listErr = err
		} else {
			names = append(names, name)
		}
	}
	if !slices.Equal(names, []string{"records"}) || listErr != context.Canceled {
		t.Fatalf("ListCollectionNames = (%v, %v)", names, listErr)
	}
	if !slices.Equal(backend.calls, []string{"Exists", "EnsureExists", "EnsureDeleted", "CollectionExists", "EnsureCollectionDeleted", "ListCollectionNames"}) {
		t.Fatalf("lifecycle calls = %v", backend.calls)
	}
}
