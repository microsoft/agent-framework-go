// Copyright (c) Microsoft. All rights reserved.

package vectordata_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/vectordata"
)

type record struct {
	ID   string
	Text string
}

type testCollection struct {
	vectordata.Collection[string, *record]
	get    func(context.Context, string, *vectordata.RecordRetrievalOptions) (*record, bool, error)
	delete func(context.Context, string) error
	upsert func(context.Context, []*record) error
}

func (c *testCollection) Get(ctx context.Context, key string, options *vectordata.RecordRetrievalOptions) (*record, bool, error) {
	return c.get(ctx, key, options)
}

func (c *testCollection) Delete(ctx context.Context, key string) error { return c.delete(ctx, key) }

func (c *testCollection) Upsert(ctx context.Context, records ...*record) error {
	return c.upsert(ctx, records)
}

func TestGetBatch_SequentialAndLazy(t *testing.T) {
	ctx := t.Context()
	options := &vectordata.RecordRetrievalOptions{IncludeVectors: true}
	want := []*record{{ID: "first"}, {ID: "second"}}
	var calls []string
	collection := &testCollection{get: func(got context.Context, key string, opts *vectordata.RecordRetrievalOptions) (*record, bool, error) {
		if got != ctx || opts != options {
			t.Fatal("arguments were not preserved")
		}
		calls = append(calls, key)
		switch key {
		case "first":
			return want[0], true, nil
		case "second":
			return want[1], true, nil
		default:
			return nil, false, nil
		}
	}}
	keys := []string{"missing", "first", "second"}
	results := vectordata.GetBatch(ctx, collection, keys, options)
	if len(calls) != 0 {
		t.Fatal("batch was retrieved before iteration")
	}
	var got []*record
	for value, err := range results {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if !slices.Equal(got, want) || !slices.Equal(calls, keys) || !options.IncludeVectors {
		t.Fatalf("records = %v, calls = %v", got, calls)
	}
}

func TestGetBatch_StopsOnErrorOrConsumerBreak(t *testing.T) {
	want := errors.New("provider failed")
	for _, stopEarly := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "consumer"}[stopEarly], func(t *testing.T) {
			var calls []string
			collection := &testCollection{get: func(_ context.Context, key string, _ *vectordata.RecordRetrievalOptions) (*record, bool, error) {
				calls = append(calls, key)
				if key == "error" {
					return nil, false, want
				}
				return &record{ID: key}, true, nil
			}}
			var values int
			var failure error
			for value, err := range vectordata.GetBatch(t.Context(), collection, []string{"first", "error", "later"}, nil) {
				if err != nil {
					if value != nil {
						t.Fatal("error result contained a record")
					}
					failure = err
					break
				}
				values++
				if stopEarly {
					break
				}
			}
			if values != 1 || stopEarly && (failure != nil || len(calls) != 1) ||
				!stopEarly && (failure != want || len(calls) != 2) {
				t.Fatalf("values = %d, failure = %v, calls = %v", values, failure, calls)
			}
		})
	}
}

func TestGetBatch_CancellationBetweenRecords(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	collection := &testCollection{get: func(context.Context, string, *vectordata.RecordRetrievalOptions) (*record, bool, error) {
		calls++
		return &record{ID: "first"}, true, nil
	}}
	var failure error
	for _, err := range vectordata.GetBatch(ctx, collection, []string{"first", "second"}, nil) {
		if err != nil {
			failure = err
			break
		}
		cancel()
	}
	if failure != context.Canceled || calls != 1 {
		t.Fatalf("failure = %v, calls = %d", failure, calls)
	}
}

type nativeCollection struct {
	*testCollection
	getBatch    func(context.Context, []string, *vectordata.RecordRetrievalOptions) iter.Seq2[*record, error]
	deleteBatch func(context.Context, []string) error
}

func (c *nativeCollection) GetBatch(ctx context.Context, keys []string, options *vectordata.RecordRetrievalOptions) iter.Seq2[*record, error] {
	return c.getBatch(ctx, keys, options)
}

