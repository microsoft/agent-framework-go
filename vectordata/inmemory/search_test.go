// Copyright (c) Microsoft. All rights reserved.

package inmemory_test

import (
	"cmp"
	"context"
	"errors"
	"iter"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
)

func vectorDefinition(distance vectordata.DistanceFunction, index vectordata.IndexKind) *vectordata.Definition {
	return &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.VectorProperty{Name: "Vector", Dimensions: 4, DistanceFunction: distance, IndexKind: index},
	}}
}

// Uses the exact, opposite, and orthogonal vectors from the upstream distance
// conformance tests, including a threshold equal to the second returned score.
func TestSearch_DistanceFunctions(t *testing.T) {
	for _, test := range []struct {
		distance vectordata.DistanceFunction
		scores   []float64
	}{
		{"", []float64{1, 0, -1}},
		{vectordata.CosineSimilarity, []float64{1, 0, -1}},
		{vectordata.CosineDistance, []float64{0, 1, 2}},
		{vectordata.DotProductSimilarity, []float64{1, 0, -1}},
		{vectordata.EuclideanDistance, []float64{0, math.Sqrt(3), 2}},
	} {
		t.Run(string(test.distance), func(t *testing.T) {
			c := createCollection[string, document](t, vectorDefinition(test.distance, ""))
			base := []float32{1, 0, 0, 0}
			if err := c.Upsert(t.Context(),
				document{ID: "exact", Vector: base},
				document{ID: "opposite", Vector: []float32{-1, 0, 0, 0}},
				document{ID: "orthogonal", Vector: []float32{0, -1, -1, 0}},
				document{ID: "null"},
			); err != nil {
				t.Fatal(err)
			}
			results := collect(t, c.Search(t.Context(), base, 3, nil))
			if len(results) != 3 {
				t.Fatalf("result count = %d", len(results))
			}
			for i, id := range []string{"exact", "orthogonal", "opposite"} {
				result := results[i]
				if result.Record.ID != id || result.Score == nil || math.Abs(*result.Score-test.scores[i]) > 0.00001 || len(result.Record.Vector) != 4 {
					t.Fatalf("result %d = %+v, want %s at %g", i, result, id, test.scores[i])
				}
			}
			threshold := *results[1].Score
			filtered := collect(t, c.Search(t.Context(), base, 3, &vectordata.SearchOptions{ScoreThreshold: &threshold}))
			if len(filtered) != 2 || filtered[1].Record.ID != "orthogonal" {
				t.Fatalf("inclusive threshold results = %+v", filtered)
			}
			options := &vectordata.SearchOptions{Skip: 1, IncludeVectors: true}
			page := collect(t, c.Search(t.Context(), base, 1, options))
			if len(page) != 1 || page[0].Record.ID != "orthogonal" || !slices.Equal(page[0].Record.Vector, []float32{0, -1, -1, 0}) {
				t.Fatalf("page = %+v", page)
			}
			page[0].Record.Vector[0] = 99
			stored, _, err := c.Get(t.Context(), "orthogonal", &vectordata.RecordRetrievalOptions{IncludeVectors: true})
			if err != nil || stored.Vector[0] != 99 || options.Skip != 1 || !options.IncludeVectors {
				t.Fatal("search did not retain stored data or changed options")
			}
			beforeTop := collect(t, c.Search(t.Context(), base, 1, &vectordata.SearchOptions{
				Filter: &vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: "opposite"},
			}))
			if len(beforeTop) != 1 || beforeTop[0].Record.ID != "opposite" {
				t.Fatalf("filter was not applied before ranking/top: %+v", beforeTop)
			}
			if beyond := collect(t, c.Search(t.Context(), base, math.MaxInt, &vectordata.SearchOptions{Skip: math.MaxInt})); len(beyond) != 0 {
				t.Fatalf("out-of-range page = %v", beyond)
			}
		})
	}
}

func TestSearch_UndefinedScores(t *testing.T) {
	for _, distance := range []vectordata.DistanceFunction{
		"", vectordata.CosineSimilarity, vectordata.CosineDistance,
		vectordata.DotProductSimilarity, vectordata.EuclideanDistance,
	} {
		t.Run(string(distance), func(t *testing.T) {
			c := createCollection[string, document](t, vectorDefinition(distance, ""))
			base := []float32{1, 0, 0, 0}
			zero := []float32{0, 0, 0, 0}
			nan := []float32{float32(math.NaN()), 0, 0, 0}
			if err := c.Upsert(t.Context(),
				document{ID: "zero", Vector: zero},
				document{ID: "exact", Vector: base},
				document{ID: "nan", Vector: nan},
			); err != nil {
				t.Fatal(err)
			}
			cosine := distance == "" || distance == vectordata.CosineSimilarity || distance == vectordata.CosineDistance
			count := 2
			if cosine {
				count = 1
			}
			for _, top := range []int{1, 3} {
				results := collect(t, c.Search(t.Context(), base, top, nil))
				if len(results) != min(top, count) || results[0].Record.ID != "exact" {
					t.Fatalf("top %d results = %+v", top, results)
				}
				for _, result := range results {
					if result.Score == nil || math.IsNaN(*result.Score) {
						t.Fatalf("undefined score returned: %+v", result)
					}
				}
			}
			results := collect(t, c.Search(t.Context(), zero, 3, nil))
			count = 2
			if cosine {
				count = 0
			}
			if len(results) != count {
				t.Fatalf("zero-norm query results = %+v, want %d", results, count)
			}
			for _, result := range results {
				if result.Score == nil || math.IsNaN(*result.Score) {
					t.Fatalf("undefined zero-norm query score: %+v", result)
				}
			}
			if results := collect(t, c.Search(t.Context(), nan, 3, nil)); len(results) != 0 {
				t.Fatalf("NaN query returned results: %+v", results)
			}
		})
	}
}

