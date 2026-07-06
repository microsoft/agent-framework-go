// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.csv")
	err := writeCSV(path,
		[]VerificationResult{{ExampleName: "example", Passed: false, Failures: []string{"missing, thing"}}},
		[]SkippedExample{{Name: "skipped", Reason: "missing env"}},
		[]ExampleDefinition{{Name: "example", ProjectPath: "examples/example"}, {Name: "skipped", ProjectPath: "examples/skipped"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "FAILED") || !strings.Contains(string(content), "SKIPPED") {
		t.Fatalf("CSV content missing statuses:\n%s", content)
	}
}

func TestWriteMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.md")
	err := writeMarkdown(path, []VerificationResult{{ExampleName: "example", Passed: true}}, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Example Verification Results") || !strings.Contains(string(content), "PASSED") {
		t.Fatalf("Markdown content missing expected text:\n%s", content)
	}
}
