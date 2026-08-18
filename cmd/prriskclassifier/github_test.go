// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestEnsureLabelsUsesPaginatedREST(t *testing.T) {
	wantArgs := []string{"api", "--paginate", "--slurp", "repos/microsoft/agent-framework-go/labels?per_page=100"}
	client := &ghClient{
		repo: "microsoft/agent-framework-go",
		run: func(_ context.Context, args ...string) (string, error) {
			if !slices.Equal(args, wantArgs) {
				return "", fmt.Errorf("args = %v, want %v", args, wantArgs)
			}
			return `[[{"name":"risk:low"},{"name":"risk:medium"},{"name":"risk:high"},{"name":"pending-auto-risk"},{"name":"failed-auto-risk"}]]`, nil
		},
	}
	if err := client.ensureLabels(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPullRequestLabelsUsesREST(t *testing.T) {
	wantArgs := []string{"api", "repos/microsoft/agent-framework-go/issues/42"}
	client := &ghClient{
		repo: "microsoft/agent-framework-go",
		run: func(_ context.Context, args ...string) (string, error) {
			if !slices.Equal(args, wantArgs) {
				return "", fmt.Errorf("args = %v, want %v", args, wantArgs)
			}
			return `{"labels":[{"name":"risk:medium"},{"name":"kind:code"}]}`, nil
		},
	}
	labels, err := client.pullRequestLabels(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(labels, []string{riskMedium, "kind:code"}) {
		t.Fatalf("labels = %v", labels)
	}
}