func TestSearch_UnsupportedMetricsAndIndexHints(t *testing.T) {
	for _, distance := range []vectordata.DistanceFunction{
		vectordata.NegativeDotProductSimilarity, vectordata.EuclideanSquaredDistance,
		vectordata.HammingDistance, vectordata.ManhattanDistance, "custom",
	} {
		t.Run(string(distance), func(t *testing.T) {
			c := createCollection[string, document](t, vectorDefinition(distance, ""))
			_ = sequenceError(t, c.Search(t.Context(), []float32{1, 0, 0, 0}, 1, nil))
		})
	}
	for _, index := range []vectordata.IndexKind{
		"", vectordata.Flat, vectordata.HNSW, vectordata.IVFFlat, vectordata.DiskANN,
		vectordata.QuantizedFlat, vectordata.Dynamic, "custom",
	} {
		t.Run("index-"+string(index), func(t *testing.T) {
			c := createCollection[string, document](t, vectorDefinition("", index))
			if err := c.Upsert(t.Context(), document{ID: "id", Vector: []float32{1, 0, 0, 0}}); err != nil {
				t.Fatal(err)
			}
			if got := collect(t, c.Search(t.Context(), []float32{1, 0, 0, 0}, 1, nil)); len(got) != 1 || *got[0].Score != 1 {
				t.Fatalf("index hint changed flat search: %+v", got)
			}
		})
	}
}

func TestSearch_Validation(t *testing.T) {
	c := createCollection[string, document](t, nil)
	vector := []float32{1, 0, 0, 0}
	for name, test := range map[string]struct {
		value   any
		top     int
		options *vectordata.SearchOptions
	}{
		"nil":             {top: 1},
		"typed-nil":       {value: []float32(nil), top: 1},
		"zero-top":        {value: vector},
		"negative-top":    {value: vector, top: -1},
		"dimensions":      {value: []float32{1}, top: 1},
		"float64":         {value: []float64{1, 0, 0, 0}, top: 1},
		"ungenerated":     {value: "query", top: 1},
		"negative-skip":   {value: vector, top: 1, options: &vectordata.SearchOptions{Skip: -1}},
		"unknown-vector":  {value: vector, top: 1, options: &vectordata.SearchOptions{VectorProperty: "missing"}},
		"non-vector":      {value: vector, top: 1, options: &vectordata.SearchOptions{VectorProperty: "Text"}},
		"invalid-filter":  {value: vector, top: 1, options: &vectordata.SearchOptions{Filter: &vectordata.Filter{}}},
		"unknown-filter":  {value: vector, top: 1, options: &vectordata.SearchOptions{Filter: &vectordata.Filter{Operator: vectordata.Equal, Property: "missing", Value: 1}}},
		"filter-type":     {value: vector, top: 1, options: &vectordata.SearchOptions{Filter: &vectordata.Filter{Operator: vectordata.Equal, Property: "Text", Value: 1}}},
		"string-contains": {value: vector, top: 1, options: &vectordata.SearchOptions{Filter: &vectordata.Filter{Operator: vectordata.Contains, Property: "Text", Value: "part"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_ = sequenceError(t, c.Search(t.Context(), test.value, test.top, test.options))
		})
	}
	type multiple struct {
		ID     string    `vectordata:"key"`
		First  []float32 `vectordata:"vector,dimensions=4"`
		Second []float32 `vectordata:"vector,dimensions=4"`
	}
	ambiguous := createCollection[string, multiple](t, nil)
	_ = sequenceError(t, ambiguous.Search(t.Context(), vector, 1, nil))
	type noVectors struct {
		ID string `vectordata:"key"`
	}
	empty := createCollection[string, noVectors](t, nil)
	_ = sequenceError(t, empty.Search(t.Context(), vector, 1, nil))
	type unsupported struct {
		ID     string    `vectordata:"key"`
		Vector []float64 `vectordata:"vector,dimensions=4"`
	}
	if _, err := inmemory.NewCollection[string, unsupported]("records", nil); err == nil {
		t.Fatal("accepted unsupported float64 vectors")
	}
}

type filterRecord struct {
	ID          int       `vectordata:"key"`
	Vector      []float32 `vectordata:"vector,dimensions=3"`
	Int         int       `vectordata:"data"`
	String      *string   `vectordata:"data"`
	Bool        bool      `vectordata:"data"`
	Int2        int
	StringArray []string `vectordata:"data"`
	StringList  []string `vectordata:"data"`
}

func filterFixture(t *testing.T) (*inmemory.Collection[int, filterRecord], *inmemory.Collection[int, map[string]any]) {
	t.Helper()
	typed := createCollection[int, filterRecord](t, nil)
	properties := []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[int]()},
		&vectordata.VectorProperty{Name: "Vector", Type: reflect.TypeFor[[]float32](), Dimensions: 3},
	}
	for _, field := range reflect.VisibleFields(reflect.TypeFor[filterRecord]()) {
		if field.Name != "ID" && field.Name != "Vector" {
			properties = append(properties, &vectordata.DataProperty{Name: field.Name, Type: field.Type})
		}
	}
	dynamic := createCollection[int, map[string]any](t, &vectordata.Definition{Properties: properties})
	records := []filterRecord{
		{ID: 0, Int: 8, String: new("foo"), Bool: true, Int2: 80, StringArray: []string{"x", "y"}, StringList: []string{"x", "y"}},
		{ID: 1, Int: 9, String: new("bar"), Int2: 90, StringArray: []string{"a", "b"}, StringList: []string{"a", "b"}},
		{ID: 2, Int: 9, String: new("foo"), Bool: true, Int2: 9, StringArray: []string{"x"}, StringList: []string{"x"}},
		{ID: 3, Int: 10, Int2: 100, StringArray: []string{"x", "y", "z"}, StringList: []string{"x", "y", "z"}},
		{ID: 4, Int: 11, String: new(`>with $om[ specia]"chara<ters'and\stuff`), Bool: true, Int2: 101, StringArray: []string{"y", "z"}, StringList: []string{"y", "z"}},
	}
	for i := range records {
		records[i].Vector = []float32{1, 2, 3}
		record := reflect.ValueOf(records[i])
		values := make(map[string]any)
		for _, field := range reflect.VisibleFields(record.Type()) {
			values[field.Name] = record.FieldByIndex(field.Index).Interface()
		}
		if err := dynamic.Upsert(t.Context(), values); err != nil {
			t.Fatal(err)
		}
	}
	if err := typed.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	return typed, dynamic
}

