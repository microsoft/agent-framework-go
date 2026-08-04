// Copyright (c) Microsoft. All rights reserved.

package main

import "testing"

// permissionDeniedIssueBody mirrors the body produced by gh-aw's
// pr_permission_denied_fallback.md template (create-pull-request safe output).
const permissionDeniedIssueBody = "Ports the new ChatOptions.Foo field from upstream .NET.\n\n" +
	"- Adds an exported Foo option\n" +
	"- Adds a test\n\n" +
	"---\n\n" +
	"> [!NOTE]\n" +
	"> This was originally intended as a pull request, but GitHub Actions is not permitted to create or approve pull requests in this repository.\n" +
	"> The changes have been pushed to branch `dotnet-port-api-nightly-abc123`.\n" +
	">\n" +
	"> **[Click here to create the pull request](https://github.com/microsoft/agent-framework-go/compare/main...dotnet-port-api-nightly-abc123?expand=1&title=%5Bdotnet-port-api%5D%20Add%20ChatOptions.Foo)**\n\n" +
	"To fix the permissions issue, go to **Settings** → **Actions** → **General** and enable **Allow GitHub Actions to create and approve pull requests**. See also: [gh-aw FAQ](https://example.test/faq)"

func TestParseFallbackIssue_CompareURL(t *testing.T) {
	spec, err := parseFallbackIssue(permissionDeniedIssueBody, "[dotnet-port-api] Add ChatOptions.Foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Base != "main" {
		t.Errorf("base = %q, want main", spec.Base)
	}
	if spec.Head != "dotnet-port-api-nightly-abc123" {
		t.Errorf("head = %q, want dotnet-port-api-nightly-abc123", spec.Head)
	}
	if want := "[dotnet-port-api] Add ChatOptions.Foo"; spec.Title != want {
		t.Errorf("title = %q, want %q", spec.Title, want)
	}
	if want := "Ports the new ChatOptions.Foo field from upstream .NET.\n\n- Adds an exported Foo option\n- Adds a test"; spec.Body != want {
		t.Errorf("body = %q, want %q", spec.Body, want)
	}
}

func TestParseFallbackIssue_GhPrCreateCommand(t *testing.T) {
	body := "Fixes a nil deref.\n\n---\n\n> [!NOTE]\n" +
		"> This was originally intended as a pull request, but the git push operation failed.\n\n" +
		"To create a pull request with the changes:\n\n" +
		"```sh\n" +
		"gh pr create --title 'Fix nil deref' --base main --head dotnet-port-fixes-nightly-xyz --repo microsoft/agent-framework-go\n" +
		"```"
	spec, err := parseFallbackIssue(body, "[dotnet-port-fixes] Fix nil deref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Base != "main" || spec.Head != "dotnet-port-fixes-nightly-xyz" || spec.Title != "Fix nil deref" {
		t.Errorf("got base=%q head=%q title=%q", spec.Base, spec.Head, spec.Title)
	}
}

func TestParseFallbackIssue_TitleFallsBackToIssueTitle(t *testing.T) {
	// Compare URL without a title query parameter.
	body := "Body.\n\n---\n\n> [!NOTE]\n" +
		"> The changes have been pushed to branch `feature-x`.\n" +
		"> **[Click here](https://github.com/o/r/compare/main...feature-x?expand=1)**"
	spec, err := parseFallbackIssue(body, "[dotnet-port-api] From issue title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Title != "[dotnet-port-api] From issue title" {
		t.Errorf("title = %q, want issue title", spec.Title)
	}
}

func TestParseFallbackIssue_NoBranch(t *testing.T) {
	if _, err := parseFallbackIssue("Just a regular issue with no branch.", "Some issue"); err == nil {
		t.Fatal("expected an error when no branch/compare link is present")
	}
}

func TestParseCompareURL(t *testing.T) {
	base, head, title, ok := parseCompareURL("https://github.com/o/r/compare/main...my/feature?expand=1&title=Hello%20World")
	if !ok {
		t.Fatal("expected ok")
	}
	if base != "main" || head != "my/feature" || title != "Hello World" {
		t.Errorf("got base=%q head=%q title=%q", base, head, title)
	}
}

func TestOriginalPRBody(t *testing.T) {
	if got := originalPRBody(permissionDeniedIssueBody); got != "Ports the new ChatOptions.Foo field from upstream .NET.\n\n- Adds an exported Foo option\n- Adds a test" {
		t.Errorf("unexpected body: %q", got)
	}
	if got := originalPRBody("no marker here"); got != "no marker here" {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestEnsureClosesLine(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		issue       int
		wantChanged bool
		wantContain string
	}{
		{"empty body", "", 42, true, "Closes #42"},
		{"appends", "Some description.", 42, true, "Some description.\n\nCloses #42"},
		{"already closes", "Fixes #42 in this PR.", 42, false, "Fixes #42 in this PR."},
		{"different issue", "Closes #7", 42, true, "Closes #7\n\nCloses #42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := ensureClosesLine(tt.body, tt.issue)
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if got != tt.wantContain {
				t.Errorf("body = %q, want %q", got, tt.wantContain)
			}
		})
	}
}
