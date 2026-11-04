// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

func (c *collection) translateFilter(filter *vectordata.Filter) (string, error) {
	if err := filter.Validate(); err != nil {
		return "", err
	}
	if filter == nil {
		return "", nil
	}
	return c.filterExpression(filter)
}

func (c *collection) filterExpression(filter *vectordata.Filter) (string, error) {
	switch filter.Operator {
	case vectordata.And, vectordata.Or, vectordata.Not:
		parts := make([]string, len(filter.Operands))
		for i := range filter.Operands {
			part, err := c.filterExpression(&filter.Operands[i])
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		if filter.Operator == vectordata.Not {
			return "(not " + parts[0] + ")", nil
		}
		return "(" + strings.Join(parts, " "+string(filter.Operator)+" ") + ")", nil
	}
	name, typ, err := c.filterProperty(filter.Property)
	if err != nil {
		return "", err
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch filter.Operator {
	case vectordata.In:
		if typ.Kind() != reflect.String {
			return "", fmt.Errorf("property %q: in filters require a string property", filter.Property)
		}
		value := reflect.ValueOf(filter.Value)
		values := make([]string, 0, value.Len())
		hasEmpty := false
		delimiter := ", "
		for i := range value.Len() {
			element := value.Index(i)
			for element.Kind() == reflect.Interface && !element.IsNil() {
				element = element.Elem()
			}
			if element.Kind() != reflect.String {
				return "", fmt.Errorf("property %q: in filters require string values", filter.Property)
			}
			text := element.String()
			// search.in ignores empty tokens, so match them separately.
			if text == "" {
				hasEmpty = true
				continue
			}
			values = append(values, text)
			if strings.ContainsAny(text, ", ") {
				delimiter = "|"
			}
		}
		if len(values) == 0 {
			if hasEmpty {
				return "(" + name + " eq '')", nil
			}
			return "false", nil
		}
		if delimiter == "|" {
			for _, value := range values {
				if strings.Contains(value, "|") {
					return "", fmt.Errorf("property %q: in filter values cannot combine commas or spaces with pipes", filter.Property)
				}
			}
		}
		expression := "search.in(" + name + ", " + quote(strings.Join(values, delimiter))
		if delimiter == "|" {
			expression += ", '|'"
		}
		expression += ")"
		if hasEmpty {
			expression = "(" + name + " eq '' or " + expression + ")"
		}
		return expression, nil
	case vectordata.Contains:
		if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
			return "", fmt.Errorf("property %q: contains filters require a collection property", filter.Property)
		}
		value, err := odataLiteral(filter.Value)
		if err != nil {
			return "", err
		}
		return name + "/any(t: t eq " + value + ")", nil
	default:
		if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array && typ != reflect.TypeFor[uuid.UUID]() {
			return "", fmt.Errorf("property %q: scalar comparison requires a scalar property", filter.Property)
		}
		value, err := odataLiteral(filter.Value)
		if err != nil {
			return "", err
		}
		return "(" + name + " " + string(filter.Operator) + " " + value + ")", nil
	}
}

func (c *collection) filterProperty(name string) (string, reflect.Type, error) {
	switch property := c.properties[name].(type) {
	case *providerservices.KeyPropertyModel:
		return property.StorageName, property.Type, nil
	case *providerservices.DataPropertyModel:
		return property.StorageName, property.Type, nil
	default:
		return "", nil, fmt.Errorf("filter or ordering property %q must be a key or data property", name)
	}
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func odataLiteral(value any) (string, error) {
	reflected := reflect.ValueOf(value)
	visited := make(map[uintptr]bool)
	for reflected.IsValid() && (reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface) {
		if reflected.IsNil() {
			return "null", nil
		}
		if reflected.Kind() == reflect.Pointer {
			address := reflected.Pointer()
			if visited[address] {
				return "", fmt.Errorf("filter value contains a pointer cycle")
			}
			visited[address] = true
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return "null", nil
	}
	if reflected.Type() == reflect.TypeFor[uuid.UUID]() {
		return quote(reflected.Interface().(uuid.UUID).String()), nil
	}
	if reflected.Type() == reflect.TypeFor[time.Time]() {
		return reflected.Interface().(time.Time).Format(time.RFC3339Nano), nil
	}
	switch reflected.Kind() {
	case reflect.String:
		return quote(reflected.String()), nil
	case reflect.Bool:
		return strconv.FormatBool(reflected.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(reflected.Int(), 10), nil
	case reflect.Uint8:
		return strconv.FormatUint(reflected.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		number := reflected.Float()
		switch {
		case math.IsInf(number, 1):
			return "INF", nil
		case math.IsInf(number, -1):
			return "-INF", nil
		default:
			return strconv.FormatFloat(number, 'g', -1, reflected.Type().Bits()), nil
		}
	default:
		return "", fmt.Errorf("unsupported filter value type %v", reflected.Type())
	}
}
