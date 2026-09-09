// Copyright (c) Microsoft. All rights reserved.

package main

import "testing"

func issueWith(title, author string) issue {
	iss := issue{Title: title}
	iss.Author.Login = author
	return iss
}

func TestMatchesAnyPrefix(t *testing.T) {
	prefixes := []string{"[dotnet-port-api]", "[dotnet-code]"}
	if !matchesAnyPrefix("[dotnet-code] Do a thing", prefixes) {
		t.Error("expected [dotnet-code] title to match")
	}
	if matchesAnyPrefix("[dotnet-port-fixes] Fix", prefixes) {
		t.Error("did not expect [dotnet-port-fixes] to match the given set")
	}
	if matchesAnyPrefix("no prefix", prefixes) {
		t.Error("did not expect an unprefixed title to match")
	}
	if !matchesAnyPrefix("anything at all", nil) {
		t.Error("an empty prefix list should match every title")
	}
}

func TestSelectIssues_FiltersByAuthorAndPrefix(t *testing.T) {
	issues := []issue{
		issueWith("[dotnet-port-api] Real port", "app/github-actions"),
		issueWith("[dotnet-code] Real code change", "app/github-actions"),
		// Right prefix, wrong author (e.g. a spoofed issue from a random user).
		issueWith("[dotnet-port-api] Malicious", "randomuser"),
		// Trusted author, but not a porting issue.
		issueWith("Some unrelated issue", "app/github-actions"),
	}

	prefixes := []string{"[dotnet-port-api]", "[dotnet-port-fixes]", "[dotnet-code]"}
	selected := selectIssues(issues, prefixes, defaultIssueAuthor)
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected issues, got %d: %+v", len(selected), selected)
	}
	for _, iss := range selected {
		if iss.Author.Login != "app/github-actions" {
			t.Errorf("selected an issue from untrusted author: %+v", iss)
		}
	}
}

func TestSelectIssues_NoPrefixMatchesAllFromAuthor(t *testing.T) {
	issues := []issue{
		issueWith("[dotnet-port-api] Real port", "app/github-actions"),
		issueWith("Some other automated issue", "app/github-actions"),
		issueWith("[dotnet-code] From a random user", "randomuser"),
	}
	selected := selectIssues(issues, nil, defaultIssueAuthor)
	if len(selected) != 2 {
		t.Fatalf("expected both author-owned issues, got %d: %+v", len(selected), selected)
	}
}

func TestSelectIssues_EmptyAuthorDisablesCheck(t *testing.T) {
	issues := []issue{
		issueWith("[dotnet-code] From a human", "someone"),
	}
	if got := selectIssues(issues, nil, ""); len(got) != 1 {
		t.Fatalf("empty author should disable the check; got %d", len(got))
	}
}

func TestSelectIssues_SinglePrefix(t *testing.T) {
	issues := []issue{
		issueWith("[dotnet-port-api] One", "app/github-actions"),
		issueWith("[dotnet-code] Two", "app/github-actions"),
	}
	got := selectIssues(issues, []string{"[dotnet-code]"}, defaultIssueAuthor)
	if len(got) != 1 || got[0].Title != "[dotnet-code] Two" {
		t.Fatalf("expected only the [dotnet-code] issue, got %+v", got)
	}
}
