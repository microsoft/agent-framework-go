// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVerifyDeterministicOutput(t *testing.T) {
	verifier := ExampleVerifier{}
	result := verifier.Verify(context.Background(), ExampleDefinition{
		Name:            "example",
		IsDeterministic: true,
		MustContain:     []string{"hello"},
		MustNotContain:  []string{"panic"},
	}, ExampleRunResult{Stdout: "hello world", ExitCode: 0})
	if !result.Passed {
		t.Fatalf("Passed = false, failures: %#v", result.Failures)
	}
}

func TestVerifyRequiresAIAgentForSemanticChecks(t *testing.T) {
	verifier := ExampleVerifier{}
	result := verifier.Verify(context.Background(), ExampleDefinition{
		Name:                      "example",
		ExpectedOutputDescription: []string{"contains a joke"},
	}, ExampleRunResult{Stdout: "hello world", ExitCode: 0})
	if result.Passed {
		t.Fatal("Passed = true, want failure")
	}
	if len(result.Failures) != 1 {
		t.Fatalf("Failures = %#v, want one", result.Failures)
	}
}

func TestTruncateDoesNotSplitRunes(t *testing.T) {
	// "°" is two bytes (0xC2 0xB0); truncating at byte 1 must not split it.
	got := truncate("a°cdef", 2)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, "a") || strings.ContainsRune(got, '�') {
		t.Fatalf("truncate = %q, want a clean rune-boundary cut", got)
	}
	// Short input is returned unchanged.
	if truncate("ok", 10) != "ok" {
		t.Fatalf("short input was modified")
	}
}
