// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type fakeRiskClient struct {
	files   []string
	labels  []string
	ensured bool
	added   []string
	removed []string
	addErr  map[string]error
}

func (f *fakeRiskClient) ensureLabels(context.Context) error {
	f.ensured = true
	return nil
}

func (f *fakeRiskClient) pullRequestSignals(context.Context, int) ([]string, []string, error) {
	return f.files, f.labels, nil
}

func (f *fakeRiskClient) pullRequestLabels(context.Context, int) ([]string, error) {
	return f.labels, nil
}

func (f *fakeRiskClient) addLabel(_ context.Context, _ int, label string) error {
	f.added = append(f.added, label)
	return f.addErr[label]
}

func (f *fakeRiskClient) removeLabel(_ context.Context, _ int, label string) error {
	f.removed = append(f.removed, label)
	return nil
}

func TestClassifyPullRequestDeterministicLowReconcilesLabels(t *testing.T) {
	client := &fakeRiskClient{
		files:  []string{"docs/guide.md"},
		labels: []string{"size:small", "kind:docs", riskHigh, pendingAutoRisk, failedAutoRisk},
	}
	result, err := classifyPullRequest(context.Background(), client, 42)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Label != riskLow || result.NeedsAgent {
		t.Fatalf("result = %+v", result)
	}
	if !client.ensured || !slices.Equal(client.added, []string{riskLow}) {
		t.Fatalf("ensured = %t, added = %v", client.ensured, client.added)
	}
	if !slices.Equal(client.removed, []string{riskHigh, pendingAutoRisk, failedAutoRisk}) {
		t.Fatalf("removed = %v", client.removed)
	}
}

func TestClassifyPullRequestDeterministicNoop(t *testing.T) {
	client := &fakeRiskClient{
		files:  []string{"docs/guide.md"},
		labels: []string{"size:small", "kind:docs", riskLow},
	}
	result, err := classifyPullRequest(context.Background(), client, 42)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Label != riskLow || result.NeedsAgent {
		t.Fatalf("result = %+v", result)
	}
	if len(client.added) != 0 || len(client.removed) != 0 {
		t.Fatalf("added = %v, removed = %v", client.added, client.removed)
	}
}

func TestClassifyPullRequestInconclusiveMarksPendingAndClearsStaleResult(t *testing.T) {
	client := &fakeRiskClient{
		files:  []string{"provider/openaiprovider/openai.go"},
		labels: []string{"kind:code", riskMedium, failedAutoRisk},
	}
	result, err := classifyPullRequest(context.Background(), client, 42)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Label != "" || !result.NeedsAgent {
		t.Fatalf("result = %+v", result)
	}
	if !slices.Equal(client.added, []string{pendingAutoRisk}) || !slices.Equal(client.removed, []string{riskMedium, failedAutoRisk}) {
		t.Fatalf("added = %v, removed = %v", client.added, client.removed)
	}
}

func TestClassifyPullRequestInconclusiveDoesNotDuplicateMarker(t *testing.T) {
	client := &fakeRiskClient{
		files:  []string{"provider/openaiprovider/openai.go"},
		labels: []string{"kind:code", pendingAutoRisk},
	}
	result, err := classifyPullRequest(context.Background(), client, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsAgent || len(client.added) != 0 || len(client.removed) != 0 {
		t.Fatalf("result=%+v added=%v removed=%v", result, client.added, client.removed)
	}
}

func TestClassifyPullRequestLabelFailureAddsMarker(t *testing.T) {
	writeErr := errors.New("write denied")
	client := &fakeRiskClient{
		files:  []string{"docs/guide.md"},
		labels: []string{"size:small", "kind:docs"},
		addErr: map[string]error{riskLow: writeErr},
	}
	_, err := classifyPullRequest(context.Background(), client, 42)
	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want write failure", err)
	}
	if !slices.Equal(client.added, []string{riskLow, failedAutoRisk}) {
		t.Fatalf("added = %v", client.added)
	}
}

func TestValidatePullRequestRiskAcceptsExclusiveStates(t *testing.T) {
	for _, labels := range [][]string{
		{riskMedium},
		{failedAutoRisk},
	} {
		client := &fakeRiskClient{labels: labels}
		if err := validatePullRequestRisk(context.Background(), client, 42); err != nil {
			t.Fatalf("labels %v: %v", labels, err)
		}
		if len(client.added) != 0 || len(client.removed) != 0 {
			t.Fatalf("labels %v: added=%v removed=%v", labels, client.added, client.removed)
		}
	}
}

func TestValidatePullRequestRiskConvertsMissingResultToUnable(t *testing.T) {
	client := &fakeRiskClient{}
	if err := validatePullRequestRisk(context.Background(), client, 42); err == nil {
		t.Fatal("validation unexpectedly succeeded")
	}
	if !slices.Equal(client.added, []string{failedAutoRisk}) || len(client.removed) != 0 {
		t.Fatalf("added=%v removed=%v", client.added, client.removed)
	}
}

func TestValidatePullRequestRiskClearsConflictingResult(t *testing.T) {
	client := &fakeRiskClient{labels: []string{riskLow, riskHigh}}
	if err := validatePullRequestRisk(context.Background(), client, 42); err == nil {
		t.Fatal("validation unexpectedly succeeded")
	}
	if !slices.Equal(client.added, []string{failedAutoRisk}) {
		t.Fatalf("added=%v", client.added)
	}
	if !slices.Equal(client.removed, []string{riskLow, riskHigh}) {
		t.Fatalf("removed=%v", client.removed)
	}
}

func TestValidatePullRequestRiskClearsStaleRiskOnAbstention(t *testing.T) {
	client := &fakeRiskClient{labels: []string{riskHigh, failedAutoRisk}}
	if err := validatePullRequestRisk(context.Background(), client, 42); err == nil {
		t.Fatal("validation unexpectedly succeeded")
	}
	if len(client.added) != 0 || !slices.Equal(client.removed, []string{riskHigh}) {
		t.Fatalf("added=%v removed=%v", client.added, client.removed)
	}
}

func TestValidatePullRequestRiskConvertsPendingResultToUnable(t *testing.T) {
	client := &fakeRiskClient{labels: []string{pendingAutoRisk}}
	if err := validatePullRequestRisk(context.Background(), client, 42); err == nil {
		t.Fatal("validation unexpectedly succeeded")
	}
	if !slices.Equal(client.added, []string{failedAutoRisk}) {
		t.Fatalf("added=%v", client.added)
	}
	if !slices.Equal(client.removed, []string{pendingAutoRisk}) {
		t.Fatalf("removed=%v", client.removed)
	}
}

func TestValidatePullRequestRiskRejectsConfidentResultWithPendingMarker(t *testing.T) {
	client := &fakeRiskClient{labels: []string{riskMedium, pendingAutoRisk}}
	if err := validatePullRequestRisk(context.Background(), client, 42); err == nil {
		t.Fatal("validation unexpectedly succeeded")
	}
	if !slices.Equal(client.added, []string{failedAutoRisk}) {
		t.Fatalf("added=%v", client.added)
	}
	if !slices.Equal(client.removed, []string{riskMedium, pendingAutoRisk}) {
		t.Fatalf("removed=%v", client.removed)
	}
}
