// Copyright (c) Microsoft. All rights reserved.

// Command prfromissue opens pull requests for gh-aw fallback tracking issues and
// links the two together.
//
// The agent-framework porting workflows run with a token that cannot open pull
// requests, so gh-aw's create-pull-request safe output pushes the branch and
// files a tracking issue containing a compare link instead. This tool scans open
// issues whose title starts with a given prefix (e.g. "[dotnet-port-api]"),
// opens the pull request from the pushed branch, sets its body to "Closes
// #<issue>", and comments the PR link back on the issue. When a PR already exists
// for the branch it links the two instead of opening a duplicate.
//
// Authentication uses the gh CLI: `gh auth login` locally, or the
// GH_TOKEN / GITHUB_TOKEN environment variable in CI.
//
// Usage:
//
//	go run ./cmd/prfromissue -prefix "[dotnet-port-api]"
//	go run ./cmd/prfromissue -prefix "[dotnet-port-fixes]" -repo microsoft/agent-framework-go
//	go run ./cmd/prfromissue -prefix "[dotnet-port-api]" -dry-run
//	go run ./cmd/prfromissue -prefix "[dotnet-port-api]" -draft=false
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

type options struct {
	prefix string
	repo   string
	limit  int
	draft  bool
	dryRun bool
}

func parseOptions(args []string) (options, bool) {
	fs := flag.NewFlagSet("prfromissue", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts options
	fs.StringVar(&opts.prefix, "prefix", "", "required: only act on open issues whose title starts with this prefix (e.g. \"[dotnet-port-api]\")")
	fs.StringVar(&opts.repo, "repo", "", "target repository as owner/repo (defaults to the repository in the current directory)")
	fs.IntVar(&opts.limit, "limit", 100, "maximum number of open issues to scan")
	fs.BoolVar(&opts.draft, "draft", true, "open the pull request as a draft")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print the actions that would be taken without changing anything")
	if err := fs.Parse(args); err != nil {
		return options{}, false
	}
	if strings.TrimSpace(opts.prefix) == "" {
		fmt.Fprintln(os.Stderr, "error: -prefix is required")
		fs.Usage()
		return options{}, false
	}
	return opts, true
}

func run(args []string) int {
	opts, ok := parseOptions(args)
	if !ok {
		return 2
	}

	client := &ghClient{repo: opts.repo, run: execGH}
	processor := &processor{client: client, out: os.Stdout, draft: opts.draft, dryRun: opts.dryRun}

	ctx := context.Background()
	issues, err := client.listOpenIssues(ctx, opts.limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listing issues: %v\n", err)
		return 1
	}

	matched := 0
	failures := 0
	for _, iss := range issues {
		if !strings.HasPrefix(iss.Title, opts.prefix) {
			continue
		}
		matched++
		if err := processor.handle(ctx, iss); err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "error: issue #%d: %v\n", iss.Number, err)
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "\nScanned %d open issue(s); %d matched prefix %q; %d error(s).\n",
		len(issues), matched, opts.prefix, failures)
	if failures > 0 {
		return 1
	}
	return 0
}
