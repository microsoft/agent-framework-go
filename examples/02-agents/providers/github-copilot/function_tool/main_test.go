// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"testing"
)

func TestCanaryStatusTool(t *testing.T) {
	result, err := canaryStatusTool.Call(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != canaryResult {
		t.Fatalf("result = %q, want %q", result, canaryResult)
	}
}
