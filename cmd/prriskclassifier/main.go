// Copyright (c) Microsoft. All rights reserved.

// Command prriskclassifier applies deterministic Agent Framework risk rules to
// a pull request and reports whether semantic agent classification is needed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	pendingAutoRisk = "pending-auto-risk"
	failedAutoRisk  = "failed-auto-risk"
)

type options struct {
	repo           string
	prNumber       int
	output         string
	summary        string
	validateResult bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args, getenv, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	client := &ghClient{repo: opts.repo, run: execGH}
	if opts.validateResult {
		if err := validatePullRequestRisk(context.Background(), client, opts.prNumber); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: validate PR #%d risk result: %v\n", opts.prNumber, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "PR #%d: automatic risk result is valid\n", opts.prNumber)
		return 0
	}

	result, err := classifyPullRequest(context.Background(), client, opts.prNumber)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: classify PR #%d: %v\n", opts.prNumber, err)
		return 1
	}
	if err := writeResult(opts, result); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: write result: %v\n", err)
		return 1
	}

	if result.Decision.Label == "" {
		_, _ = fmt.Fprintf(stdout, "PR #%d: deterministic risk inconclusive; agent classification required\n", opts.prNumber)
	} else {
		_, _ = fmt.Fprintf(stdout, "PR #%d: %s (%s)\n", opts.prNumber, result.Decision.Label, result.Decision.Reason)
	}
	return 0
}

func parseOptions(args []string, getenv func(string) string, output io.Writer) (options, error) {
	fs := flag.NewFlagSet("prriskclassifier", flag.ContinueOnError)
	fs.SetOutput(output)
	repo := fs.String("repo", getenv("GITHUB_REPOSITORY"), "target repository in owner/name form")
	prNumber := fs.Int("pr-number", envInt(getenv("PR_NUMBER")), "pull request number")
	resultPath := fs.String("output", getenv("GITHUB_OUTPUT"), "GitHub Actions output file (optional)")
	summaryPath := fs.String("summary", getenv("GITHUB_STEP_SUMMARY"), "GitHub Actions summary file (optional)")
	validateResult := fs.Bool("validate-result", false, "validate and enforce the final automatic risk label state")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	parts := strings.Split(*repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return options{}, fmt.Errorf("repo must be in owner/name form, got %q", *repo)
	}
	if *prNumber <= 0 {
		return options{}, fmt.Errorf("pr-number must be positive")
	}
	return options{
		repo:           *repo,
		prNumber:       *prNumber,
		output:         *resultPath,
		summary:        *summaryPath,
		validateResult: *validateResult,
	}, nil
}

func envInt(value string) int {
	number, _ := strconv.Atoi(value)
	return number
}

type classificationResult struct {
	Decision   deterministicDecision
	NeedsAgent bool
}

func writeResult(opts options, result classificationResult) error {
	if opts.output != "" {
		output := fmt.Sprintf("needs-agent=%t\nrisk-label=%s\n", result.NeedsAgent, result.Decision.Label)
		if err := os.WriteFile(opts.output, []byte(output), 0o600); err != nil {
			return err
		}
	}
	if opts.summary != "" {
		message := "Deterministic rules were inconclusive; semantic risk classification is required."
		if result.Decision.Label != "" {
			message = fmt.Sprintf("Applied `%s`: %s.", result.Decision.Label, result.Decision.Reason)
		}
		if err := os.WriteFile(opts.summary, []byte("### Risk classification\n\n"+message+"\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}
