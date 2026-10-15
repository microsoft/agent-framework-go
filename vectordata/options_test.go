// Copyright (c) Microsoft. All rights reserved.

package vectordata_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/vectordata"
)

func TestOptions_DefaultsAndValidation(t *testing.T) {
	for _, skip := range []int{-1, 0, 3} {
		search := &vectordata.SearchOptions{Skip: skip, ScoreThreshold: new(0.0)}
		hybrid := &vectordata.HybridSearchOptions{Skip: skip, ScoreThreshold: new(0.0)}
		get := &vectordata.FilteredRecordRetrievalOptions{Skip: skip}
		for _, err := range []error{search.Validate(), hybrid.Validate(), get.Validate()} {
			if (err != nil) != (skip < 0) {
				t.Fatalf("skip %d: %v", skip, err)
			}
		}
		if search.Skip != skip || hybrid.Skip != skip || get.Skip != skip ||
			*search.ScoreThreshold != 0 || *hybrid.ScoreThreshold != 0 {
			t.Fatal("validation modified options")
		}
	}
	if (*vectordata.SearchOptions)(nil).Validate() != nil ||
		(*vectordata.HybridSearchOptions)(nil).Validate() != nil ||
		(*vectordata.FilteredRecordRetrievalOptions)(nil).Validate() != nil {
		t.Fatal("nil options should use defaults")
	}
	if (vectordata.RecordRetrievalOptions{}).IncludeVectors ||
		(vectordata.SearchOptions{}).IncludeVectors || (vectordata.HybridSearchOptions{}).IncludeVectors {
		t.Fatal("vectors should be excluded by default")
	}
	sorts := []vectordata.Sort{{Property: "Group"}, {Property: "Rating", Descending: true}}
	options := &vectordata.FilteredRecordRetrievalOptions{OrderBy: sorts}
	if err := options.Validate(); err != nil || !reflect.DeepEqual(options.OrderBy, sorts) {
		t.Fatalf("sort validation = %v", err)
	}
	options.OrderBy = []vectordata.Sort{{}}
	if err := options.Validate(); err == nil {
		t.Fatal("empty sort property was accepted")
	}
	filter := &vectordata.Filter{Operator: "invalid"}
	if (&vectordata.SearchOptions{Filter: filter}).Validate() == nil ||
		(&vectordata.HybridSearchOptions{Filter: filter}).Validate() == nil {
		t.Fatal("invalid filter was ignored")
	}
}

func TestFilter_ValidPredicates(t *testing.T) {
	for _, operator := range []vectordata.FilterOperator{
		vectordata.Equal, vectordata.NotEqual, vectordata.GreaterThan,
		vectordata.GreaterThanOrEqual, vectordata.LessThan, vectordata.LessThanOrEqual,
		vectordata.Contains, vectordata.In,
	} {
		filter := vectordata.Filter{Operator: operator, Property: "Value", Value: 42}
		if operator == vectordata.In {
			filter.Value = []int{1, 2}
		}
		before := filter
		if err := filter.Validate(); err != nil || !reflect.DeepEqual(filter, before) {
			t.Fatalf("%s = %v", operator, err)
		}
	}
	equal := vectordata.Filter{Operator: vectordata.Equal, Property: "Optional"}
	for _, operator := range []vectordata.FilterOperator{vectordata.And, vectordata.Or} {
		filter := vectordata.Filter{Operator: operator, Operands: []vectordata.Filter{equal, equal}}
		if err := filter.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	not := &vectordata.Filter{Operator: vectordata.Not, Operands: []vectordata.Filter{equal}}
	if err := not.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, values := range []any{[]int(nil), []string{}, [2]int{1, 2}} {
		if err := (&vectordata.Filter{Operator: vectordata.In, Property: "ID", Value: values}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (*vectordata.Filter)(nil).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFilter_InvalidPredicates(t *testing.T) {
	equal := vectordata.Filter{Operator: vectordata.Equal, Property: "Value", Value: 1}
	for _, filter := range []vectordata.Filter{
		{},
		{Operator: "custom", Property: "ID"},
		{Operator: vectordata.Equal},
		{Operator: vectordata.Equal, Property: "ID", Operands: []vectordata.Filter{equal}},
		{Operator: vectordata.And},
		{Operator: vectordata.Or, Operands: []vectordata.Filter{equal}},
		{Operator: vectordata.Not},
		{Operator: vectordata.Not, Operands: []vectordata.Filter{equal, equal}},
		{Operator: vectordata.Not, Property: "ID", Operands: []vectordata.Filter{equal}},
		{Operator: vectordata.Not, Value: true, Operands: []vectordata.Filter{equal}},
		{Operator: vectordata.And, Operands: []vectordata.Filter{equal, {}}},
		{Operator: vectordata.In, Property: "ID"},
		{Operator: vectordata.In, Property: "ID", Value: "not a list"},
	} {
		if err := filter.Validate(); err == nil {
			t.Fatalf("invalid filter accepted: %+v", filter)
		}
	}
	operands := make([]vectordata.Filter, 2)
	operands[0] = vectordata.Filter{Operator: vectordata.And, Operands: operands}
	operands[1] = equal
	if err := operands[0].Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cyclic filter = %v", err)
	}
}

func TestSearchResult_ScorePresence(t *testing.T) {
	for _, score := range []*float64{nil, new(0.0), new(0.75)} {
		want := vectordata.SearchResult[record]{Record: record{ID: "first"}, Score: score}
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var result vectordata.SearchResult[record]
		if err := json.Unmarshal(data, &result); err != nil || !reflect.DeepEqual(result, want) {
			t.Fatalf("result = %+v, %v; want %+v", result, err, want)
		}
	}
}

func TestError_MetadataAndCause(t *testing.T) {
	for _, test := range []struct {
		err  vectordata.Error
		want string
	}{
		{vectordata.Error{}, "vectordata: vector store operation failed"},
		{vectordata.Error{Message: "failed"}, "vectordata: failed"},
		{vectordata.Error{Err: context.Canceled}, "vectordata: context canceled"},
		{vectordata.Error{OperationName: "upsert", Message: "failed", Err: context.Canceled}, "vectordata: upsert: failed: context canceled"},
	} {
		if got := test.err.Error(); got != test.want {
			t.Fatalf("Error = %q, want %q", got, test.want)
		}
		if test.err.Unwrap() != test.err.Err {
			t.Fatal("underlying error changed")
		}
	}
	failure := &vectordata.Error{
		SystemName: "test", StoreName: "database", CollectionName: "records",
		OperationName: "get", Err: context.Canceled,
	}
	var found *vectordata.Error
	if !errors.Is(failure, context.Canceled) || !errors.As(failure, &found) || found != failure ||
		found.SystemName != "test" || found.StoreName != "database" || found.CollectionName != "records" {
		t.Fatalf("error chain lost metadata: %+v", found)
	}
}
