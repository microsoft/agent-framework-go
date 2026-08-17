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

	if isClearlyLowRisk(kinds, labels) && len(files) > 0 && !slices.ContainsFunc(files, isPotentialProductionFile) {
		return deterministicDecision{
			Label:  riskLow,
			Reason: "changes are limited to documentation, tests, or examples",
		}
	}

	return deterministicDecision{Reason: "production or ambiguous changes require confidence-gated semantic review"}
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

func isPotentialProductionFile(path string) bool {
	path = filepath.ToSlash(path)
	base := filepath.Base(path)
	return !strings.HasPrefix(path, "docs/") &&
		!strings.HasPrefix(path, "examples/") &&
		!strings.Contains(path, "/testdata/") &&
		!strings.HasSuffix(path, "_test.go") &&
		!strings.HasSuffix(path, ".md") &&
		base != "LICENSE"
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