func TestSearch_Filters(t *testing.T) {
	typed, dynamic := filterFixture(t)
	for _, test := range []struct {
		name   string
		filter vectordata.Filter
		ids    []int
	}{
		{"equal-int", vectordata.Filter{Operator: vectordata.Equal, Property: "Int", Value: 8}, []int{0}},
		{"equal-string", vectordata.Filter{Operator: vectordata.Equal, Property: "String", Value: "foo"}, []int{0, 2}},
		{"equal-null", vectordata.Filter{Operator: vectordata.Equal, Property: "String", Value: nil}, []int{3}},
		{"equal-typed-null", vectordata.Filter{Operator: vectordata.Equal, Property: "String", Value: (*string)(nil)}, []int{3}},
		{"nullable-input", vectordata.Filter{Operator: vectordata.Equal, Property: "Int", Value: new(8)}, []int{0}},
		{"int-not-null", vectordata.Filter{Operator: vectordata.Equal, Property: "Int", Value: nil}, nil},
		{"special-characters", vectordata.Filter{Operator: vectordata.Equal, Property: "String", Value: `>with $om[ specia]"chara<ters'and\stuff`}, []int{4}},
		{"literal-value", vectordata.Filter{Operator: vectordata.Equal, Property: "String", Value: "foo; DROP TABLE records;"}, nil},
		{"not-substring", vectordata.Filter{Operator: vectordata.Equal, Property: "String", Value: "some"}, nil},
		{"not-equal-int", vectordata.Filter{Operator: vectordata.NotEqual, Property: "Int", Value: 9}, []int{0, 3, 4}},
		{"not-equal-string", vectordata.Filter{Operator: vectordata.NotEqual, Property: "String", Value: "foo"}, []int{1, 3, 4}},
		{"not-null", vectordata.Filter{Operator: vectordata.NotEqual, Property: "String", Value: nil}, []int{0, 1, 2, 4}},
		{"greater", vectordata.Filter{Operator: vectordata.GreaterThan, Property: "Int", Value: 9}, []int{3, 4}},
		{"greater-equal", vectordata.Filter{Operator: vectordata.GreaterThanOrEqual, Property: "Int", Value: 9}, []int{1, 2, 3, 4}},
		{"less", vectordata.Filter{Operator: vectordata.LessThan, Property: "Int", Value: 9}, []int{0}},
		{"less-equal", vectordata.Filter{Operator: vectordata.LessThanOrEqual, Property: "Int", Value: 9}, []int{0, 1, 2}},
		{"bool", vectordata.Filter{Operator: vectordata.Equal, Property: "Bool", Value: true}, []int{0, 2, 4}},
		{"array-contains", vectordata.Filter{Operator: vectordata.Contains, Property: "StringArray", Value: "y"}, []int{0, 3, 4}},
		{"list-contains", vectordata.Filter{Operator: vectordata.Contains, Property: "StringList", Value: "a"}, []int{1}},
		{"in", vectordata.Filter{Operator: vectordata.In, Property: "Int", Value: []int{8, 10}}, []int{0, 3}},
		{"in-array", vectordata.Filter{Operator: vectordata.In, Property: "String", Value: [2]string{"foo", "bar"}}, []int{0, 1, 2}},
		{"in-nullable", vectordata.Filter{Operator: vectordata.In, Property: "String", Value: []any{nil, "foo"}}, []int{0, 2, 3}},
		{"in-empty", vectordata.Filter{Operator: vectordata.In, Property: "Int", Value: []int{}}, nil},
		{"and", vectordata.Filter{Operator: vectordata.And, Operands: []vectordata.Filter{
			{Operator: vectordata.GreaterThanOrEqual, Property: "Int", Value: 9},
			{Operator: vectordata.Equal, Property: "Bool", Value: true},
		}}, []int{2, 4}},
		{"or", vectordata.Filter{Operator: vectordata.Or, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Int", Value: 8},
			{Operator: vectordata.Equal, Property: "Int2", Value: 100},
		}}, []int{0, 3}},
		{"not", vectordata.Filter{Operator: vectordata.Not, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Int", Value: 9},
		}}, []int{0, 3, 4}},
		{"nested", vectordata.Filter{Operator: vectordata.Or, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Int", Value: 8},
			{Operator: vectordata.And, Operands: []vectordata.Filter{
				{Operator: vectordata.GreaterThan, Property: "Int", Value: 8},
				{Operator: vectordata.Equal, Property: "String", Value: "foo"},
			}},
		}}, []int{0, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			vector := []float32{1, 2, 3}
			options := &vectordata.SearchOptions{Filter: &test.filter}
			var typedIDs, dynamicIDs, filteredIDs []int
			for _, result := range collect(t, typed.Search(t.Context(), vector, 10, options)) {
				typedIDs = append(typedIDs, result.Record.ID)
			}
			for _, result := range collect(t, dynamic.Search(t.Context(), vector, 10, options)) {
				dynamicIDs = append(dynamicIDs, result.Record["ID"].(int))
			}
			for _, record := range collect(t, typed.GetFiltered(t.Context(), test.filter, 10, nil)) {
				filteredIDs = append(filteredIDs, record.ID)
			}
			for _, ids := range [][]int{typedIDs, dynamicIDs, filteredIDs} {
				slices.Sort(ids)
				if !slices.Equal(ids, test.ids) {
					t.Fatalf("filter returned IDs %v, want %v", ids, test.ids)
				}
			}
		})
	}
}

