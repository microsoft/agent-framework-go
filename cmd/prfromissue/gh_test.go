// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"reflect"
	"testing"
)

func TestRedactGHArgs(t *testing.T) {
	args := []string{"pr", "create", "--title", "test", "--body", "top secret", "--body=another secret"}
	got := redactGHArgs(args)
	want := []string{"pr", "create", "--title", "test", "--body", "<redacted>", "--body=<redacted>"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redactGHArgs() = %v, want %v", got, want)
	}
}
