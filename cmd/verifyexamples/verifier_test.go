// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

type textSemanticVerifier string

func (v textSemanticVerifier) RunText(context.Context, string, ...agent.Option) agent.ResponseStream {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(&agent.ResponseUpdate{
			Role:     message.RoleAssistant,
			Contents: message.Contents{&message.TextContent{Text: string(v)}},
		}, nil)
	}
}

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

func TestVerifySemanticOutputWithoutStructuredOutput(t *testing.T) {
	verifier := ExampleVerifier{
		verifierAgent: textSemanticVerifier(`{"pass":true,"ai_reasoning":"The greeting is present.","expectation_results":[{"expectation":"contains a greeting","met":true,"detail":"stdout contains hello"}]}`),
	}
	result := verifier.Verify(context.Background(), ExampleDefinition{
		Name:                      "example",
		ExpectedOutputDescription: []string{"contains a greeting"},
	}, ExampleRunResult{Stdout: "hello world", ExitCode: 0})
	if !result.Passed {
		t.Fatalf("Passed = false, failures: %#v", result.Failures)
	}
	if result.AIReasoning != "The greeting is present." {
		t.Fatalf("AIReasoning = %q, want parsed reasoning", result.AIReasoning)
	}
}

func TestVerifySemanticOutputRejectsInvalidJSON(t *testing.T) {
	verifier := ExampleVerifier{verifierAgent: textSemanticVerifier(`not JSON`)}
	result := verifier.Verify(context.Background(), ExampleDefinition{
		Name:                      "example",
		ExpectedOutputDescription: []string{"contains a greeting"},
	}, ExampleRunResult{Stdout: "hello world", ExitCode: 0})
	if result.Passed {
		t.Fatal("Passed = true, want failure")
	}
	if len(result.Failures) != 1 || result.Failures[0] == "" {
		t.Fatalf("Failures = %#v, want one JSON error", result.Failures)
	}
}

func TestVerifySemanticOutputRequiresEveryExpectation(t *testing.T) {
	verifier := ExampleVerifier{
		verifierAgent: textSemanticVerifier(`{"pass":true,"ai_reasoning":"Looks good.","expectation_results":[]}`),
	}
	result := verifier.Verify(context.Background(), ExampleDefinition{
		Name:                      "example",
		ExpectedOutputDescription: []string{"contains a greeting"},
	}, ExampleRunResult{Stdout: "hello world", ExitCode: 0})
	if result.Passed {
		t.Fatal("Passed = true, want failure")
	}
	if len(result.Failures) != 1 || result.Failures[0] == "" {
		t.Fatalf("Failures = %#v, want one expectation-count error", result.Failures)
	}
}
