// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// prSpec describes the pull request that a fallback tracking issue is asking a
// human (or this tool) to open on its behalf.
type prSpec struct {
	Base  string // base branch the PR should target (e.g. "main")
	Head  string // head branch that was pushed by the workflow
	Title string // intended PR title (already includes the workflow title-prefix)
	Body  string // original PR description, taken from the top of the issue body
}

// fallbackNoteMarker separates the original PR body from the gh-aw fallback note
// appended by the create-pull-request safe output when PR creation is blocked.
const fallbackNoteMarker = "\n\n---\n\n> [!NOTE]"

var (
	// compareURLRe matches the GitHub compare link embedded in the fallback issue,
	// e.g. https://github.com/OWNER/REPO/compare/main...branch?expand=1&title=...
	compareURLRe = regexp.MustCompile(`https?://[^\s)]+/compare/[^\s)]+`)

	// pushedBranchRe matches the "pushed to branch `<branch>`" line as a secondary
	// source for the head branch.
	pushedBranchRe = regexp.MustCompile("pushed to branch `([^`]+)`")

	// ghPrCreateRe matches the manual "gh pr create" command that some fallback
	// templates (e.g. the push-failed variant) embed instead of a compare URL.
	// The title may be single- or double-quoted; Go's RE2 has no backreferences,
	// so each quote style is matched explicitly.
	ghPrCreateRe = regexp.MustCompile(`gh pr create --title (?:'([^']*)'|"([^"]*)") --base (\S+) --head (\S+)`)
)

// parseFallbackIssue extracts the pull request details from a gh-aw fallback
// tracking issue. issueTitle is used as a fallback PR title when the issue body
// does not carry one.
func parseFallbackIssue(issueBody, issueTitle string) (prSpec, error) {
	spec := prSpec{Body: originalPRBody(issueBody)}

	if base, head, title, ok := parseCompareURL(compareURLRe.FindString(issueBody)); ok {
		spec.Base, spec.Head, spec.Title = base, head, title
	} else if m := ghPrCreateRe.FindStringSubmatch(issueBody); m != nil {
		title := m[1]
		if title == "" {
			title = m[2]
		}
		spec.Title, spec.Base, spec.Head = title, m[3], m[4]
	}

	// Fall back to the "pushed to branch" line when no head was found above.
	if spec.Head == "" {
		if m := pushedBranchRe.FindStringSubmatch(issueBody); m != nil {
			spec.Head = m[1]
		}
	}
	if spec.Title == "" {
		spec.Title = strings.TrimSpace(issueTitle)
	}

	if spec.Head == "" {
		return prSpec{}, fmt.Errorf("could not find a pushed branch or compare link in the issue body")
	}
	if spec.Base == "" {
		return prSpec{}, fmt.Errorf("could not determine the base branch from the issue body")
	}
	if spec.Title == "" {
		return prSpec{}, fmt.Errorf("could not determine a pull request title")
	}
	return spec, nil
}

// parseCompareURL pulls the base branch, head branch, and title out of a GitHub
// compare URL of the form ".../compare/<base>...<head>?expand=1&title=<title>".
func parseCompareURL(raw string) (base, head, title string, ok bool) {
	if raw == "" {
		return "", "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", false
	}
	idx := strings.Index(u.Path, "/compare/")
	if idx < 0 {
		return "", "", "", false
	}
	// net/url has already percent-decoded u.Path and the query values.
	parts := strings.SplitN(u.Path[idx+len("/compare/"):], "...", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], u.Query().Get("title"), true
}

// originalPRBody returns the portion of the issue body that precedes the gh-aw
// fallback note, i.e. the PR description the workflow originally authored.
func originalPRBody(issueBody string) string {
	if idx := strings.Index(issueBody, fallbackNoteMarker); idx >= 0 {
		return strings.TrimSpace(issueBody[:idx])
	}
	return strings.TrimSpace(issueBody)
}

// closingKeywordRe detects an existing issue-closing keyword referencing a
// specific issue number (GitHub recognizes close/closes/closed/fix/fixes/fixed/
// resolve/resolves/resolved).
func closingKeywordRe(issueNumber int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?i)\b(clos(e|es|ed)|fix(es|ed)?|resolv(e|es|ed))\b[^\n#]*#%d\b`, issueNumber))
}

// ensureClosesLine appends "Closes #<issueNumber>" to body when it does not
// already contain a closing keyword for that issue. It reports whether the body
// changed.
func ensureClosesLine(body string, issueNumber int) (string, bool) {
	if closingKeywordRe(issueNumber).MatchString(body) {
		return body, false
	}
	closes := fmt.Sprintf("Closes #%d", issueNumber)
	if strings.TrimSpace(body) == "" {
		return closes, true
	}
	return strings.TrimRight(body, "\n") + "\n\n" + closes, true
}
