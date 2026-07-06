// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type VerificationOrchestrator struct {
	verifier      ExampleVerifier
	reporter      *ConsoleReporter
	logWriter     *LogFileWriter
	repoRoot      string
	timeout       time.Duration
	buildExamples bool
}

func (o VerificationOrchestrator) RunAll(ctx context.Context, examples []ExampleDefinition, maxParallelism int) RunAllResult {
	var skipped []SkippedExample
	var runnable []ExampleDefinition
	var exampleOrder []string

	for _, example := range examples {
		exampleOrder = append(exampleOrder, example.Name)
		if example.SkipReason != "" {
			skipped = append(skipped, SkippedExample{Name: example.Name, Reason: example.SkipReason})
			o.reporter.WriteLineWithPrefix(example.Name, "SKIPPED — "+example.SkipReason)
			if o.logWriter != nil {
				_ = o.logWriter.WriteSkipped(example.Name, example.SkipReason)
			}
			continue
		}
		if reason := missingEnvironmentReason(example); reason != "" {
			skipped = append(skipped, SkippedExample{Name: example.Name, Reason: reason})
			o.reporter.WriteLineWithPrefix(example.Name, "SKIPPED — "+reason)
			if o.logWriter != nil {
				_ = o.logWriter.WriteSkipped(example.Name, reason)
			}
			continue
		}
		runnable = append(runnable, example)
	}

	o.reporter.WriteLineWithPrefix("runner", fmt.Sprintf("Running %d examples (max %d parallel)...", len(runnable), maxParallelism))
	results := make(map[string]VerificationResult)
	var resultsLock sync.Mutex
	semaphore := make(chan struct{}, maxParallelism)
	var wg sync.WaitGroup
	for _, example := range runnable {
		example := example
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			result := o.runSingle(ctx, example)
			resultsLock.Lock()
			results[example.Name] = result
			resultsLock.Unlock()
		}()
	}
	wg.Wait()
	return RunAllResult{Results: results, Skipped: skipped, ExampleOrder: exampleOrder}
}

func (o VerificationOrchestrator) runSingle(ctx context.Context, example ExampleDefinition) VerificationResult {
	logLines := []string{fmt.Sprintf("[%s] Running...", example.Name)}
	o.reporter.WriteLineWithPrefix(example.Name, "Running...")
	projectPath := filepath.Join(o.repoRoot, filepath.FromSlash(example.ProjectPath))
	run := runExample(ctx, projectPath, o.timeout, o.buildExamples, example.Inputs, example.InputDelay)

	completed := fmt.Sprintf("Completed (%.1fs, exit=%d). Verifying...", run.Elapsed.Seconds(), run.ExitCode)
	logLines = append(logLines, fmt.Sprintf("[%s] %s", example.Name, completed))
	o.reporter.WriteLineWithPrefix(example.Name, completed)

	result := o.verifier.Verify(ctx, example, run)
	if result.Passed {
		logLines = append(logLines, fmt.Sprintf("[%s] PASSED", example.Name))
		o.reporter.WriteLineWithPrefix(example.Name, "PASSED")
	} else {
		logLines = append(logLines, fmt.Sprintf("[%s] FAILED", example.Name))
		o.reporter.WriteLineWithPrefix(example.Name, "FAILED")
		for _, failure := range result.Failures {
			line := "  ✗ " + failure
			logLines = append(logLines, fmt.Sprintf("[%s] %s", example.Name, line))
			o.reporter.WriteLineWithPrefix(example.Name, line)
		}
	}
	if result.AIReasoning != "" {
		line := "  AI: " + truncate(result.AIReasoning, 300)
		logLines = append(logLines, fmt.Sprintf("[%s] %s", example.Name, line))
		o.reporter.WriteLineWithPrefix(example.Name, line)
	}
	result.Stdout = run.Stdout
	result.Stderr = run.Stderr
	result.LogLines = logLines
	if o.logWriter != nil {
		_ = o.logWriter.WriteExampleResult(result)
	}
	return result
}

func missingEnvironmentReason(example ExampleDefinition) string {
	var missingRequired []string
	for _, envName := range example.RequiredEnvironmentVariables {
		if os.Getenv(envName) == "" {
			missingRequired = append(missingRequired, envName)
		}
	}
	var missingOptional []string
	for _, envName := range example.OptionalEnvironmentVariables {
		if os.Getenv(envName) == "" {
			missingOptional = append(missingOptional, envName)
		}
	}
	var reasons []string
	if len(missingRequired) > 0 {
		reasons = append(reasons, "Missing required: "+strings.Join(missingRequired, ", "))
	}
	if len(missingOptional) > 0 {
		reasons = append(reasons, "Missing optional (would cause console prompt hang): "+strings.Join(missingOptional, ", "))
	}
	return strings.Join(reasons, "; ")
}

func orderedResults(run RunAllResult) []VerificationResult {
	results := make([]VerificationResult, 0, len(run.Results))
	for _, name := range run.ExampleOrder {
		result, ok := run.Results[name]
		if ok {
			results = append(results, result)
		}
	}
	return results
}
