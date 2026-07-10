// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// commandRunner runs the gh CLI with the given arguments and returns its stdout.
// It is a seam so tests can stub gh without spawning a real process.
type commandRunner func(ctx context.Context, args ...string) (string, error)

// execGH runs the real gh binary. gh authenticates from `gh auth login`
// locally, or from the GH_TOKEN / GITHUB_TOKEN environment variable in CI.
func execGH(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("gh %s: %s", strings.Join(redactGHArgs(args), " "), msg)
	}
	return stdout.String(), nil
}

func redactGHArgs(args []string) []string {
	redacted := append([]string(nil), args...)
	for i, arg := range redacted {
		switch {
		case arg == "--body" && i+1 < len(redacted):
			redacted[i+1] = "<redacted>"
		case strings.HasPrefix(arg, "--body="):
			redacted[i] = "--body=<redacted>"
		}
	}
	return redacted
}

// ghClient wraps the gh CLI, scoped to a single repository when repo is set.
type ghClient struct {
	repo string // owner/repo; empty means "infer from the current directory"
	run  commandRunner
}

// issue is the subset of issue fields the tool reads.
type issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

// pullRequest is the subset of PR fields the tool reads.
type pullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

// withRepo prepends "--repo <repo>" to args when a repository is configured.
func (c *ghClient) withRepo(args ...string) []string {
	if c.repo == "" {
		return args
	}
	return append([]string{"--repo", c.repo}, args...)
}

// listOpenIssues returns up to limit open issues, newest first.
func (c *ghClient) listOpenIssues(ctx context.Context, limit int) ([]issue, error) {
	out, err := c.run(ctx, c.withRepo(
		"issue", "list",
		"--state", "open",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,body,url,author",
	)...)
	if err != nil {
		return nil, err
	}
	var issues []issue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("parse issue list: %w", err)
	}
	return issues, nil
}

// openPRForHead returns the open pull request whose head branch is head, or nil
// when no open PR exists for that branch.
func (c *ghClient) openPRForHead(ctx context.Context, head string) (*pullRequest, error) {
	out, err := c.run(ctx, c.withRepo(
		"pr", "list",
		"--state", "open",
		"--head", head,
		"--json", "number,url,body",
	)...)
	if err != nil {
		return nil, err
	}
	var prs []pullRequest
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("parse pr list: %w", err)
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

// createPR opens a pull request and returns its URL.
func (c *ghClient) createPR(ctx context.Context, spec prSpec, body string, draft bool) (string, error) {
	args := []string{
		"pr", "create",
		"--base", spec.Base,
		"--head", spec.Head,
		"--title", spec.Title,
		"--body", body,
	}
	if draft {
		args = append(args, "--draft")
	}
	out, err := c.run(ctx, c.withRepo(args...)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// editPRBody replaces the body of an existing pull request.
func (c *ghClient) editPRBody(ctx context.Context, number int, body string) error {
	_, err := c.run(ctx, c.withRepo(
		"pr", "edit", fmt.Sprintf("%d", number), "--body", body,
	)...)
	return err
}

// commentIssue adds a comment to an issue.
func (c *ghClient) commentIssue(ctx context.Context, number int, body string) error {
	_, err := c.run(ctx, c.withRepo(
		"issue", "comment", fmt.Sprintf("%d", number), "--body", body,
	)...)
	return err
}

// issueCommentBodies returns the bodies of every comment on an issue. It is used
// to avoid posting a duplicate link comment.
func (c *ghClient) issueCommentBodies(ctx context.Context, number int) ([]string, error) {
	out, err := c.run(ctx, c.withRepo(
		"issue", "view", fmt.Sprintf("%d", number), "--json", "comments",
	)...)
	if err != nil {
		return nil, err
	}
	var view struct {
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return nil, fmt.Errorf("parse issue comments: %w", err)
	}
	bodies := make([]string, 0, len(view.Comments))
	for _, comment := range view.Comments {
		bodies = append(bodies, comment.Body)
	}
	return bodies, nil
}
