// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeGH is an in-memory stand-in for the gh CLI. It dispatches on the leading
// subcommand and records mutating calls so tests can assert on them.
type fakeGH struct {
	existingPR *pullRequest // returned by "pr list"; nil means no open PR
	comments   []string     // returned by "issue view --json comments"
	createURL  string       // URL printed by "pr create"
	calls      []string     // recorded "pr create" / "pr edit" / "issue comment" calls
}

func (f *fakeGH) run(_ context.Context, args ...string) (string, error) {
	switch {
	case args[0] == "pr" && args[1] == "list":
		if f.existingPR != nil {
			b, _ := json.Marshal([]pullRequest{*f.existingPR})
			return string(b), nil
		}
		return "[]", nil
	case args[0] == "pr" && args[1] == "create":
		f.calls = append(f.calls, "pr create: "+strings.Join(args, " "))
		return f.createURL + "\n", nil
	case args[0] == "pr" && args[1] == "edit":
		f.calls = append(f.calls, "pr edit: "+strings.Join(args, " "))
		return "", nil
	case args[0] == "issue" && args[1] == "comment":
		f.calls = append(f.calls, "issue comment: "+strings.Join(args, " "))
		return "", nil
	case args[0] == "issue" && args[1] == "view":
		view := struct {
			Comments []struct {
				Body string `json:"body"`
			} `json:"comments"`
		}{}
		for _, c := range f.comments {
			view.Comments = append(view.Comments, struct {
				Body string `json:"body"`
			}{Body: c})
		}
		b, _ := json.Marshal(view)
		return string(b), nil
	}
	return "", fmt.Errorf("unexpected gh call: %s", strings.Join(args, " "))
}

func newProcessor(fake *fakeGH, dryRun bool) (*processor, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &processor{
		client: &ghClient{run: fake.run},
		out:    out,
		draft:  false,
		dryRun: dryRun,
	}, out
}

func callsContaining(calls []string, prefix string) []string {
	var matched []string
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			matched = append(matched, c)
		}
	}
	return matched
}

func TestHandle_OpensAndLinksNewPR(t *testing.T) {
	fake := &fakeGH{createURL: "https://github.com/microsoft/agent-framework-go/pull/101"}
	proc, out := newProcessor(fake, false)

	iss := issue{Number: 42, Title: "[dotnet-port-api] Add ChatOptions.Foo", Body: permissionDeniedIssueBody}
	if err := proc.handle(context.Background(), iss); err != nil {
		t.Fatalf("handle: %v", err)
	}

	creates := callsContaining(fake.calls, "pr create:")
	if len(creates) != 1 {
		t.Fatalf("expected 1 pr create call, got %d (%v)", len(creates), fake.calls)
	}
	create := creates[0]
	for _, want := range []string{"--base main", "--head dotnet-port-api-nightly-abc123", "Closes #42"} {
		if !strings.Contains(create, want) {
			t.Errorf("pr create call missing %q: %s", want, create)
		}
	}
	if strings.Contains(create, "--draft") {
		t.Errorf("pr create call should not be a draft by default: %s", create)
	}
	comments := callsContaining(fake.calls, "issue comment:")
	if len(comments) != 1 {
		t.Fatalf("expected 1 issue comment call, got %d (%v)", len(comments), fake.calls)
	}
	if !strings.Contains(comments[0], "https://github.com/microsoft/agent-framework-go/pull/101") {
		t.Errorf("comment missing PR URL: %s", comments[0])
	}
	if !strings.Contains(out.String(), "opened https://github.com/microsoft/agent-framework-go/pull/101") {
		t.Errorf("unexpected log output: %s", out.String())
	}
}

func TestHandle_DraftOpensDraftPR(t *testing.T) {
	fake := &fakeGH{createURL: "https://github.com/microsoft/agent-framework-go/pull/101"}
	proc, _ := newProcessor(fake, false)
	proc.draft = true

	iss := issue{Number: 42, Title: "[dotnet-port-api] Add ChatOptions.Foo", Body: permissionDeniedIssueBody}
	if err := proc.handle(context.Background(), iss); err != nil {
		t.Fatalf("handle: %v", err)
	}
	creates := callsContaining(fake.calls, "pr create:")
	if len(creates) != 1 || !strings.Contains(creates[0], "--draft") {
		t.Errorf("expected a draft pr create call, got %v", fake.calls)
	}
}

func TestHandle_LinksExistingPR(t *testing.T) {
	fake := &fakeGH{
		existingPR: &pullRequest{
			Number: 55,
			URL:    "https://github.com/microsoft/agent-framework-go/pull/55",
			Body:   "Original PR body without a closing keyword.",
		},
	}
	proc, _ := newProcessor(fake, false)

	iss := issue{Number: 42, Title: "[dotnet-port-api] Add ChatOptions.Foo", Body: permissionDeniedIssueBody}
	if err := proc.handle(context.Background(), iss); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(callsContaining(fake.calls, "pr create:")) != 0 {
		t.Errorf("should not open a new PR when one already exists: %v", fake.calls)
	}
	edits := callsContaining(fake.calls, "pr edit:")
	if len(edits) != 1 {
		t.Fatalf("expected 1 pr edit call, got %d (%v)", len(edits), fake.calls)
	}
	if !strings.Contains(edits[0], "Closes #42") {
		t.Errorf("pr edit should add Closes #42: %s", edits[0])
	}
	if len(callsContaining(fake.calls, "issue comment:")) != 1 {
		t.Errorf("expected the issue to be linked with a comment: %v", fake.calls)
	}
}

func TestHandle_ExistingPRAlreadyLinked(t *testing.T) {
	fake := &fakeGH{
		existingPR: &pullRequest{
			Number: 55,
			URL:    "https://github.com/microsoft/agent-framework-go/pull/55",
			Body:   "Body.\n\nCloses #42",
		},
		comments: []string{"Pull request https://github.com/microsoft/agent-framework-go/pull/55 is linked to this issue and will close it when merged."},
	}
	proc, _ := newProcessor(fake, false)

	iss := issue{Number: 42, Title: "[dotnet-port-api] Add ChatOptions.Foo", Body: permissionDeniedIssueBody}
	if err := proc.handle(context.Background(), iss); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(fake.calls) != 0 {
		t.Errorf("expected no mutating calls when already linked, got %v", fake.calls)
	}
}

func TestHandle_DryRunMakesNoChanges(t *testing.T) {
	fake := &fakeGH{createURL: "https://github.com/o/r/pull/1"}
	proc, out := newProcessor(fake, true)

	iss := issue{Number: 42, Title: "[dotnet-port-api] Add ChatOptions.Foo", Body: permissionDeniedIssueBody}
	if err := proc.handle(context.Background(), iss); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("dry run should not mutate, got %v", fake.calls)
	}
	if !strings.Contains(out.String(), "would open PR for #42") {
		t.Errorf("unexpected dry-run output: %s", out.String())
	}
}

func TestHandle_UnparseableIssueIsSkipped(t *testing.T) {
	fake := &fakeGH{}
	proc, out := newProcessor(fake, false)

	iss := issue{Number: 7, Title: "[dotnet-port-api] not a fallback issue", Body: "no branch here"}
	if err := proc.handle(context.Background(), iss); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("expected no calls for an unparseable issue, got %v", fake.calls)
	}
	if !strings.Contains(out.String(), "skip #7") {
		t.Errorf("expected a skip log line, got: %s", out.String())
	}
}
