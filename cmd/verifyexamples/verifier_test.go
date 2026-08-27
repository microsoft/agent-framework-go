// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"testing"
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