func TestGetFiltered_OrderingAndValidation(t *testing.T) {
	c, dynamic := filterFixture(t)
	for _, test := range []struct {
		name   string
		orders []vectordata.Sort
		skip   int
		top    int
		ids    []int
	}{
		{"multi-column-page", []vectordata.Sort{{Property: "Int"}, {Property: "String", Descending: true}}, 1, 2, []int{2, 1}},
		{"booleans", []vectordata.Sort{{Property: "Bool"}, {Property: "Int", Descending: true}}, 0, 10, []int{3, 1, 4, 2, 0}},
		{"nulls", []vectordata.Sort{{Property: "String"}, {Property: "Int", Descending: true}}, 0, 10, []int{3, 4, 1, 2, 0}},
		{"unannotated", []vectordata.Sort{{Property: "Int2", Descending: true}}, 0, 10, []int{4, 3, 1, 0, 2}},
		{"skip-past-end", nil, math.MaxInt, math.MaxInt, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ids []int
			for _, record := range collect(t, c.GetFiltered(t.Context(), allRecords, test.top, &vectordata.FilteredRecordRetrievalOptions{
				OrderBy: test.orders, Skip: test.skip, IncludeVectors: true,
			})) {
				ids = append(ids, record.ID)
				if !slices.Equal(record.Vector, []float32{1, 2, 3}) {
					t.Fatalf("included vector = %v", record.Vector)
				}
			}
			if !slices.Equal(ids, test.ids) {
				t.Fatalf("ordered IDs = %v, want %v", ids, test.ids)
			}
			ids = nil
			for _, record := range collect(t, dynamic.GetFiltered(t.Context(), allRecords, test.top, &vectordata.FilteredRecordRetrievalOptions{
				OrderBy: test.orders, Skip: test.skip, IncludeVectors: true,
			})) {
				ids = append(ids, record["ID"].(int))
				if !slices.Equal(record["Vector"].([]float32), []float32{1, 2, 3}) {
					t.Fatalf("included dynamic vector = %v", record["Vector"])
				}
			}
			if !slices.Equal(ids, test.ids) {
				t.Fatalf("ordered dynamic IDs = %v, want %v", ids, test.ids)
			}
		})
	}
	for name, test := range map[string]struct {
		filter  vectordata.Filter
		top     int
		options *vectordata.FilteredRecordRetrievalOptions
	}{
		"zero-top":      {filter: allRecords},
		"filter":        {top: 1},
		"negative-skip": {filter: allRecords, top: 1, options: &vectordata.FilteredRecordRetrievalOptions{Skip: -1}},
		"empty-sort":    {filter: allRecords, top: 1, options: &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{}}}},
		"unknown-sort":  {filter: allRecords, top: 1, options: &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "missing"}}}},
		"sort-type":     {filter: allRecords, top: 1, options: &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "StringArray"}}}},
		"in-type":       {filter: vectordata.Filter{Operator: vectordata.In, Property: "Int", Value: []string{"8"}}, top: 1},
		"contains-type": {filter: vectordata.Filter{Operator: vectordata.Contains, Property: "StringArray", Value: 8}, top: 1},
		"ordered-type":  {filter: vectordata.Filter{Operator: vectordata.GreaterThan, Property: "StringArray", Value: []string{"x"}}, top: 1},
	} {
		t.Run(name, func(t *testing.T) {
			_ = sequenceError(t, c.GetFiltered(t.Context(), test.filter, test.top, test.options))
		})
	}
}

