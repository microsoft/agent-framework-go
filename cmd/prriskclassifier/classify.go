// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main

import (
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

func classifyDeterministically(_ []string, labels []string) deterministicDecision {
	kinds := labelsWithPrefix(labels, "kind:")

	if isClearlyLowRisk(kinds, labels) {
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

func labelsWithPrefix(labels []string, prefix string) []string {
	return slices.Collect(func(yield func(string) bool) {
		for _, label := range labels {
			if strings.HasPrefix(label, prefix) && !yield(label) {
				return
			}
		}
	})
}
