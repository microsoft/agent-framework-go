// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main

import (
	"path/filepath"
	"slices"
	"strings"
)

const (
	riskLow    = "risk:low"
	riskMedium = "risk:medium"
	riskHigh   = "risk:high"
)

type deterministicDecision struct {
	Label  string
	Reason string
}

func classifyDeterministically(files, labels []string) deterministicDecision {
	kinds := labelsWithPrefix(labels, "kind:")

	if slices.Contains(kinds, "kind:code") && slices.ContainsFunc(files, isCriticalProductionPath) {
		return deterministicDecision{
			Label:  riskHigh,
			Reason: "production code changes a workflow, tool-execution, or concurrency boundary",
		}
	}

	if isClearlyLowRisk(kinds, labels) {
		return deterministicDecision{
			Label:  riskLow,
			Reason: "changes are limited to documentation, tests, or examples",
		}
	}

	return deterministicDecision{}
}

func isClearlyLowRisk(kinds, labels []string) bool {
	if len(kinds) == 0 ||
		!slices.ContainsFunc(labels, func(label string) bool { return strings.HasPrefix(label, "size:") }) ||
		slices.Contains(labels, "public-api-change") ||
		slices.Contains(labels, "failed-auto-classify") ||
		slices.Contains(labels, "size:xlarge") {
		return false
	}
	for _, kind := range kinds {
		if kind != "kind:docs" && kind != "kind:tests" && kind != "kind:examples" {
			return false
		}
	}
	return true
}

func isCriticalProductionPath(path string) bool {
	path = filepath.ToSlash(path)
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/testdata/") {
		return false
	}
	return isCriticalWorkflowPath(path) ||
		strings.HasPrefix(path, "agent/harness/toolapproval/") ||
		strings.HasPrefix(path, "agent/harness/toolautocall/") ||
		strings.HasPrefix(path, "tool/shelltool/") ||
		strings.HasPrefix(path, "internal/concurrent/")
}

func isCriticalWorkflowPath(path string) bool {
	if !strings.HasPrefix(path, "workflow/") {
		return false
	}
	relative := strings.TrimPrefix(path, "workflow/")
	if !strings.Contains(relative, "/") {
		return true
	}
	return strings.HasPrefix(relative, "agentworkflow/") ||
		strings.HasPrefix(relative, "checkpoint/") ||
		strings.HasPrefix(relative, "inproc/") ||
		strings.HasPrefix(relative, "internal/checkpoint/") ||
		strings.HasPrefix(relative, "internal/execution/")
}

func labelsWithPrefix(labels []string, prefix string) []string {
	return slices.Collect(func(yield func(string) bool) {
		for _, label := range labels {
			if strings.HasPrefix(label, prefix) && !yield(label) {
				return
			}
		}
	})
}
