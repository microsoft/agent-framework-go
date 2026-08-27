// Copyright (c) Microsoft. All rights reserved.

// This tool runs the examples under examples/ and verifies their output.
// Deterministic examples are verified with exact string matching.
// Non-deterministic LLM examples are verified using a Foundry or GitHub Copilot agent.
//
// Usage:
//   go run ./cmd/verifyexamples                                      # Run all examples
//   go run ./cmd/verifyexamples 01_get_started_05_first_workflow     # Run a specific example by name
//   go run ./cmd/verifyexamples --category 01-get-started            # Run one category
//   go run ./cmd/verifyexamples --parallel 16                        # Run up to 16 examples concurrently
//   go run ./cmd/verifyexamples --log results.log                    # Write sequential log to file
//   go run ./cmd/verifyexamples --csv results.csv                    # Write CSV summary to file
//   go run ./cmd/verifyexamples --md results.md                      # Write Markdown summary to file
//   go run ./cmd/verifyexamples --verifier copilot                   # Verify semantic output with GitHub Copilot
//   go run ./cmd/verifyexamples --build                              # Allow normal module writes during go run

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/copilotprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	start := time.Now()
	repoRoot, err := resolveRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	options, ok := parseOptions(args, os.Stderr)
	if !ok {
		return 1
	}
	ctx := context.Background()

	foundryEndpoint := os.Getenv("FOUNDRY_PROJECT_ENDPOINT")
	foundryModel := os.Getenv("FOUNDRY_MODEL")
	if foundryModel == "" {
		foundryModel = "gpt-4o-mini"
	}

	var verifierAgent semanticVerifier
	supportsStructuredOutput := false
	verifierName := options.Verifier
	if verifierName == "auto" {
		if foundryEndpoint == "" {
			verifierName = "disabled"
		} else {
			verifierName = "foundry"
		}
	}
	switch verifierName {
	case "foundry":
		if foundryEndpoint == "" {
			fmt.Fprintln(os.Stderr, "--verifier foundry requires FOUNDRY_PROJECT_ENDPOINT.")
			return 1
		}
		credential, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create verifier credential: %v\n", err)
			return 1
		}
		verifierAgent = foundryprovider.NewAgent(
			foundryEndpoint,
			credential,
			foundryprovider.ModelDeployment(foundryModel),
			foundryprovider.AgentConfig{
				Instructions: verifierInstructions,
				Config: agent.Config{
					Name: "OutputVerifier",
				},
			},
		)
		supportsStructuredOutput = true
	case "copilot":
		copilotClient := copilot.NewClient(&copilot.ClientOptions{
			GitHubToken: os.Getenv("COPILOT_GITHUB_TOKEN"),
		})
		if err := copilotClient.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to start Copilot verifier client: %v\n", err)
			return 1
		}
		defer func() {
			if err := copilotClient.Stop(); err != nil {
				fmt.Fprintf(os.Stderr, "failed to stop Copilot verifier client: %v\n", err)
			}
		}()
		verifierAgent = copilotprovider.NewAgent(
			copilotClient,
			copilotprovider.AgentConfig{
				SessionConfig: &copilot.SessionConfig{
					AvailableTools:                     []string{},
					EnableConfigDiscovery:              copilot.Bool(false),
					EnableOnDemandInstructionDiscovery: copilot.Bool(false),
					EnableFileHooks:                    copilot.Bool(false),
					EnableHostGitOperations:            copilot.Bool(false),
					EnableSessionStore:                 copilot.Bool(false),
					EnableSkills:                       copilot.Bool(false),
					InfiniteSessions: &copilot.InfiniteSessionConfig{
						Enabled: copilot.Bool(false),
					},
				},
				Instructions: verifierInstructions,
				Config: agent.Config{
					Name: "OutputVerifier",
				},
			},
		)
	}

	var logWriter *LogFileWriter
	if options.LogFilePath != "" {
		logWriter = NewLogFileWriter(options.LogFilePath)
		if err := logWriter.WriteHeader(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	reporter := NewConsoleReporter(os.Stdout)
	fmt.Printf("Semantic verifier: %s\n", verifierName)
	if verifierName == "foundry" {
		fmt.Printf("Foundry endpoint: %s, Model: %s\n", foundryEndpoint, foundryModel)
	}
	orchestrator := VerificationOrchestrator{
		verifier: ExampleVerifier{
			verifierAgent:            verifierAgent,
			supportsStructuredOutput: supportsStructuredOutput,
		},
		reporter:      reporter,
		logWriter:     logWriter,
		repoRoot:      repoRoot,
		timeout:       3 * time.Minute,
		buildExamples: options.BuildExamples,
	}
	run := orchestrator.RunAll(ctx, options.Examples, options.MaxParallelism)
	elapsed := time.Since(start)
	ordered := orderedResults(run)
	reporter.PrintSummary(ordered, run.Skipped, elapsed)

	if logWriter != nil {
		if err := logWriter.WriteSummary(ordered, run.Skipped, elapsed); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Log written to: %s\n", options.LogFilePath)
	}
	if options.CsvFilePath != "" {
		if err := writeCSV(options.CsvFilePath, ordered, run.Skipped, options.Examples); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("CSV written to: %s\n", options.CsvFilePath)
	}
	if options.MarkdownFilePath != "" {
		if err := writeMarkdown(options.MarkdownFilePath, ordered, run.Skipped, elapsed); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Markdown written to: %s\n", options.MarkdownFilePath)
	}

	for _, result := range ordered {
		if !result.Passed {
			return 1
		}
	}
	return 0
}

func resolveRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", wd)
		}
	}
}

const verifierInstructions = `You are a test output verifier. You will be given:
1. The actual stdout output of a program
2. The stderr output, if any
3. A list of expectations about what the output should contain or demonstrate

Determine whether the actual output satisfies each expectation. Be reasonable: output from an LLM will not match exact wording, but the semantic intent should be clearly satisfied.

In your response, return JSON with:
- ai_reasoning: a brief overall assessment
- expectation_results: exactly one entry for each expectation, in the same order
- each entry must echo the expectation text and include detail citing evidence from the output`
