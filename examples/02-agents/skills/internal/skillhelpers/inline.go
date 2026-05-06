// Copyright (c) Microsoft. All rights reserved.

package skillhelpers

import (
	"fmt"
	"math"
	"strconv"
)

// NumberArg reads a numeric argument from a positional CLI-style string slice.
// index is the position in the args slice.
func NumberArg(args []string, index int) (float64, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("missing positional argument at index %d (got %d args)", index, len(args))
	}
	parsed, err := strconv.ParseFloat(args[index], 64)
	if err != nil {
		return 0, fmt.Errorf("argument at index %d must be numeric: %w", index, err)
	}
	return parsed, nil
}

// StringArg reads a string argument from a positional CLI-style string slice.
// index is the position in the args slice.
func StringArg(args []string, index int) (string, error) {
	if index < 0 || index >= len(args) {
		return "", fmt.Errorf("missing positional argument at index %d (got %d args)", index, len(args))
	}
	return args[index], nil
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