func (c *nativeCollection) DeleteBatch(ctx context.Context, keys []string) error {
	return c.deleteBatch(ctx, keys)
}

func TestBatchHelpers_UseNativeOperations(t *testing.T) {
	ctx := t.Context()
	keys := []string{"first", "second"}
	options := &vectordata.RecordRetrievalOptions{IncludeVectors: true}
	wantErr := errors.New("native failed")
	for _, failure := range []error{nil, wantErr} {
		t.Run(map[bool]string{true: "success", false: "error"}[failure == nil], func(t *testing.T) {
			getCalls, deleteCalls, singleCalls := 0, 0, 0
			collection := &nativeCollection{
				testCollection: &testCollection{
					get: func(context.Context, string, *vectordata.RecordRetrievalOptions) (*record, bool, error) {
						singleCalls++
						return nil, false, nil
					},
					delete: func(context.Context, string) error { singleCalls++; return nil },
				},
				getBatch: func(got context.Context, gotKeys []string, opts *vectordata.RecordRetrievalOptions) iter.Seq2[*record, error] {
					getCalls++
					if got != ctx || &gotKeys[0] != &keys[0] || opts != options {
						t.Fatal("native retrieval arguments were changed")
					}
					return func(yield func(*record, error) bool) {
						yield(&record{ID: "second"}, failure)
					}
				},
				deleteBatch: func(got context.Context, gotKeys []string) error {
					deleteCalls++
					if got != ctx || &gotKeys[0] != &keys[0] {
						t.Fatal("native deletion arguments were changed")
					}
					return failure
				},
			}
			count := 0
			for value, err := range vectordata.GetBatch(ctx, collection, keys, options) {
				count++
				if err != failure || failure != nil && value != nil || failure == nil && value.ID != "second" {
					t.Fatalf("native result = %+v, %v", value, err)
				}
			}
			if err := vectordata.DeleteBatch(ctx, collection, keys); err != failure {
				t.Fatalf("native delete = %v", err)
			}
			if count != 1 || getCalls != 1 || deleteCalls != 1 || singleCalls != 0 {
				t.Fatalf("counts = %d, %d, %d, %d", count, getCalls, deleteCalls, singleCalls)
			}
		})
	}
}

func TestGetBatch_NativeStopsOnConsumerBreakAndError(t *testing.T) {
	wantErr := errors.New("native failed")
	for _, failure := range []error{nil, wantErr} {
		calls := 0
		collection := &nativeCollection{getBatch: func(context.Context, []string, *vectordata.RecordRetrievalOptions) iter.Seq2[*record, error] {
			return func(yield func(*record, error) bool) {
				for range 3 {
					calls++
					if !yield(&record{}, failure) {
						return
					}
				}
			}
		}}
		for _, err := range vectordata.GetBatch(t.Context(), collection, []string{"first", "second"}, nil) {
			if !errors.Is(err, failure) {
				t.Fatalf("GetBatch error = %v, want %v", err, failure)
			}
			break
		}
		if calls != 1 {
			t.Fatalf("native producer continued after stopping: %d calls", calls)
		}
	}
}

