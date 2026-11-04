// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

func TestCollection_VectorHybridAndServerTextSearch(t *testing.T) {
	for _, hybrid := range []bool{false, true} {
		for _, test := range []struct {
			name  string
			value any
			kind  string
		}{
			{"vector", []float32{1, 2, 3}, "vector"},
			{"embedding", &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 2, 3}}, "vector"},
			{"server text", "search string", "text"},
		} {
			t.Run(fmt.Sprintf("%s/hybrid=%t", test.name, hybrid), func(t *testing.T) {
				calls := 0
				var body map[string]any
				store := newStore(t, func(request *http.Request) (*http.Response, error) {
					calls++
					if request.URL.Path != "/indexes('records')/docs/search" {
						t.Errorf("search path = %q", request.URL.Path)
					}
					body = jsonBody(t, request)
					return response(http.StatusOK, `{"value":[
						{"id":"one","text":"first","vector":[1,2,3],"@search.score":1},
						{"id":"two","text":"second","vector":[3,2,1],"@search.score":0.75},
						{"id":"three","@search.score":0.5}
					]}`), nil
				}, azureprovider.AISearchConfig{})
				collection := openCollection[string, document](t, store, nil)
				filter := &vectordata.Filter{Operator: vectordata.Equal, Property: "Text", Value: "filter"}
				var results []vectordata.SearchResult[document]
				if hybrid {
					searcher := collection.(vectordata.KeywordHybridSearcher[document])
					sequence := searcher.HybridSearch(t.Context(), test.value, []string{"red", "blue"}, 5,
						&vectordata.HybridSearchOptions{Filter: filter, Skip: 3, IncludeVectors: true, ScoreThreshold: new(0.75)})
					if calls != 0 {
						t.Fatal("hybrid search started eagerly")
					}
					results = collect(t, sequence)
					if body["search"] != "red blue" || body["searchFields"] != "text" {
						t.Fatalf("hybrid text request = %v", body)
					}
					if _, found := body["select"]; found {
						t.Fatal("hybrid IncludeVectors=true excluded vectors")
					}
					if !slices.Equal(results[0].Record.Vector, []float32{1, 2, 3}) {
						t.Fatalf("hybrid vectors = %v", results[0].Record.Vector)
					}
				} else {
					sequence := collection.Search(t.Context(), test.value, 5,
						&vectordata.SearchOptions{Filter: filter, Skip: 3, ScoreThreshold: new(0.75)})
					if calls != 0 {
						t.Fatal("vector search started eagerly")
					}
					results = collect(t, sequence)
					if _, found := body["search"]; found {
						t.Fatal("pure vector search included keyword text")
					}
					if body["select"] != "id,text,rank,tags" || results[0].Record.Vector != nil {
						t.Fatalf("default vector projection = %v", body)
					}
				}
				if len(results) != 2 || results[0].Record.ID != "one" || results[1].Record.ID != "two" ||
					results[1].Score == nil || *results[1].Score != 0.75 || calls != 1 {
					t.Fatalf("search results = %v, calls = %d", results, calls)
				}
				query := body["vectorQueries"].([]any)[0].(map[string]any)
				if query["kind"] != test.kind || query["fields"] != "vector" || query["k"] != float64(8) ||
					body["top"] != float64(5) || body["skip"] != float64(3) ||
					body["filter"] != "(text eq 'filter')" || body["vectorFilterMode"] != "preFilter" {
					t.Fatalf("vector search request = %v", body)
				}
				if test.kind == "text" && query["text"] != "search string" {
					t.Fatalf("server vectorization request = %v", query)
				}
				if filter.Property != "Text" {
					t.Fatal("search modified the input filter")
				}
			})
		}
	}
}

func TestCollection_SearchPaginationAndEarlyStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			calls := 0
			var bodies []*trackingBody
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				calls++
				body := jsonBody(t, request)
				payload := `{"value":[{"id":"two","@search.score":0.8}]}`
				if calls == 1 {
					payload = `{
						"value":[{"id":"one","@search.score":0.9}],
						"@odata.nextLink":"/indexes('records')/docs/search?api-version=2025-09-01",
						"@search.nextPageParameters":{"top":2,"skip":1,"filter":"rank gt 0"}
					}`
				} else if body["skip"] != float64(1) || body["filter"] != "rank gt 0" {
					t.Errorf("continuation body = %v", body)
				}
				result := response(http.StatusOK, payload)
				reader := &trackingBody{Reader: bytes.NewReader([]byte(payload))}
				bodies = append(bodies, reader)
				result.Body = reader
				return result, nil
			}, azureprovider.AISearchConfig{})
			collection := openCollection[string, document](t, store, nil)
			var ids []string
			for result, err := range collection.Search(t.Context(), []float32{1, 2, 3}, 2, nil) {
				if err != nil {
					t.Fatal(err)
				}
				for _, body := range bodies {
					if body.closes.Load() == 0 {
						t.Error("response body remained open while yielding a record")
					}
				}
				ids = append(ids, result.Record.ID)
				if stop {
					break
				}
			}
			want := []string{"one", "two"}
			if stop {
				want = want[:1]
			}
			if !slices.Equal(ids, want) || calls != len(want) {
				t.Fatalf("records = %v, HTTP calls = %d", ids, calls)
			}
		})
	}
}