type paginationValue struct{ observe func() }

func (value paginationValue) Equal(paginationValue) bool {
	value.observe()
	return true
}

func TestGetFiltered_UnorderedPagination(t *testing.T) {
	type record struct {
		ID    int             `vectordata:"key"`
		Value paginationValue `vectordata:"data"`
	}
	for _, test := range []struct {
		name            string
		skip, top       int
		orderBy         []vectordata.Sort
		count, readings int
	}{
		{"first", 0, 1, nil, 1, 1},
		{"offset", 2, 2, nil, 2, 4},
		{"all", 0, math.MaxInt, nil, 6, 6},
		{"past end", math.MaxInt, 1, nil, 0, 6},
		{"ordered", 0, 1, []vectordata.Sort{{Property: "ID"}}, 1, 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := createCollection[int, record](t, nil)
			readings := 0
			records := make([]record, 6)
			for i := range records {
				records[i] = record{ID: i, Value: paginationValue{observe: func() { readings++ }}}
			}
			if err := c.Upsert(t.Context(), records...); err != nil {
				t.Fatal(err)
			}
			results := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{
				Operator: vectordata.Equal, Property: "Value", Value: paginationValue{},
			}, test.top, &vectordata.FilteredRecordRetrievalOptions{Skip: test.skip, OrderBy: test.orderBy}))
			if len(results) != test.count || readings != test.readings {
				t.Fatalf("page has %d records after %d evaluations, want %d records after %d evaluations",
					len(results), readings, test.count, test.readings)
			}
		})
	}
}

type comparisonValue struct{ rank int }

func (value comparisonValue) Compare(other comparisonValue) int {
	switch {
	case value.rank < other.rank:
		return math.MinInt
	case value.rank > other.rank:
		return math.MaxInt
	default:
		return 0
	}
}

type reverseComparison int

func (value reverseComparison) Compare(other reverseComparison) int {
	return cmp.Compare(other, value)
}

func TestGetFiltered_ValueTypes(t *testing.T) {
	type record struct {
		ID    int `vectordata:"key"`
		Value any
	}
	for name, pair := range map[string][2]any{
		"int": {1, 2}, "uint": {uint64(1), uint64(2)}, "float": {float32(1), float32(2)},
		"string": {"a", "b"}, "bool": {false, true}, "nullable": {(*int)(nil), new(2)},
		"time":              {time.Unix(1, 0), time.Unix(2, 0)},
		"uuid":              {uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002")},
		"compare-struct":    {comparisonValue{rank: 1}, comparisonValue{rank: 2}},
		"compare-pointer":   {new(comparisonValue{rank: 1}), new(comparisonValue{rank: 2})},
		"compare-primitive": {reverseComparison(2), reverseComparison(1)},
		"nested-pointer":    {new(new(1)), new(new(2))},
		"nested-interface":  {new(any(new(1))), new(any(new(2)))},
		"nested-null":       {new((*int)(nil)), new(new(2))},
	} {
		t.Run(name, func(t *testing.T) {
			c := createCollection[int, record](t, nil)
			if err := c.Upsert(t.Context(), record{ID: 2, Value: pair[1]}, record{ID: 1, Value: pair[0]}); err != nil {
				t.Fatal(err)
			}
			got := collect(t, c.GetFiltered(t.Context(), allRecords, 10, &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "Value"}}}))
			if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
				t.Fatalf("ordered records = %v", got)
			}
			equal := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Equal, Property: "Value", Value: pair[1]}, 10, nil))
			if len(equal) != 1 || equal[0].ID != 2 {
				t.Fatalf("equality results = %v", equal)
			}
			atLeast := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.GreaterThanOrEqual, Property: "Value", Value: pair[1]}, 10, nil))
			if len(atLeast) != 1 || atLeast[0].ID != 2 {
				t.Fatalf("greater-than-or-equal results = %v", atLeast)
			}
		})
	}
	c := createCollection[int, record](t, nil)
	if err := c.Upsert(t.Context(), record{ID: 1, Value: math.NaN()}, record{ID: 2, Value: float64(0)}); err != nil {
		t.Fatal(err)
	}
	for _, operator := range []vectordata.FilterOperator{vectordata.Equal, vectordata.LessThan, vectordata.GreaterThan, vectordata.LessThanOrEqual, vectordata.GreaterThanOrEqual} {
		if got := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: operator, Property: "Value", Value: math.NaN()}, 10, nil)); len(got) != 0 {
			t.Fatalf("%s with NaN matched %v", operator, got)
		}
	}
	if err := c.Upsert(t.Context(), record{ID: 3, Value: "different type"}); err != nil {
		t.Fatal(err)
	}
	_ = sequenceError(t, c.GetFiltered(t.Context(), allRecords, 10, &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "Value"}}}))
	_ = sequenceError(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.GreaterThan, Property: "Value", Value: float64(0)}, 10, nil))
}

