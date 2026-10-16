// Copyright (c) Microsoft. All rights reserved.

package providerservices_test

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

func TestCompileFilter_Nil(t *testing.T) {
	predicate, err := providerservices.CompileFilter(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []any{nil, struct{}{}, map[string]any{}} {
		if matches, err := predicate(record); err != nil || !matches {
			t.Fatalf("predicate(%T) = %v, %v", record, matches, err)
		}
	}
}

func TestCompileFilter_LogicalEvaluation(t *testing.T) {
	readErr := errors.New("read failed")
	for _, test := range []struct {
		name         string
		operator     vectordata.FilterOperator
		first        bool
		second       bool
		secondError  bool
		want         bool
		wantError    bool
		wantReadings []string
	}{
		{"and short circuit", vectordata.And, false, false, true, false, false, []string{"first"}},
		{"and match", vectordata.And, true, true, false, true, false, []string{"first", "second"}},
		{"and no match", vectordata.And, true, false, false, false, false, []string{"first", "second"}},
		{"and error", vectordata.And, true, false, true, false, true, []string{"first", "second"}},
		{"or short circuit", vectordata.Or, true, false, true, true, false, []string{"first"}},
		{"or match", vectordata.Or, false, true, false, true, false, []string{"first", "second"}},
		{"or no match", vectordata.Or, false, false, false, false, false, []string{"first", "second"}},
		{"or error", vectordata.Or, false, false, true, false, true, []string{"first", "second"}},
		{"not match", vectordata.Not, false, false, false, true, false, []string{"first"}},
		{"not no match", vectordata.Not, true, false, false, false, false, []string{"first"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			filter := &vectordata.Filter{Operator: test.operator, Operands: []vectordata.Filter{
				{Operator: vectordata.Equal, Property: "first", Value: true},
			}}
			if test.operator != vectordata.Not {
				filter.Operands = append(filter.Operands, vectordata.Filter{Operator: vectordata.Equal, Property: "second", Value: true})
			}
			var resolutions, readings []string
			predicate, err := providerservices.CompileFilter(filter, func(name string) (providerservices.FilterProperty, error) {
				resolutions = append(resolutions, name)
				return providerservices.FilterProperty{
					Type: reflect.TypeFor[bool](),
					Value: func(record any) (any, bool, error) {
						readings = append(readings, name)
						if name == "second" && test.secondError {
							return nil, false, readErr
						}
						value, found := record.(map[string]bool)[name]
						return value, found, nil
					},
				}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			wantResolutions := []string{"first"}
			if test.operator != vectordata.Not {
				wantResolutions = append(wantResolutions, "second")
			}
			if !slices.Equal(resolutions, wantResolutions) || len(readings) != 0 {
				t.Fatalf("compilation resolved %v and read %v", resolutions, readings)
			}
			for range 2 {
				readings = nil
				matches, err := predicate(map[string]bool{"first": test.first, "second": test.second})
				if matches != test.want || (err != nil) != test.wantError || test.wantError && !errors.Is(err, readErr) {
					t.Fatalf("predicate = %v, %v; want %v, error=%v", matches, err, test.want, test.wantError)
				}
				if !slices.Equal(readings, test.wantReadings) || !slices.Equal(resolutions, wantResolutions) {
					t.Fatalf("evaluation resolved %v and read %v", resolutions, readings)
				}
			}
		})
	}
}

type filterText string

func (text filterText) Equal(other filterText) bool {
	return strings.EqualFold(string(text), string(other))
}

type filterRank int

func (rank filterRank) Compare(other filterRank) int {
	return 7 * cmp.Compare(other, rank)
}

type comparisonSlice []int

func (value comparisonSlice) Compare(other comparisonSlice) int {
	if value == nil || other == nil {
		panic("comparison method called with nil")
	}
	return cmp.Compare(len(value), len(other))
}

type comparisonMap map[string]int

func (value comparisonMap) Compare(other comparisonMap) int {
	if value == nil || other == nil {
		panic("comparison method called with nil")
	}
	return cmp.Compare(len(value), len(other))
}

func TestCompileFilter_NilOrderedValues(t *testing.T) {
	for _, test := range []struct {
		name            string
		nilValue, value any
	}{
		{"slice", comparisonSlice(nil), comparisonSlice{1}},
		{"map", comparisonMap(nil), comparisonMap{"one": 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, operator := range []vectordata.FilterOperator{
				vectordata.LessThan, vectordata.LessThanOrEqual,
				vectordata.GreaterThan, vectordata.GreaterThanOrEqual,
			} {
				t.Run(string(operator), func(t *testing.T) {
					for _, operands := range []struct {
						name          string
						record, value any
					}{
						{"nil record", test.nilValue, test.value},
						{"nil literal", test.value, test.nilValue},
						{"both nil", test.nilValue, test.nilValue},
					} {
						t.Run(operands.name, func(t *testing.T) {
							predicate, err := providerservices.CompileFilter(&vectordata.Filter{
								Operator: operator, Property: "Value", Value: operands.value,
							}, func(string) (providerservices.FilterProperty, error) {
								return providerservices.FilterProperty{
									Type:  reflect.TypeOf(test.value),
									Value: func(record any) (any, bool, error) { return record, true, nil },
								}, nil
							})
							if err != nil {
								t.Fatal(err)
							}
							if matches, err := predicate(operands.record); err != nil || matches {
								t.Fatalf("nil relational comparison = %v, %v", matches, err)
							}
						})
					}
				})
			}
		})
	}
}

func TestCompileFilter_Comparisons(t *testing.T) {
	for _, test := range []struct {
		name          string
		operator      vectordata.FilterOperator
		record, value any
		want          bool
	}{
		{"equal", vectordata.Equal, 1, 1, true},
		{"not equal", vectordata.NotEqual, 1, 2, true},
		{"equal method", vectordata.Equal, filterText("alpha"), filterText("ALPHA"), true},
		{"not equal method", vectordata.NotEqual, filterText("alpha"), filterText("ALPHA"), false},
		{"equal pointers", vectordata.Equal, new(filterText("alpha")), new(filterText("ALPHA")), true},
		{"nil slice", vectordata.Equal, []int(nil), nil, true},
		{"non-nil slice", vectordata.Equal, []int{}, nil, false},
		{"nil pointer", vectordata.Equal, (*int)(nil), nil, true},
		{"contains", vectordata.Contains, []string{"a", "b"}, "b", true},
		{"contains method", vectordata.Contains, []filterText{"alpha"}, filterText("ALPHA"), true},
		{"contains array", vectordata.Contains, [2]int{1, 2}, 2, true},
		{"contains pointer", vectordata.Contains, new([]int{1, 2}), 2, true},
		{"contains nil pointer", vectordata.Contains, (*[]int)(nil), 2, false},
		{"contains nil slice", vectordata.Contains, []int(nil), 2, false},
		{"in", vectordata.In, 2, []int{1, 2}, true},
		{"in method", vectordata.In, filterText("alpha"), []filterText{"ALPHA"}, true},
		{"in empty", vectordata.In, 2, []int{}, false},
		{"in array", vectordata.In, 2, [2]int{1, 2}, true},
		{"greater", vectordata.GreaterThan, 2, 1, true},
		{"greater equal", vectordata.GreaterThanOrEqual, new(2), 2, true},
		{"less", vectordata.LessThan, 1, new(2), true},
		{"less equal", vectordata.LessThanOrEqual, 2, 2, true},
		{"compare method", vectordata.LessThan, filterRank(2), filterRank(1), true},
		{"nil relational", vectordata.LessThan, (*int)(nil), 1, false},
		{"nil operand", vectordata.GreaterThan, 1, nil, false},
		{"nan record", vectordata.LessThan, math.NaN(), float64(1), false},
		{"nan operand", vectordata.GreaterThanOrEqual, float64(1), math.NaN(), false},
		{"time", vectordata.LessThan, time.Unix(1, 0), time.Unix(2, 0), true},
		{"uuid", vectordata.LessThan, uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			filter := &vectordata.Filter{Operator: test.operator, Property: "Value", Value: test.value}
			predicate, err := providerservices.CompileFilter(filter, func(name string) (providerservices.FilterProperty, error) {
				if name != "Value" {
					return providerservices.FilterProperty{}, fmt.Errorf("unknown property %q", name)
				}
				return providerservices.FilterProperty{
					Type:  reflect.TypeOf(test.record),
					Value: func(record any) (any, bool, error) { return record, true, nil },
				}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if matches, err := predicate(test.record); err != nil || matches != test.want {
				t.Fatalf("predicate = %v, %v; want %v", matches, err, test.want)
			}
		})
	}
}

func TestCompileFilter_ModelAccessors(t *testing.T) {
	options := vectordata.CollectionOptions{Definition: &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
		&vectordata.DataProperty{Name: "Text", Type: reflect.TypeFor[string](), StorageName: "text"},
	}}}
	for _, test := range []struct {
		name   string
		build  func(vectordata.CollectionOptions) (*providerservices.CollectionModel, error)
		record any
	}{
		{"struct", buildModel[string, taggedRecord], taggedRecord{Text: "value"}},
		{"pointer", buildModel[string, *taggedRecord], &taggedRecord{Text: "value"}},
		{"map", buildModel[string, map[string]any], map[string]any{"Text": "value", "text": "storage"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, err := test.build(options)
			if err != nil {
				t.Fatal(err)
			}
			properties := make(map[string]providerservices.FilterProperty)
			for _, property := range model.Properties {
				if data, ok := property.(*providerservices.DataPropertyModel); ok {
					properties[data.Name] = providerservices.FilterProperty{Type: data.Type, Value: data.Value}
				}
			}
			resolve := func(name string) (providerservices.FilterProperty, error) {
				property, found := properties[name]
				if !found {
					return providerservices.FilterProperty{}, fmt.Errorf("property %q was not found", name)
				}
				return property, nil
			}
			predicate, err := providerservices.CompileFilter(&vectordata.Filter{
				Operator: vectordata.Equal, Property: "Text", Value: "value",
			}, resolve)
			if err != nil {
				t.Fatal(err)
			}
			if matches, err := predicate(test.record); err != nil || !matches {
				t.Fatalf("predicate = %v, %v", matches, err)
			}
			if matches, err := predicate(struct{}{}); err == nil || matches {
				t.Fatalf("invalid record = %v, %v", matches, err)
			}
			for _, name := range []string{"text", "Other"} {
				if _, err := providerservices.CompileFilter(&vectordata.Filter{
					Operator: vectordata.Equal, Property: name, Value: "value",
				}, resolve); err == nil {
					t.Fatalf("accepted unavailable property %q", name)
				}
			}
		})
	}
}

func TestCompileFilter_MissingAndDynamicValues(t *testing.T) {
	resolve := func(name string) (providerservices.FilterProperty, error) {
		return providerservices.FilterProperty{
			Type: reflect.TypeFor[any](),
			Value: func(record any) (any, bool, error) {
				value, found := record.(map[string]any)[name]
				return value, found, nil
			},
		}, nil
	}
	predicate, err := providerservices.CompileFilter(&vectordata.Filter{
		Operator: vectordata.Equal, Property: "Value", Value: nil,
	}, resolve)
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{}
	for _, input := range []map[string]any{record, {"Value": nil}, {"Value": (*string)(nil)}} {
		if matches, err := predicate(input); err != nil || !matches {
			t.Fatalf("nil or missing = %v, %v", matches, err)
		}
	}
	record["Value"] = "present"
	if matches, err := predicate(record); err != nil || matches {
		t.Fatalf("updated record = %v, %v", matches, err)
	}
	predicate, err = providerservices.CompileFilter(&vectordata.Filter{
		Operator: vectordata.GreaterThan, Property: "Value", Value: 1,
	}, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if matches, err := predicate(record); err == nil || matches {
		t.Fatalf("incompatible dynamic comparison = %v, %v", matches, err)
	}
	record["Value"] = 2
	if matches, err := predicate(record); err != nil || !matches {
		t.Fatalf("compatible dynamic comparison = %v, %v", matches, err)
	}
}

func TestCompileFilter_InvalidConfiguration(t *testing.T) {
	valid := &vectordata.Filter{Operator: vectordata.Equal, Property: "Value", Value: 1}
	cyclic := &vectordata.Filter{Operator: vectordata.Not, Operands: make([]vectordata.Filter, 1)}
	cyclic.Operands[0] = *cyclic
	for _, test := range []struct {
		name       string
		filter     *vectordata.Filter
		typ        reflect.Type
		noResolver bool
		noReader   bool
	}{
		{"no resolver", valid, reflect.TypeFor[int](), true, false},
		{"no type", valid, nil, false, false},
		{"no reader", valid, reflect.TypeFor[int](), false, true},
		{"invalid operator", &vectordata.Filter{Operator: "unknown", Property: "Value"}, nil, false, false},
		{"invalid operands", &vectordata.Filter{Operator: vectordata.Not}, nil, false, false},
		{"cyclic filter", cyclic, nil, false, false},
		{"incompatible value", valid, reflect.TypeFor[string](), false, false},
		{"contains scalar", &vectordata.Filter{Operator: vectordata.Contains, Property: "Value", Value: 1}, reflect.TypeFor[int](), false, false},
		{"contains incompatible", &vectordata.Filter{Operator: vectordata.Contains, Property: "Value", Value: "one"}, reflect.TypeFor[[]int](), false, false},
		{"in scalar", &vectordata.Filter{Operator: vectordata.In, Property: "Value", Value: 1}, reflect.TypeFor[int](), false, false},
		{"in incompatible", &vectordata.Filter{Operator: vectordata.In, Property: "Value", Value: []string{"one"}}, reflect.TypeFor[int](), false, false},
		{"unordered type", &vectordata.Filter{Operator: vectordata.LessThan, Property: "Value", Value: []int{1}}, reflect.TypeFor[[]int](), false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolve := func(string) (providerservices.FilterProperty, error) {
				property := providerservices.FilterProperty{Type: test.typ}
				if !test.noReader {
					property.Value = func(any) (any, bool, error) {
						t.Fatal("compilation read a record")
						return nil, false, nil
					}
				}
				return property, nil
			}
			if test.noResolver {
				resolve = nil
			}
			if predicate, err := providerservices.CompileFilter(test.filter, resolve); err == nil || predicate != nil {
				t.Fatalf("invalid compilation = %v, predicate present=%v", err, predicate != nil)
			}
		})
	}
}

func TestCompileFilter_ResolverError(t *testing.T) {
	want := errors.New("property unavailable")
	filter := &vectordata.Filter{Operator: vectordata.Or, Operands: []vectordata.Filter{
		{Operator: vectordata.Equal, Property: "Value", Value: 1},
		{Operator: vectordata.Equal, Property: "Unavailable", Value: 1},
	}}
	predicate, err := providerservices.CompileFilter(filter, func(name string) (providerservices.FilterProperty, error) {
		if name == "Unavailable" {
			return providerservices.FilterProperty{}, want
		}
		return providerservices.FilterProperty{
			Type: reflect.TypeFor[int](),
			Value: func(any) (any, bool, error) {
				t.Fatal("compilation read a record")
				return nil, false, nil
			},
		}, nil
	})
	if !errors.Is(err, want) || predicate != nil {
		t.Fatalf("compilation = %v, predicate present=%v", err, predicate != nil)
	}
}
