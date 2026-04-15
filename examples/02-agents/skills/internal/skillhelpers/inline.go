// Copyright (c) Microsoft. All rights reserved.

package skillhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/microsoft/agent-framework-go/memory/skills"
)

// NewStaticResource creates a resource backed by static content.
func NewStaticResource(name, description, content string) skills.Resource {
	return NewFuncResource(name, description, func(context.Context) (any, error) {
		return content, nil
	})
}

// NewFuncResource creates a resource backed by a function.
func NewFuncResource(name, description string, read func(context.Context) (any, error)) skills.Resource {
	if read == nil {
		panic("read is required")
	}
	return skills.Resource{Name: name, Description: description, Read: read}
}

// NewFuncScript creates a script backed by a function.
func NewFuncScript(name, description string, run func(context.Context, *skills.Skill, map[string]any) (any, error)) skills.Script {
	if run == nil {
		panic("run is required")
	}
	return skills.Script{
		Name:        name,
		Description: description,
		Run: func(ctx context.Context, owner *skills.Skill, arguments map[string]any) (any, error) {
			if arguments == nil {
				arguments = map[string]any{}
			}
			return run(ctx, owner, arguments)
		},
	}
}

// NumberArg reads a numeric argument from a tool call argument map.
func NumberArg(arguments map[string]any, name string) (float64, error) {
	value, ok := arguments[name]
	if !ok {
		return 0, fmt.Errorf("missing argument %q", name)
	}

	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int32:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case json.Number:
		return typed.Float64()
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, fmt.Errorf("argument %q must be numeric: %w", name, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("argument %q must be numeric, got %T", name, value)
	}
}

// StringArg reads a string argument from a tool call argument map.
func StringArg(arguments map[string]any, name string) (string, error) {
	value, ok := arguments[name]
	if !ok {
		return "", fmt.Errorf("missing argument %q", name)
	}

	switch typed := value.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		return "", fmt.Errorf("argument %q must be a string, got %T", name, value)
	}
}

// Round rounds a float to the given number of decimal places.
func Round(value float64, precision int) float64 {
	scale := math.Pow10(precision)
	return math.Round(value*scale) / scale
}

// MultiplyConversion builds a standard JSON-shaped conversion response.
func MultiplyConversion(value, factor float64, precision int) map[string]any {
	return map[string]any{
		"value":  value,
		"factor": factor,
		"result": Round(value*factor, precision),
	}
}