func TestGetFiltered_CompareMethods(t *testing.T) {
	type record struct {
		ID     int              `vectordata:"key"`
		Value  *comparisonValue `vectordata:"data"`
		Vector []float32        `vectordata:"vector,dimensions=1"`
	}
	c := createCollection[int, record](t, nil)
	if err := c.Upsert(t.Context(),
		record{ID: 3, Value: new(comparisonValue{rank: 3}), Vector: []float32{1}},
		record{ID: 1, Value: new(comparisonValue{rank: 1}), Vector: []float32{1}},
		record{ID: 0, Vector: []float32{1}},
		record{ID: 2, Value: new(comparisonValue{rank: 2}), Vector: []float32{1}},
	); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		descending bool
		ids        []int
	}{
		{"ascending", false, []int{0, 1, 2, 3}},
		{"descending", true, []int{3, 2, 1, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ids []int
			for _, value := range collect(t, c.GetFiltered(t.Context(), allRecords, 10, &vectordata.FilteredRecordRetrievalOptions{
				OrderBy: []vectordata.Sort{{Property: "Value", Descending: test.descending}},
			})) {
				ids = append(ids, value.ID)
			}
			if !slices.Equal(ids, test.ids) {
				t.Fatalf("ordered IDs = %v, want %v", ids, test.ids)
			}
		})
	}
	for _, test := range []struct {
		operator vectordata.FilterOperator
		ids      []int
	}{
		{vectordata.GreaterThan, []int{3}},
		{vectordata.GreaterThanOrEqual, []int{2, 3}},
		{vectordata.LessThan, []int{1}},
		{vectordata.LessThanOrEqual, []int{1, 2}},
	} {
		t.Run(string(test.operator), func(t *testing.T) {
			filter := vectordata.Filter{Operator: test.operator, Property: "Value", Value: comparisonValue{rank: 2}}
			var filteredIDs, searchIDs []int
			for _, value := range collect(t, c.GetFiltered(t.Context(), filter, 10, nil)) {
				filteredIDs = append(filteredIDs, value.ID)
			}
			for _, result := range collect(t, c.Search(t.Context(), []float32{1}, 10, &vectordata.SearchOptions{Filter: &filter})) {
				searchIDs = append(searchIDs, result.Record.ID)
			}
			for _, ids := range [][]int{filteredIDs, searchIDs} {
				slices.Sort(ids)
				if !slices.Equal(ids, test.ids) {
					t.Fatalf("filtered IDs = %v, want %v", ids, test.ids)
				}
			}
		})
	}
}

type (
	invalidCompareArgument struct{}
	invalidCompareResult   struct{}
	invalidCompareVariadic struct{}
	invalidCompareNoInput  struct{}
	invalidCompareNoOutput struct{}
	invalidCompareOutputs  struct{}
)

func (invalidCompareArgument) Compare(any) int                       { return 0 }
func (invalidCompareResult) Compare(invalidCompareResult) int64      { return 0 }
func (invalidCompareVariadic) Compare(...invalidCompareVariadic) int { return 0 }
func (invalidCompareNoInput) Compare() int                           { return 0 }
func (invalidCompareNoOutput) Compare(invalidCompareNoOutput)        {}
func (invalidCompareOutputs) Compare(invalidCompareOutputs) (int, error) {
	return 0, nil
}

