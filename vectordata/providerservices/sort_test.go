// Copyright (c) Microsoft. All rights reserved.

package providerservices_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

func recordSequence[R any](records ...R) iter.Seq2[R, error] {
	return func(yield func(R, error) bool) {
		for _, record := range records {
			if !yield(record, nil) {
				return
			}
		}
	}
}

func TestSortRecords_StableOrderingAndCachedKeys(t *testing.T) {
	type record struct {
		ID    string
		Group int
		Value *int
	}
	records := []*record{
		{"a", 2, new(1)},
		{"b", 1, nil},
		{"c", 1, new(2)},
		{"d", 1, new(2)},
		{"e", 1, new(1)},
		{"f", 2, new(3)},
	}
	original := slices.Clone(records)
	want := []*record{records[2], records[3], records[4], records[1], records[5], records[0]}
	var resolutions []string
	reads := make(map[string]int)
	var current *record
	resolve := func(name string) (providerservices.FilterProperty, error) {
		resolutions = append(resolutions, name)
		typ := reflect.TypeFor[int]()
		if name == "Value" {
			typ = reflect.TypeFor[*int]()
		}
		return providerservices.FilterProperty{
			Type: typ,
			Value: func(input any) (any, bool, error) {
				value := input.(*record)
				if value != current {
					t.Fatal("sort keys were not read during record enumeration")
				}
				reads[value.ID+"."+name]++
				if name == "Value" {
					return value.Value, true, nil
				}
				return value.Group, true, nil
			},
		}, nil
	}
	source := func(yield func(*record, error) bool) {
		if !slices.Equal(resolutions, []string{"Group", "Value"}) {
			t.Fatalf("properties resolved before enumeration = %v", resolutions)
		}
		for _, value := range records {
			current = value
			if !yield(value, nil) {
				return
			}
		}
		current = nil
	}
	result, err := providerservices.SortRecords(source, []vectordata.Sort{
		{Property: "Group"}, {Property: "Value", Descending: true},
	}, resolve)
	if err != nil || !slices.Equal(result, want) {
		t.Fatalf("sorted records = %v, %v; want %v", result, err, want)
	}
	if !slices.Equal(records, original) || len(resolutions) != 2 {
		t.Fatal("sorting mutated the input slice or resolved properties repeatedly")
	}
	for _, value := range records {
		for _, name := range []string{"Group", "Value"} {
			if count := reads[value.ID+"."+name]; count != 1 {
				t.Fatalf("%s.%s read %d times", value.ID, name, count)
			}
		}
	}
}

func TestSortRecords_NoOrdering(t *testing.T) {
	records := []string{"second", "first", "third"}
	result, err := providerservices.SortRecords(recordSequence(records...), nil, nil)
	if err != nil || !slices.Equal(result, records) {
		t.Fatalf("unordered records = %v, %v", result, err)
	}
	result[0] = "changed"
	if records[0] != "second" {
		t.Fatal("result slice aliases the input slice")
	}
	result, err = providerservices.SortRecords(recordSequence[string](), nil, nil)
	if err != nil || len(result) != 0 {
		t.Fatalf("empty records = %v, %v", result, err)
	}
}

func TestSortRecords_MissingAndDynamicValues(t *testing.T) {
	records := []map[string]any{
		{"ID": "present", "Value": 2},
		{"ID": "missing"},
		{"ID": "nil", "Value": (*int)(nil)},
		{"ID": "one", "Value": new(new(1))},
	}
	resolve := func(name string) (providerservices.FilterProperty, error) {
		return providerservices.FilterProperty{
			Type: reflect.TypeFor[any](),
			Value: func(record any) (any, bool, error) {
				value, found := record.(map[string]any)[name]
				return value, found, nil
			},
		}, nil
	}
	result, err := providerservices.SortRecords(recordSequence(records...), []vectordata.Sort{{Property: "Value"}}, resolve)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, record := range result {
		ids = append(ids, record["ID"].(string))
	}
	if !slices.Equal(ids, []string{"missing", "nil", "one", "present"}) {
		t.Fatalf("ordered IDs = %v", ids)
	}
	for _, values := range [][]any{{1, "incompatible"}, {struct{}{}}} {
		input := make([]map[string]any, len(values))
		for i, value := range values {
			input[i] = map[string]any{"Value": value}
		}
		if result, err := providerservices.SortRecords(recordSequence(input...), []vectordata.Sort{{Property: "Value"}}, resolve); err == nil || result != nil {
			t.Fatalf("invalid dynamic ordering = %v, %v", result, err)
		}
	}
}

