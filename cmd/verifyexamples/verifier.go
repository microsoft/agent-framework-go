// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
)

type semanticVerifier interface {
	RunText(context.Context, string, ...agent.Option) agent.ResponseStream
}

type ExampleVerifier struct {
	verifierAgent            semanticVerifier
	supportsStructuredOutput bool
}

func (v ExampleVerifier) Verify(ctx context.Context, example ExampleDefinition, run ExampleRunResult) VerificationResult {
	var failures []string
	if run.ExitCode != 0 {
		failures = append(failures, fmt.Sprintf("Exit code was %d, expected 0. Stderr: %s", run.ExitCode, truncate(run.Stderr, 500)))
	}
	for _, expected := range example.MustContain {
		if !strings.Contains(run.Stdout, expected) {
			failures = append(failures, fmt.Sprintf("Output missing expected substring: %q", expected))
		}
	}
	for _, unexpected := range example.MustNotContain {
		if strings.Contains(run.Stdout, unexpected) {
			failures = append(failures, fmt.Sprintf("Output contains unexpected substring: %q", unexpected))
		}
	}

	aiReasoning := ""
	if !example.IsDeterministic && len(example.ExpectedOutputDescription) > 0 {
		if v.verifierAgent == nil {
			failures = append(failures, "AI verification required but no AI agent configured (missing FOUNDRY_PROJECT_ENDPOINT).")
		} else {
			reasoning, unmet := v.verifyWithAI(ctx, run.Stdout, run.Stderr, example.ExpectedOutputDescription)
			aiReasoning = reasoning
			for _, unmetExpectation := range unmet {
				failures = append(failures, "AI expectation not met: "+unmetExpectation)
			}
		}
	}

	passed := len(failures) == 0
	summary := fmt.Sprintf("%d check(s) failed", len(failures))
	if passed {
		summary = "All checks passed"
	}
	return VerificationResult{
		ExampleName: example.Name,
		Passed:      passed,
		Summary:     summary,
		Failures:    failures,
		AIReasoning: aiReasoning,
	}
}

func (v ExampleVerifier) verifyWithAI(ctx context.Context, stdout string, stderr string, expectations []string) (string, []string) {
	var expectationList strings.Builder
	for i, expectation := range expectations {
		fmt.Fprintf(&expectationList, "  %d. %s\n", i+1, expectation)
	}

	stderrSection := ""
	if strings.TrimSpace(stderr) != "" {
		stderrSection = fmt.Sprintf("\nStderr output:\n---\n%s\n---\n", truncate(stderr, 2000))
	}

	prompt := fmt.Sprintf(`Actual program output:
---
%s
---
%s
Expectations to verify:
%s
Does the output satisfy all expectations?
Respond with only JSON matching this shape:
{"pass":true,"ai_reasoning":"brief overall assessment","expectation_results":[{"expectation":"text","met":true,"detail":"evidence"}]}
`, truncate(stdout, 4000), stderrSection, expectationList.String())

	var result aiVerificationResponse
	var options []agent.Option
	if v.supportsStructuredOutput {
		options = append(options, agent.WithStructuredOutput(&result))
	} else {
		options = append(options, agent.Stream(false))
	}
	response, err := v.verifierAgent.RunText(ctx, prompt, options...).Collect()
	if err != nil {
		return "AI verification error: " + err.Error(), []string{"AI verification error: " + err.Error()}
	}

	if len(result.ExpectationResults) == 0 && response != nil && response.String() != "" {
		if err := json.Unmarshal([]byte(response.String()), &result); err != nil {
			text := "AI verification returned invalid JSON: " + err.Error() + ". Raw: " + truncate(response.String(), 500)
			return text, []string{text}
		}
	}
	if len(result.ExpectationResults) != len(expectations) {
		text := fmt.Sprintf("AI verification returned %d expectation results, want %d.", len(result.ExpectationResults), len(expectations))
		return text, []string{text}
	}
	for i, expectation := range expectations {
		if result.ExpectationResults[i].Expectation != expectation {
			text := fmt.Sprintf("AI verification result %d echoed expectation %q, want %q.", i+1, result.ExpectationResults[i].Expectation, expectation)
			return text, []string{text}
		}
	}

	reasoning := result.AIReasoning
	if strings.TrimSpace(reasoning) == "" {
		reasoning = "(no reasoning provided)"
	}
	var unmet []string
	for _, expectation := range result.ExpectationResults {
		if expectation.Met {
			continue
		}
		detail := expectation.Expectation
		if strings.TrimSpace(expectation.Detail) != "" {
			detail += " - " + expectation.Detail
		}
		unmet = append(unmet, detail)
	}
	if len(unmet) == 0 && !result.Pass {
		unmet = append(unmet, reasoning)
	}
	return reasoning, unmet
}

type aiVerificationResponse struct {
	Pass               bool                  `json:"pass"`
	AIReasoning        string                `json:"ai_reasoning"`
	ExpectationResults []aiExpectationResult `json:"expectation_results"`
}

type aiExpectationResult struct {
	Expectation string `json:"expectation"`
	Met         bool   `json:"met"`
	Detail      string `json:"detail"`
}

func truncate(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength] + "... (truncated)"
}
