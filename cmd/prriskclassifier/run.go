// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

func classifyPullRequest(ctx context.Context, client riskClient, number int) (classificationResult, error) {
	if err := client.ensureLabels(ctx); err != nil {
		return classificationResult{}, markFailure(ctx, client, number, fmt.Errorf("ensure risk labels: %w", err))
	}
	files, labels, err := client.pullRequestSignals(ctx, number)
	if err != nil {
		return classificationResult{}, markFailure(ctx, client, number, fmt.Errorf("read pull request signals: %w", err))
	}

	decision := classifyDeterministically(files, labels)
	result := classificationResult{Decision: decision, NeedsAgent: decision.Label == ""}
	if result.NeedsAgent {
		if !slices.Contains(labels, pendingAutoRisk) {
			if err := client.addLabel(ctx, number, pendingAutoRisk); err != nil {
				return classificationResult{}, fmt.Errorf("mark semantic risk classification pending: %w", err)
			}
		}
		for _, label := range currentRiskLabels(labels) {
			if err := client.removeLabel(ctx, number, label); err != nil {
				return classificationResult{}, fmt.Errorf("clear stale %s before semantic review: %w", label, err)
			}
		}
		if slices.Contains(labels, failedAutoRisk) {
			if err := client.removeLabel(ctx, number, failedAutoRisk); err != nil {
				return classificationResult{}, fmt.Errorf("clear stale %s before semantic review: %w", failedAutoRisk, err)
			}
		}
		return result, nil
	}

	if !slices.Contains(labels, decision.Label) {
		if err := client.addLabel(ctx, number, decision.Label); err != nil {
			return classificationResult{}, markFailure(ctx, client, number, fmt.Errorf("add %s: %w", decision.Label, err))
		}
	}
	for _, label := range managedLabels {
		if label.Name == decision.Label || !slices.Contains(labels, label.Name) {
			continue
		}
		if err := client.removeLabel(ctx, number, label.Name); err != nil {
			return classificationResult{}, markFailure(ctx, client, number, fmt.Errorf("remove stale %s: %w", label.Name, err))
		}
	}
	return result, nil
}

func markFailure(ctx context.Context, client riskClient, number int, cause error) error {
	if err := client.addLabel(ctx, number, failedAutoRisk); err != nil {
		return errors.Join(cause, fmt.Errorf("add %s: %w", failedAutoRisk, err))
	}
	return cause
}

func validatePullRequestRisk(ctx context.Context, client riskClient, number int) error {
	if err := client.ensureLabels(ctx); err != nil {
		return markFailure(ctx, client, number, fmt.Errorf("ensure risk labels: %w", err))
	}
	labels, err := client.pullRequestLabels(ctx, number)
	if err != nil {
		return markFailure(ctx, client, number, fmt.Errorf("read pull request labels: %w", err))
	}

	riskLabels := currentRiskLabels(labels)
	hasPendingMarker := slices.Contains(labels, pendingAutoRisk)
	hasFailureMarker := slices.Contains(labels, failedAutoRisk)
	if !hasPendingMarker && ((len(riskLabels) == 1 && !hasFailureMarker) || (len(riskLabels) == 0 && hasFailureMarker)) {
		return nil
	}

	cause := fmt.Errorf(
		"invalid automatic risk state: risk labels=%v, %s=%t, %s=%t",
		riskLabels,
		pendingAutoRisk,
		hasPendingMarker,
		failedAutoRisk,
		hasFailureMarker,
	)
	if !hasFailureMarker {
		if err := client.addLabel(ctx, number, failedAutoRisk); err != nil {
			return errors.Join(cause, fmt.Errorf("add %s: %w", failedAutoRisk, err))
		}
	}
	for _, label := range riskLabels {
		if err := client.removeLabel(ctx, number, label); err != nil {
			return errors.Join(cause, fmt.Errorf("remove invalid %s: %w", label, err))
		}
	}
	if hasPendingMarker {
		if err := client.removeLabel(ctx, number, pendingAutoRisk); err != nil {
			return errors.Join(cause, fmt.Errorf("remove invalid %s: %w", pendingAutoRisk, err))
		}
	}
	return cause
}

func currentRiskLabels(labels []string) []string {
	return slices.DeleteFunc(slices.Clone(labels), func(label string) bool {
		return label != riskLow && label != riskMedium && label != riskHigh
	})
}
