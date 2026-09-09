// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// processor turns a single fallback tracking issue into a linked pull request.
type processor struct {
	client *ghClient
	out    io.Writer
	draft  bool
	dryRun bool
}

// handle opens (or links) the pull request described by a fallback issue.
func (p *processor) handle(ctx context.Context, iss issue) error {
	spec, err := parseFallbackIssue(iss.Body, iss.Title)
	if err != nil {
		p.logf("skip #%d %q: %v", iss.Number, iss.Title, err)
		return nil
	}

	existing, err := p.client.openPRForHead(ctx, spec.Head)
	if err != nil {
		return fmt.Errorf("checking for existing pull request on %q: %w", spec.Head, err)
	}
	if existing != nil {
		return p.linkExisting(ctx, iss, *existing)
	}
	return p.openNew(ctx, iss, spec)
}

// openNew creates a new pull request from the pushed branch and links it back to
// the issue.
func (p *processor) openNew(ctx context.Context, iss issue, spec prSpec) error {
	body, _ := ensureClosesLine(spec.Body, iss.Number)

	if p.dryRun {
		p.logf("would open PR for #%d: %s -> %s %q (Closes #%d)",
			iss.Number, spec.Head, spec.Base, spec.Title, iss.Number)
		return nil
	}

	prURL, err := p.client.createPR(ctx, spec, body, p.draft)
	if err != nil {
		return fmt.Errorf("creating pull request from %q: %w", spec.Head, err)
	}
	p.logf("opened %s for #%d (branch %s)", prURL, iss.Number, spec.Head)
	return p.commentLink(ctx, iss.Number, prURL)
}

// linkExisting ensures an already-open pull request and its issue reference each
// other, without opening a duplicate.
func (p *processor) linkExisting(ctx context.Context, iss issue, pr pullRequest) error {
	newBody, changed := ensureClosesLine(pr.Body, iss.Number)
	if p.dryRun {
		if changed {
			p.logf("would add \"Closes #%d\" to existing PR %s and link issue #%d", iss.Number, pr.URL, iss.Number)
		} else {
			p.logf("would link existing PR %s to issue #%d", pr.URL, iss.Number)
		}
		return nil
	}

	if changed {
		if err := p.client.editPRBody(ctx, pr.Number, newBody); err != nil {
			return fmt.Errorf("updating body of pull request #%d: %w", pr.Number, err)
		}
		p.logf("linked existing %s to #%d (added Closes #%d)", pr.URL, iss.Number, iss.Number)
	} else {
		p.logf("existing %s already closes #%d", pr.URL, iss.Number)
	}
	return p.commentLink(ctx, iss.Number, pr.URL)
}

// commentLink posts a comment on the issue pointing at the pull request, unless
// an earlier comment already references it.
func (p *processor) commentLink(ctx context.Context, issueNumber int, prURL string) error {
	bodies, err := p.client.issueCommentBodies(ctx, issueNumber)
	if err != nil {
		return fmt.Errorf("reading comments on issue #%d: %w", issueNumber, err)
	}
	for _, body := range bodies {
		if strings.Contains(body, prURL) {
			return nil // already linked
		}
	}
	comment := fmt.Sprintf("Pull request %s is linked to this issue and will close it when merged.", prURL)
	if err := p.client.commentIssue(ctx, issueNumber, comment); err != nil {
		return fmt.Errorf("commenting on issue #%d: %w", issueNumber, err)
	}
	return nil
}

func (p *processor) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, format+"\n", args...)
}