func TestCollection_SearchFailuresAndContinuationOrigin(t *testing.T) {
	for _, payload := range []string{
		`{}`,
		`{"value":[{"id":"one","@search.score":"invalid"}]}`,
		`{"value":[{"id":"one"},{"text":"missing key"}]}`,
		`{"value":[],"@odata.nextLink":"https://other.example/indexes","@search.nextPageParameters":{"skip":1}}`,
		`{"value":[],"@odata.nextLink":"https://user:secret@example.search.windows.net/indexes","@search.nextPageParameters":{"skip":1}}`,
		`{"value":[],"@odata.nextLink":"/indexes('records')/docs/search"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			calls := 0
			store := newStore(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(http.StatusOK, payload), nil
			}, azureprovider.AISearchConfig{})
			collection := openCollection[string, document](t, store, nil)
			_ = sequenceError(t, collection.Search(t.Context(), []float32{1, 2, 3}, 3, nil))
			if calls != 1 {
				t.Fatalf("invalid response triggered %d requests", calls)
			}
		})
	}
}

func TestCollection_SearchInputValidation(t *testing.T) {
	calls := 0
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusOK, `{"value":[]}`), nil
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, document](t, store, nil)
	for _, test := range []struct {
		value   any
		top     int
		options *vectordata.SearchOptions
	}{
		{nil, 1, nil},
		{[]float32(nil), 1, nil},
		{(*embedding.Of[float32])(nil), 1, nil},
		{[]float32{1, 2, 3}, 0, nil},
		{[]float32{1, 2, 3}, 1, &vectordata.SearchOptions{Skip: -1}},
		{[]float32{1, 2, 3}, 2, &vectordata.SearchOptions{Skip: int(^uint(0) >> 1)}},
		{[]float32{1, 2, 3}, 1, &vectordata.SearchOptions{VectorProperty: "Text"}},
		{42, 1, nil},
	} {
		_ = sequenceError(t, collection.Search(t.Context(), test.value, test.top, test.options))
	}
	hybrid := collection.(vectordata.KeywordHybridSearcher[document])
	_ = sequenceError(t, hybrid.HybridSearch(t.Context(), []float32{1, 2, 3}, nil, 1, nil))
	_ = sequenceError(t, hybrid.HybridSearch(t.Context(), []float32{1, 2, 3}, []string{"text"}, 1,
		&vectordata.HybridSearchOptions{AdditionalProperty: "Rank"}))
	if calls != 0 {
		t.Fatal("invalid search input performed I/O")
	}
}

func TestCollection_CancelSearchBetweenPages(t *testing.T) {
	calls := 0
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusOK, `{
			"value":[{"id":"one"}],
			"@odata.nextLink":"/indexes('records')/docs/search?api-version=2025-09-01",
			"@search.nextPageParameters":{"top":2,"skip":1}
		}`), nil
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, document](t, store, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	count := 0
	var failure error
	for _, err := range collection.Search(ctx, []float32{1, 2, 3}, 2, nil) {
		if err != nil {
			failure = err
			continue
		}
		count++
		cancel()
	}
	if count != 1 || calls != 1 || !errors.Is(failure, context.Canceled) {
		t.Fatalf("count=%d calls=%d failure=%v", count, calls, failure)
	}
}

func TestCollection_FilteredRetrievalProjection(t *testing.T) {
	for _, include := range []bool{false, true} {
		var requestBody map[string]any
		store := newStore(t, func(request *http.Request) (*http.Response, error) {
			requestBody = jsonBody(t, request)
			return response(http.StatusOK, `{"value":[{"id":"one","vector":[1,2,3]}]}`), nil
		}, azureprovider.AISearchConfig{})
		collection := openCollection[string, document](t, store, nil)
		results := collect(t, collection.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: "one"},
			1, &vectordata.FilteredRecordRetrievalOptions{IncludeVectors: include}))
		_, selected := requestBody["select"]
		if len(results) != 1 || selected == include || (results[0].Vector != nil) != include {
			t.Fatalf("projection=%v results=%v", requestBody, results)
		}
		if _, found := requestBody["vectorQueries"]; found {
			t.Fatal("filtered retrieval included a vector query")
		}
	}
}

func TestCollection_SearchScoreShapes(t *testing.T) {
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"value":[{"id":"zero","@search.score":0},{"id":"null","@search.score":null},{"id":"missing"}]}`), nil
	}, azureprovider.AISearchConfig{})
	results := collect(t, openCollection[string, document](t, store, nil).Search(t.Context(), []float32{1, 2, 3}, 3, nil))
	if len(results) != 3 || results[0].Score == nil || *results[0].Score != 0 || results[1].Score != nil || results[2].Score != nil {
		data, _ := json.Marshal(results)
		t.Fatalf("search scores = %s", data)
	}
	if !reflect.DeepEqual([]string{results[0].Record.ID, results[1].Record.ID, results[2].Record.ID}, []string{"zero", "null", "missing"}) {
		t.Fatal("score handling changed result order")
	}
}
