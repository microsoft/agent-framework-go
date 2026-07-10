// Copyright (c) Microsoft. All rights reserved.

// Command prfromissue opens pull requests for gh-aw fallback tracking issues and
// links the two together.
//
// The agent-framework porting workflows run with a token that cannot open pull
// requests, so gh-aw's create-pull-request safe output pushes the branch and
// files a tracking issue containing a compare link instead. This tool scans the
// target repository's open issues that those workflows filed, opens the pull
// request from the pushed branch, sets its body to "Closes #<issue>", and
// comments the PR link back on the issue. When a PR already exists for the branch
// it links the two instead of opening a duplicate.
//
// By default it targets microsoft/agent-framework-go and acts on every open issue
// opened by app/github-actions (the account that files the fallback issues);
// issues that do not parse as a fallback are skipped. Use -prefix to narrow to a
// single workflow's issues, or -issue-author to change/disable the author check.
//
// Authentication uses the gh CLI: `gh auth login` locally, or the
// GH_TOKEN / GITHUB_TOKEN environment variable in CI.
//
// Usage:
//
//	go install github.com/microsoft/agent-framework-go/cmd/prfromissue@latest
//	prfromissue                           # open + link PRs for all fallback issues
//	prfromissue -dry-run                  # preview without making changes
//	prfromissue -prefix "[dotnet-code]"   # limit to one workflow's issues
//	prfromissue -draft                    # open the PRs as drafts
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// defaultRepo is the repository prfromissue targets unless -repo overrides it.
// Targeting a fixed repository (rather than inferring one from the current
// directory) lets the tool be `go install`ed and run from anywhere.
const defaultRepo = "microsoft/agent-framework-go"

// defaultIssueAuthor is the account that files the fallback tracking issues. The
// tool only acts on issues opened by this author so that an unrelated issue with
// a matching title cannot drive it to open a pull request.
const defaultIssueAuthor = "app/github-actions"

type options struct {
	prefix      string
	repo        string
	issueAuthor string
	limit       int
	draft       bool
	dryRun      bool
}

func parseOptions(args []string) (options, bool) {
	fs := flag.NewFlagSet("prfromissue", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts options
	fs.StringVar(&opts.prefix, "prefix", "", "only act on issues whose title starts with this prefix; empty processes every matching issue")
	fs.StringVar(&opts.repo, "repo", defaultRepo, "target repository as owner/repo")
	fs.StringVar(&opts.issueAuthor, "issue-author", defaultIssueAuthor, "only act on issues opened by this login; empty disables the author check")
	fs.IntVar(&opts.limit, "limit", 100, "maximum number of open issues to scan")
	fs.BoolVar(&opts.draft, "draft", false, "open the pull request as a draft")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print the actions that would be taken without changing anything")
	if err := fs.Parse(args); err != nil {
		return options{}, false
	}
	return opts, true
}

// matchesAnyPrefix reports whether title starts with any of the given prefixes.
// An empty prefix list matches every title.
func matchesAnyPrefix(title string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(title, p) {
			return true
		}
	}
	return false
}

// selectIssues returns the issues to act on: those opened by author (when set),
// optionally narrowed to titles matching one of the prefixes.
func selectIssues(issues []issue, prefixes []string, author string) []issue {
	var selected []issue
	for _, iss := range issues {
		if author != "" && !strings.EqualFold(iss.Author.Login, author) {
			continue
		}
		if matchesAnyPrefix(iss.Title, prefixes) {
			selected = append(selected, iss)
		}
	}
	return selected
}

func run(args []string) int {
	opts, ok := parseOptions(args)
	if !ok {
		return 2
	}

	var prefixes []string
	if opts.prefix != "" {
		prefixes = []string{opts.prefix}
	}

	client := &ghClient{repo: opts.repo, run: execGH}
	proc := &processor{client: client, out: os.Stdout, draft: opts.draft, dryRun: opts.dryRun}

	ctx := context.Background()
	issues, err := client.listOpenIssues(ctx, opts.limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listing issues: %v\n", err)
		return 1
	}

	selected := selectIssues(issues, prefixes, opts.issueAuthor)
	failures := 0
	for _, iss := range selected {
		if err := proc.handle(ctx, iss); err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "error: issue #%d: %v\n", iss.Number, err)
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "\nScanned %d open issue(s); %d matched; %d error(s).\n",
		len(issues), len(selected), failures)
	if failures > 0 {
		return 1
	}
	return 0
}
