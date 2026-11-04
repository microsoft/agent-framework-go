// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"fmt"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

func TestCollection_FilterTranslation(t *testing.T) {
	instant := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	key := uuid.MustParse("d282e2e0-73ce-4b91-8973-b6bc418e31fa")
	for _, test := range []struct {
		name   string
		filter vectordata.Filter
		want   string
	}{
		{"quoted string", vectordata.Filter{Operator: vectordata.Equal, Property: "Text", Value: "O'Reilly"}, "(text eq 'O''Reilly')"},
		{"not equal", vectordata.Filter{Operator: vectordata.NotEqual, Property: "Rank", Value: 3}, "(rank ne 3)"},
		{"greater", vectordata.Filter{Operator: vectordata.GreaterThan, Property: "Rank", Value: 3}, "(rank gt 3)"},
		{"greater equal", vectordata.Filter{Operator: vectordata.GreaterThanOrEqual, Property: "Rank", Value: 3}, "(rank ge 3)"},
		{"less", vectordata.Filter{Operator: vectordata.LessThan, Property: "Rank", Value: 3}, "(rank lt 3)"},
		{"less equal", vectordata.Filter{Operator: vectordata.LessThanOrEqual, Property: "Rank", Value: 3}, "(rank le 3)"},
		{"null", vectordata.Filter{Operator: vectordata.Equal, Property: "Text", Value: nil}, "(text eq null)"},
		{"typed nil", vectordata.Filter{Operator: vectordata.Equal, Property: "Text", Value: (*string)(nil)}, "(text eq null)"},
		{"pointer", vectordata.Filter{Operator: vectordata.Equal, Property: "Rank", Value: new(3)}, "(rank eq 3)"},
		{"contains", vectordata.Filter{Operator: vectordata.Contains, Property: "Tags", Value: "a'b"}, "tags/any(t: t eq 'a''b')"},
		{"in", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{"one", "two"}}, "search.in(text, 'one, two')"},
		{"in pipe delimiter", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{"one two", "O'Reilly"}}, "search.in(text, 'one two|O''Reilly', '|')"},
		{"in default pipe value", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{"a|b", "c"}}, "search.in(text, 'a|b, c')"},
		{"empty in", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{}}, "false"},
		{"nil in", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string(nil)}, "false"},
		{"in empty string", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{""}}, "(text eq '')"},
		{"in repeated empty strings", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{"", ""}}, "(text eq '')"},
		{"in empty and nonempty strings", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{"", "one", ""}}, "(text eq '' or search.in(text, 'one'))"},
		{"in empty and delimited strings", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []any{"one two", "", "O'Reilly"}}, "(text eq '' or search.in(text, 'one two|O''Reilly', '|'))"},
		{"not in empty and nonempty strings", vectordata.Filter{Operator: vectordata.Not, Operands: []vectordata.Filter{
			{Operator: vectordata.In, Property: "Text", Value: []string{"", "one"}},
		}}, "(not (text eq '' or search.in(text, 'one')))"},
		{"date", vectordata.Filter{Operator: vectordata.Equal, Property: "When", Value: instant}, "(when eq 2026-01-02T03:04:05Z)"},
		{"uuid", vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: key}, "(id eq 'd282e2e0-73ce-4b91-8973-b6bc418e31fa')"},
		{"bool", vectordata.Filter{Operator: vectordata.Equal, Property: "Active", Value: true}, "(active eq true)"},
		{"float", vectordata.Filter{Operator: vectordata.Equal, Property: "Number", Value: float32(1.5)}, "(number eq 1.5)"},
		{"byte", vectordata.Filter{Operator: vectordata.Equal, Property: "Number", Value: byte(3)}, "(number eq 3)"},
		{"infinity", vectordata.Filter{Operator: vectordata.Equal, Property: "Number", Value: math.Inf(1)}, "(number eq INF)"},
		{"negative infinity", vectordata.Filter{Operator: vectordata.Equal, Property: "Number", Value: math.Inf(-1)}, "(number eq -INF)"},
		{"and", vectordata.Filter{Operator: vectordata.And, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Text", Value: "a"},
			{Operator: vectordata.GreaterThan, Property: "Rank", Value: 2},
		}}, "((text eq 'a') and (rank gt 2))"},
		{"or", vectordata.Filter{Operator: vectordata.Or, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Text", Value: "a"},
			{Operator: vectordata.Equal, Property: "Text", Value: "b"},
		}}, "((text eq 'a') or (text eq 'b'))"},
		{"not", vectordata.Filter{Operator: vectordata.Not, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Text", Value: "a"},
		}}, "(not (text eq 'a'))"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]any
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				body = jsonBody(t, request)
				return response(http.StatusOK, `{"value":[]}`), nil
			}, azureprovider.AISearchConfig{})
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", StorageName: "id", Type: reflect.TypeFor[string]()},
				&vectordata.DataProperty{Name: "Text", StorageName: "text", Type: reflect.TypeFor[string](), IsIndexed: true},
				&vectordata.DataProperty{Name: "Rank", StorageName: "rank", Type: reflect.TypeFor[int32](), IsIndexed: true},
				&vectordata.DataProperty{Name: "Tags", StorageName: "tags", Type: reflect.TypeFor[[]string](), IsIndexed: true},
				&vectordata.DataProperty{Name: "When", StorageName: "when", Type: reflect.TypeFor[time.Time](), IsIndexed: true},
				&vectordata.DataProperty{Name: "Active", StorageName: "active", Type: reflect.TypeFor[bool](), IsIndexed: true},
				&vectordata.DataProperty{Name: "Number", StorageName: "number", Type: reflect.TypeFor[float64](), IsIndexed: true},
			}}
			collection := openCollection[string, map[string]any](t, store, definition)
			collect(t, collection.GetFiltered(t.Context(), test.filter, 5, &vectordata.FilteredRecordRetrievalOptions{
				Skip: 2, OrderBy: []vectordata.Sort{{Property: "Rank", Descending: true}, {Property: "Text"}, {Property: "ID"}},
			}))
			if body["filter"] != test.want || body["top"] != float64(5) || body["skip"] != float64(2) ||
				body["orderby"] != "rank desc,text,id" {
				t.Fatalf("filtered search = %v; want filter %s", body, test.want)
			}
		})
	}
}

