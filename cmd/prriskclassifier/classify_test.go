// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main

import "testing"

func TestClassifyDeterministically(t *testing.T) {
	tests := []struct {
		name   string
		files  []string
		labels []string
		want   string
	}{
		{
			name:   "documentation only",
			files:  []string{"docs/guide.md"},
			labels: []string{"size:small", "kind:docs"},
			want:   riskLow,
		},
		{
			name:   "workflow tests only",
			files:  []string{"workflow/inproc/state_test.go"},
			labels: []string{"size:medium", "kind:tests", "area:workflow"},
			want:   riskLow,
		},
		{
			name:   "workflow production code",
			files:  []string{"workflow/inproc/state.go", "workflow/inproc/state_test.go"},
			labels: []string{"size:medium", "kind:code", "kind:tests", "area:workflow"},
			want:   riskHigh,
		},
		{
			name:   "shell tool production code",
			files:  []string{"tool/shelltool/localshell.go"},
			labels: []string{"size:small", "kind:code", "area:tool"},
			want:   riskHigh,
		},
		{
			name:   "tool approval production code",
			files:  []string{"agent/harness/toolapproval/toolapproval.go", "agent/harness/toolapproval/toolapproval_test.go"},
			labels: []string{"size:medium", "kind:code", "kind:tests", "area:agent"},
			want:   riskHigh,
		},
		{
			name:   "concurrency implementation",
			files:  []string{"internal/concurrent/map.go"},
			labels: []string{"size:small", "kind:code", "area:internal"},
			want:   riskHigh,
		},
		{
			name:   "public API needs semantic review",
			files:  []string{"agent/agent.go"},
			labels: []string{"size:small", "kind:docs", "public-api-change"},
		},
		{
			name:   "large documentation needs semantic review",
			files:  []string{"docs/guide.md"},
			labels: []string{"size:xlarge", "kind:docs"},
		},
		{
			name:   "missing size needs semantic review",
			files:  []string{"docs/guide.md"},
			labels: []string{"kind:docs"},
		},
		{
			name:   "failed kind classification needs semantic review",
			files:  []string{"docs/guide.md"},
			labels: []string{"size:small", "kind:docs", "failed-auto-classify"},
		},
		{
			name:   "dependency update needs semantic review",
			files:  []string{"go.mod", "go.sum"},
			labels: []string{"size:small", "kind:dependencies"},
		},
		{
			name:   "contained provider code needs semantic review",
			files:  []string{"provider/openaiprovider/openai.go"},
			labels: []string{"size:small", "kind:code", "area:provider/openai"},
		},
		{
			name:   "CI change needs semantic review",
			files:  []string{".github/workflows/test.yml"},
			labels: []string{"size:small", "kind:ci", "area:github"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyDeterministically(test.files, test.labels)
			if got.Label != test.want {
				t.Fatalf("label = %q, want %q (reason: %q)", got.Label, test.want, got.Reason)
			}
		})
	}
}