func TestSortRecords_InvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		name       string
		typ        reflect.Type
		noIterator bool
		noResolver bool
		noReader   bool
		noProperty bool
	}{
		{"nil iterator", reflect.TypeFor[int](), true, false, false, false},
		{"nil resolver", reflect.TypeFor[int](), false, true, false, false},
		{"nil type", nil, false, false, false, false},
		{"nil reader", reflect.TypeFor[int](), false, false, true, false},
		{"empty property", reflect.TypeFor[int](), false, false, false, true},
		{"unordered type", reflect.TypeFor[[]int](), false, false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := func(func(int, error) bool) { t.Fatal("invalid sort enumerated records") }
			if test.noIterator {
				source = nil
			}
			resolve := func(string) (providerservices.FilterProperty, error) {
				property := providerservices.FilterProperty{Type: test.typ}
				if !test.noReader {
					property.Value = func(any) (any, bool, error) {
						t.Fatal("invalid sort read a property")
						return nil, false, nil
					}
				}
				return property, nil
			}
			if test.noResolver {
				resolve = nil
			}
			orderBy := []vectordata.Sort{{Property: "Value"}}
			if test.noProperty {
				orderBy[0].Property = ""
			}
			if result, err := providerservices.SortRecords(source, orderBy, resolve); err == nil || result != nil {
				t.Fatalf("invalid sort = %v, %v", result, err)
			}
		})
	}
}

func TestSortRecords_ErrorPropagationAndEarlyStop(t *testing.T) {
	for _, stage := range []string{"resolver", "iterator", "reader"} {
		t.Run(stage, func(t *testing.T) {
			want := errors.New(stage + " failed")
			yielded, reads := 0, 0
			closed := false
			source := func(yield func(int, error) bool) {
				defer func() { closed = true }()
				for value := range 3 {
					yielded++
					if stage == "iterator" && value == 1 {
						yield(0, want)
						return
					}
					if !yield(value, nil) {
						return
					}
				}
			}
			resolve := func(string) (providerservices.FilterProperty, error) {
				if stage == "resolver" {
					return providerservices.FilterProperty{}, want
				}
				return providerservices.FilterProperty{
					Type: reflect.TypeFor[int](),
					Value: func(record any) (any, bool, error) {
						reads++
						if stage == "reader" {
							return nil, false, want
						}
						return record, true, nil
					},
				}, nil
			}
			result, err := providerservices.SortRecords(source, []vectordata.Sort{{Property: "Value"}}, resolve)
			if !errors.Is(err, want) || result != nil {
				t.Fatalf("sort failure = %v, %v", result, err)
			}
			wantYielded, wantReads := 1, 1
			switch stage {
			case "iterator":
				wantYielded = 2
			case "resolver":
				wantYielded, wantReads = 0, 0
			}
			if yielded != wantYielded || reads != wantReads || closed != (stage != "resolver") {
				t.Fatalf("yielded=%d, reads=%d, closed=%v", yielded, reads, closed)
			}
		})
	}
}

func TestSortRecords_IteratorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reads := 0
	source := func(yield func(int, error) bool) {
		for value := range 3 {
			if err := ctx.Err(); err != nil {
				yield(0, err)
				return
			}
			if !yield(value, nil) {
				return
			}
		}
	}
	result, err := providerservices.SortRecords(source, []vectordata.Sort{{Property: "Value"}}, func(name string) (providerservices.FilterProperty, error) {
		if name != "Value" {
			return providerservices.FilterProperty{}, fmt.Errorf("unknown property %q", name)
		}
		return providerservices.FilterProperty{
			Type: reflect.TypeFor[int](),
			Value: func(record any) (any, bool, error) {
				reads++
				cancel()
				return record, true, nil
			},
		}, nil
	})
	if !errors.Is(err, context.Canceled) || result != nil || reads != 1 {
		t.Fatalf("canceled sorting = %v, %v; reads=%d", result, err, reads)
	}
}