func TestCollection_RejectsUnsupportedFilters(t *testing.T) {
	var cycle any
	cycle = &cycle
	for _, filter := range []vectordata.Filter{
		{Operator: vectordata.Equal, Property: "Unknown", Value: "a"},
		{Operator: vectordata.Equal, Property: "Vector", Value: "a"},
		{Operator: vectordata.In, Property: "Rank", Value: []int{1, 2}},
		{Operator: vectordata.In, Property: "Text", Value: []any{"a", 2}},
		{Operator: vectordata.In, Property: "Text", Value: []string{"a b", "c|d"}},
		{Operator: vectordata.In, Property: "Text", Value: []any{"", 2}},
		{Operator: vectordata.In, Property: "Text", Value: []string{"", "a b", "c|d"}},
		{Operator: vectordata.Contains, Property: "Text", Value: "a"},
		{Operator: vectordata.Equal, Property: "Tags", Value: "a"},
		{Operator: vectordata.Equal, Property: "Text", Value: map[string]any{"bad": "value"}},
		{Operator: vectordata.Equal, Property: "Text", Value: cycle},
		{Operator: vectordata.And},
		{Operator: "unknown", Property: "Text", Value: "a"},
	} {
		t.Run(fmt.Sprint(filter.Operator)+"/"+filter.Property, func(t *testing.T) {
			calls := 0
			store := newStore(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(http.StatusOK, `{"value":[]}`), nil
			}, azureprovider.AISearchConfig{})
			collection := openCollection[string, document](t, store, nil)
			_ = sequenceError(t, collection.GetFiltered(t.Context(), filter, 1, nil))
			if calls != 0 {
				t.Fatal("unsupported filter reached the service")
			}
		})
	}
}
