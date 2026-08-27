// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"testing"
)

func TestParseOptionsFiltersCategoryAndNames(t *testing.T) {
	var stderr bytes.Buffer
	options, ok := parseOptions([]string{"--category", "01-get-started", "01_get_started_05_first_workflow", "--parallel", "2", "--build"}, &stderr)
	if !ok {
		t.Fatalf("parseOptions failed: %s", stderr.String())
	}
	if options.MaxParallelism != 2 {
		t.Fatalf("MaxParallelism = %d, want 2", options.MaxParallelism)
	}
	if !options.BuildExamples {
		t.Fatal("BuildExamples = false, want true")
	}
	if len(options.Examples) != 1 || options.Examples[0].Name != "01_get_started_05_first_workflow" {
		t.Fatalf("Examples = %#v, want only first workflow", options.Examples)
	}
}

func TestParseOptionsRejectsMissingFlagValue(t *testing.T) {
	var stderr bytes.Buffer
	_, ok := parseOptions([]string{"--csv"}, &stderr)
	if ok {
		t.Fatal("parseOptions succeeded, want failure")
	}
	if stderr.String() == "" {
		t.Fatal("stderr is empty")
	}
}
