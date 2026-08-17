// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOptionsFromEnvironment(t *testing.T) {
	env := map[string]string{
		"GITHUB_REPOSITORY":   "microsoft/agent-framework-go",
		"PR_NUMBER":           "824",
		"GITHUB_OUTPUT":       "output.txt",
		"GITHUB_STEP_SUMMARY": "summary.md",
	}
	opts, err := parseOptions(nil, func(name string) string { return env[name] }, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.repo != env["GITHUB_REPOSITORY"] || opts.prNumber != 824 || opts.output != "output.txt" || opts.summary != "summary.md" {
		t.Fatalf("options = %+v", opts)
	}
}

func TestParseOptionsRejectsInvalidInput(t *testing.T) {
	tests := [][]string{
		{"-repo", "invalid", "-pr-number", "1"},
		{"-repo", "microsoft/agent-framework-go", "-pr-number", "0"},
	}
	for _, args := range tests {
		if _, err := parseOptions(args, func(string) string { return "" }, &strings.Builder{}); err == nil {
			t.Fatalf("parseOptions(%v) unexpectedly succeeded", args)
		}
	}
}

func TestParseOptionsValidateResult(t *testing.T) {
	opts, err := parseOptions(
		[]string{"-repo", "microsoft/agent-framework-go", "-pr-number", "824", "-validate-result"},
		func(string) string { return "" },
		&strings.Builder{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.validateResult {
		t.Fatal("validate-result was not enabled")
	}
}

func TestWriteResult(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		output:  filepath.Join(dir, "output.txt"),
		summary: filepath.Join(dir, "summary.md"),
	}
	result := classificationResult{
		Decision: deterministicDecision{Label: riskHigh, Reason: "critical path"},
	}
	if err := writeResult(opts, result); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(opts.output)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), "needs-agent=false\nrisk-label=risk:high\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	summary, err := os.ReadFile(opts.summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "Applied `risk:high`: critical path.") {
		t.Fatalf("summary = %q", summary)
	}
}
