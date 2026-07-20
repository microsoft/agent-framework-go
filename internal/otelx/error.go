// Copyright (c) Microsoft. All rights reserved.

package otelx

import (
	"fmt"
	"strings"
)

// ErrorTypeName returns the short (unqualified) type name of err, matching
// Python's type(exception).__name__ for cross-SDK parity on the error.type span attribute.
func ErrorTypeName(err error) string {
	t := fmt.Sprintf("%T", err)
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	return t
}