func TestBatchHelpers_EmptyAndInvalidInputs(t *testing.T) {
	collection := &nativeCollection{}
	for _, keys := range [][]string{nil, {}} {
		for _, err := range vectordata.GetBatch(t.Context(), collection, keys, nil) {
			if err != nil {
				t.Fatal(err)
			}
			t.Fatal("empty retrieval yielded a value")
		}
		if err := vectordata.DeleteBatch(t.Context(), collection, keys); err != nil {
			t.Fatal(err)
		}
	}
	var missing vectordata.Collection[string, *record]
	count := 0
	for value, err := range vectordata.GetBatch(t.Context(), missing, nil, nil) {
		count++
		if err == nil || value != nil {
			t.Fatal("expected nil collection error")
		}
	}
	if count != 1 || vectordata.DeleteBatch(t.Context(), missing, nil) == nil {
		t.Fatal("missing collection was accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, keys := range [][]string{nil, {"first"}} {
		count = 0
		for _, err := range vectordata.GetBatch(ctx, collection, keys, nil) {
			count++
			if err != context.Canceled {
				t.Fatalf("canceled retrieval = %v", err)
			}
		}
		if count != 1 || vectordata.DeleteBatch(ctx, collection, keys) != context.Canceled {
			t.Fatal("cancellation was not preserved")
		}
	}
}

func TestDeleteBatch_SequentialErrorsAndCancellation(t *testing.T) {
	wantErr := errors.New("delete failed")
	for _, cancelAfterFirst := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		var calls []string
		collection := &testCollection{delete: func(_ context.Context, key string) error {
			calls = append(calls, key)
			if cancelAfterFirst {
				cancel()
				return nil
			}
			if key == "error" {
				return wantErr
			}
			return nil
		}}
		err := vectordata.DeleteBatch(ctx, collection, []string{"missing", "error", "later"})
		cancel()
		if cancelAfterFirst && (err != context.Canceled || len(calls) != 1) ||
			!cancelAfterFirst && (err != wantErr || len(calls) != 2) {
			t.Fatalf("delete = %v, calls = %v", err, calls)
		}
	}
	var calls []string
	collection := &testCollection{delete: func(_ context.Context, key string) error { calls = append(calls, key); return nil }}
	if err := vectordata.DeleteBatch(t.Context(), collection, []string{"first", "second"}); err != nil ||
		!slices.Equal(calls, []string{"first", "second"}) {
		t.Fatalf("successful delete = %v, calls = %v", err, calls)
	}
}

func TestCollection_UpsertVariadic(t *testing.T) {
	ctx := t.Context()
	first, second := &record{ID: "first"}, &record{ID: "second"}
	batch := []*record{first, second}
	var nilBatch []*record
	emptyBatch := []*record{}
	for _, test := range []struct {
		name string
		want []*record
		call func(vectordata.Collection[string, *record]) error
	}{
		{"single", []*record{first}, func(c vectordata.Collection[string, *record]) error { return c.Upsert(ctx, first) }},
		{"multiple", batch, func(c vectordata.Collection[string, *record]) error { return c.Upsert(ctx, first, second) }},
		{"slice", batch, func(c vectordata.Collection[string, *record]) error { return c.Upsert(ctx, batch...) }},
		{"no records", nil, func(c vectordata.Collection[string, *record]) error { return c.Upsert(ctx) }},
		{"nil slice", nil, func(c vectordata.Collection[string, *record]) error { return c.Upsert(ctx, nilBatch...) }},
		{"empty slice", emptyBatch, func(c vectordata.Collection[string, *record]) error { return c.Upsert(ctx, emptyBatch...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, wantErr := range []error{nil, errors.New("upsert failed")} {
				calls := 0
				collection := &testCollection{upsert: func(got context.Context, values []*record) error {
					calls++
					if got != ctx || !slices.Equal(values, test.want) || (values == nil) != (test.want == nil) {
						t.Fatalf("batch arguments changed: %+v; want %+v", values, test.want)
					}
					return wantErr
				}}
				if err := test.call(collection); err != wantErr || calls != 1 {
					t.Fatalf("upsert = %v, calls = %d; want %v and one batch", err, calls, wantErr)
				}
			}
		})
	}
}

type byteKeyCollection struct {
	vectordata.Collection[[]byte, map[string]any]
}

func (*byteKeyCollection) Get(_ context.Context, key []byte, _ *vectordata.RecordRetrievalOptions) (map[string]any, bool, error) {
	return map[string]any{"key": key}, true, nil
}

func TestGetBatch_NonComparableKeys(t *testing.T) {
	key := []byte{1, 2, 3}
	count := 0
	for value, err := range vectordata.GetBatch(t.Context(), &byteKeyCollection{}, [][]byte{key}, nil) {
		count++
		if err != nil || !slices.Equal(value["key"].([]byte), key) {
			t.Fatalf("record = %+v, %v", value, err)
		}
	}
	if count != 1 {
		t.Fatalf("records = %d", count)
	}
}