func TestGetFiltered_InvalidCompareMethods(t *testing.T) {
	for _, value := range []any{
		invalidCompareArgument{},
		invalidCompareResult{},
		invalidCompareVariadic{},
		invalidCompareNoInput{},
		invalidCompareNoOutput{},
		invalidCompareOutputs{},
	} {
		t.Run(reflect.TypeOf(value).Name(), func(t *testing.T) {
			for _, typ := range []reflect.Type{reflect.TypeOf(value), reflect.TypeFor[any]()} {
				t.Run(typ.String(), func(t *testing.T) {
					c := createCollection[int, map[string]any](t, &vectordata.Definition{
						Properties: []vectordata.Property{
							&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[int]()},
							&vectordata.DataProperty{Name: "Value", Type: typ},
						},
					})
					if err := c.Upsert(t.Context(), map[string]any{"ID": 1, "Value": value}); err != nil {
						t.Fatal(err)
					}
					_ = sequenceError(t, c.GetFiltered(t.Context(), allRecords, 10, &vectordata.FilteredRecordRetrievalOptions{
						OrderBy: []vectordata.Sort{{Property: "Value"}},
					}))
					filter := vectordata.Filter{Operator: vectordata.GreaterThan, Property: "Value", Value: value}
					_ = sequenceError(t, c.GetFiltered(t.Context(), filter, 10, nil))
				})
			}
		})
	}
}

type equalityValue struct{ text string }

func (value equalityValue) Equal(other equalityValue) bool {
	return strings.EqualFold(value.text, other.text)
}

type foldedString string

func (value foldedString) Equal(other foldedString) bool {
	return strings.EqualFold(string(value), string(other))
}

type (
	invalidEqualArgument struct{ rank int }
	invalidEqualResult   struct{ rank int }
)

func (invalidEqualArgument) Equal(any) bool             { return true }
func (invalidEqualResult) Equal(invalidEqualResult) int { return 0 }

func TestGetFiltered_EqualityMethods(t *testing.T) {
	type record struct {
		ID    int `vectordata:"key"`
		Value any
	}
	instant := time.Unix(1, 0).UTC()
	for _, test := range []struct {
		name         string
		actual, want any
		equal        bool
	}{
		{"struct", equalityValue{"alpha"}, equalityValue{"ALPHA"}, true},
		{"pointer", new(equalityValue{"alpha"}), new(equalityValue{"ALPHA"}), true},
		{"primitive", foldedString("alpha"), foldedString("ALPHA"), true},
		{"different-value", equalityValue{"alpha"}, equalityValue{"beta"}, false},
		{"different-type", equalityValue{"alpha"}, foldedString("ALPHA"), false},
		{"nil", (*equalityValue)(nil), nil, true},
		{"nil-and-value", (*equalityValue)(nil), equalityValue{"alpha"}, false},
		{"time", instant, instant.In(time.FixedZone("offset", 3600)), true},
		{"invalid-argument", invalidEqualArgument{1}, invalidEqualArgument{2}, false},
		{"invalid-argument-equal", invalidEqualArgument{1}, invalidEqualArgument{1}, true},
		{"invalid-result", invalidEqualResult{1}, invalidEqualResult{1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := createCollection[int, record](t, nil)
			if err := c.Upsert(t.Context(), record{ID: 1, Value: test.actual}); err != nil {
				t.Fatal(err)
			}
			for _, operator := range []vectordata.FilterOperator{vectordata.Equal, vectordata.NotEqual} {
				got := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: operator, Property: "Value", Value: test.want}, 10, nil))
				wantMatch := test.equal
				if operator == vectordata.NotEqual {
					wantMatch = !wantMatch
				}
				if (len(got) == 1) != wantMatch {
					t.Fatalf("%s returned %v, want match = %v", operator, got, wantMatch)
				}
			}
		})
	}
}

func TestSearch_EqualityMethods(t *testing.T) {
	type record struct {
		ID     int             `vectordata:"key"`
		Value  *equalityValue  `vectordata:"data"`
		Values []equalityValue `vectordata:"data"`
		Vector []float32       `vectordata:"vector,dimensions=1"`
	}
	c := createCollection[int, record](t, nil)
	if err := c.Upsert(t.Context(),
		record{ID: 1, Value: new(equalityValue{"alpha"}), Values: []equalityValue{{"alpha"}}, Vector: []float32{1}},
		record{ID: 2, Value: new(equalityValue{"beta"}), Values: []equalityValue{{"beta"}}, Vector: []float32{1}},
		record{ID: 3, Vector: []float32{1}},
	); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		operator vectordata.FilterOperator
		property string
		value    any
		ids      []int
	}{
		{vectordata.Equal, "Value", equalityValue{"ALPHA"}, []int{1}},
		{vectordata.NotEqual, "Value", equalityValue{"ALPHA"}, []int{2, 3}},
		{vectordata.Contains, "Values", equalityValue{"ALPHA"}, []int{1}},
		{vectordata.In, "Value", []equalityValue{{"ALPHA"}}, []int{1}},
	} {
		t.Run(string(test.operator), func(t *testing.T) {
			filter := vectordata.Filter{Operator: test.operator, Property: test.property, Value: test.value}
			var filteredIDs, searchIDs []int
			for _, value := range collect(t, c.GetFiltered(t.Context(), filter, 10, nil)) {
				filteredIDs = append(filteredIDs, value.ID)
			}
			for _, result := range collect(t, c.Search(t.Context(), []float32{1}, 10, &vectordata.SearchOptions{Filter: &filter})) {
				searchIDs = append(searchIDs, result.Record.ID)
			}
			for _, ids := range [][]int{filteredIDs, searchIDs} {
				slices.Sort(ids)
				if !slices.Equal(ids, test.ids) {
					t.Fatalf("filtered IDs = %v, want %v", ids, test.ids)
				}
			}
		})
	}
}

