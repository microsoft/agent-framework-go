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

func (f *fakeRiskClient) addLabel(_ context.Context, _ int, label string) error {
	f.added = append(f.added, label)
	return f.addErr[label]
}

func (f *fakeRiskClient) removeLabel(_ context.Context, _ int, label string) error {
	f.removed = append(f.removed, label)
	return nil
}

func TestClassifyPullRequestDeterministicHighReconcilesLabels(t *testing.T) {
	client := &fakeRiskClient{
		files:  []string{"workflow/inproc/state.go"},
		labels: []string{"kind:code", riskLow, failedAutoRisk},
	}
	result, err := classifyPullRequest(context.Background(), client, 42)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Label != riskHigh || result.NeedsAgent {
		t.Fatalf("result = %+v", result)
	}
	if !client.ensured || !slices.Equal(client.added, []string{riskHigh}) {
		t.Fatalf("ensured = %t, added = %v", client.ensured, client.added)
	}
	if !slices.Equal(client.removed, []string{riskLow, failedAutoRisk}) {
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

func TestClassifyPullRequestInconclusivePreservesRiskLabels(t *testing.T) {
	client := &fakeRiskClient{
		files:  []string{"provider/openaiprovider/openai.go"},
		labels: []string{"kind:code", riskMedium},
	}
	result, err := classifyPullRequest(context.Background(), client, 42)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Label != "" || !result.NeedsAgent {
		t.Fatalf("result = %+v", result)
	}
	if len(client.added) != 0 || len(client.removed) != 0 {
		t.Fatalf("added = %v, removed = %v", client.added, client.removed)
	}
}

func TestClassifyPullRequestLabelFailureAddsMarker(t *testing.T) {
	writeErr := errors.New("write denied")
	client := &fakeRiskClient{
		files:  []string{"agent/harness/toolapproval/toolapproval.go"},
		labels: []string{"size:small", "kind:code"},
		addErr: map[string]error{riskHigh: writeErr},
	}
	_, err := classifyPullRequest(context.Background(), client, 42)
	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want write failure", err)
	}
	if !slices.Equal(client.added, []string{riskHigh, failedAutoRisk}) {
		t.Fatalf("added = %v", client.added)
	}
}
