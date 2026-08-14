// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"slices"
)

func classifyPullRequest(ctx context.Context, client riskClient, number int) (classificationResult, error) {
	if err := client.ensureLabels(ctx); err != nil {
		return classificationResult{}, fmt.Errorf("ensure risk labels: %w", err)
	}
	files, labels, err := client.pullRequestSignals(ctx, number)
	if err != nil {
		return classificationResult{}, fmt.Errorf("read pull request signals: %w", err)
	}

	decision := classifyDeterministically(files, labels)
	result := classificationResult{Decision: decision, NeedsAgent: decision.Label == ""}
	if result.NeedsAgent {
		return result, nil
	}

	if !slices.Contains(labels, decision.Label) {
		if err := client.addLabel(ctx, number, decision.Label); err != nil {
			return classificationResult{}, fmt.Errorf("add %s: %w", decision.Label, err)
		}
	}
	for _, label := range managedLabels {
		if label.Name == decision.Label || !slices.Contains(labels, label.Name) {
			continue
		}
		if err := client.removeLabel(ctx, number, label.Name); err != nil {
			return classificationResult{}, fmt.Errorf("remove stale %s: %w", label.Name, err)
		}
	}
	return result, nil
}