type (
	recursiveFirst  *recursiveSecond
	recursiveSecond *recursiveFirst
)

func TestGetFiltered_CyclicValues(t *testing.T) {
	type record struct {
		ID    int `vectordata:"key"`
		Value any
	}
	var cycle any
	cycle = &cycle
	c := createCollection[int, record](t, nil)
	if err := c.Upsert(t.Context(), record{ID: 1, Value: cycle}); err != nil {
		t.Fatal(err)
	}
	got := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Equal, Property: "Value", Value: cycle}, 1, nil))
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("cyclic equality = %v", got)
	}
	_ = sequenceError(t, c.GetFiltered(t.Context(), allRecords, 1, &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "Value"}}}))
	type recursive struct {
		ID    int `vectordata:"key"`
		Value recursiveFirst
	}
	recursiveCollection := createCollection[int, recursive](t, nil)
	_ = sequenceError(t, recursiveCollection.GetFiltered(t.Context(), allRecords, 1, &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "Value"}}}))
}

func TestGetFiltered_NilCollectionsAndArrayMembership(t *testing.T) {
	type record struct {
		ID    int `vectordata:"key"`
		Slice []string
		Map   map[string]int
		Array [2]string
	}
	c := createCollection[int, record](t, nil)
	if err := c.Upsert(t.Context(), record{ID: 1, Array: [2]string{"a", "b"}}, record{ID: 2, Slice: []string{}, Map: map[string]int{}}); err != nil {
		t.Fatal(err)
	}
	for _, property := range []string{"Slice", "Map"} {
		got := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Equal, Property: property, Value: nil}, 2, nil))
		if len(got) != 1 || got[0].ID != 1 {
			t.Fatalf("nil %s equality = %v", property, got)
		}
	}
	got := collect(t, c.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Contains, Property: "Array", Value: "a"}, 2, nil))
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("array membership = %v", got)
	}
}

func TestIterators_LazyAndEarlyTermination(t *testing.T) {
	c, err := inmemory.NewCollection[string, document]("lazy", nil)
	if err != nil {
		t.Fatal(err)
	}
	search := c.Search(t.Context(), []float32{1, 0, 0, 0}, 10, nil)
	filtered := c.GetFiltered(t.Context(), allRecords, 10, nil)
	if err := c.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(t.Context(), document{ID: "one", Vector: []float32{1, 0, 0, 0}}, document{ID: "two", Vector: []float32{1, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	searchCalls, filteredCalls := 0, 0
	search(func(result vectordata.SearchResult[document], err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		searchCalls++
		if err := c.Upsert(t.Context(), result.Record); err != nil {
			t.Fatal(err)
		}
		return false
	})
	filtered(func(record document, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		filteredCalls++
		if err := c.Delete(t.Context(), record.ID); err != nil {
			t.Fatal(err)
		}
		return false
	})
	if searchCalls != 1 || filteredCalls != 1 {
		t.Fatalf("iterator calls = %d, %d", searchCalls, filteredCalls)
	}
}

func cancelAfterFirst[T any](t *testing.T, sequence func(context.Context) iter.Seq2[T, error]) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	values, failures := 0, 0
	for _, err := range sequence(ctx) {
		if err != nil {
			failures++
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("iterator error = %v", err)
			}
		} else {
			values++
			cancel()
		}
	}
	if values != 1 || failures != 1 {
		t.Fatalf("iterator yielded %d values and %d errors", values, failures)
	}
}

func TestIterators_CancellationBetweenResults(t *testing.T) {
	c := createCollection[string, document](t, nil)
	if err := c.Upsert(t.Context(), document{ID: "one", Vector: []float32{1, 0, 0, 0}}, document{ID: "two", Vector: []float32{1, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	cancelAfterFirst(t, func(ctx context.Context) iter.Seq2[vectordata.SearchResult[document], error] {
		return c.Search(ctx, []float32{1, 0, 0, 0}, 2, nil)
	})
	cancelAfterFirst(t, func(ctx context.Context) iter.Seq2[document, error] {
		return c.GetFiltered(ctx, allRecords, 2, nil)
	})
	store := inmemory.New(inmemory.Config{})
	for _, name := range []string{"one", "two"} {
		collection, err := vectordata.GetCollection[string, document](store.Store, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := collection.EnsureExists(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	cancelAfterFirst(t, store.ListCollectionNames)
}
