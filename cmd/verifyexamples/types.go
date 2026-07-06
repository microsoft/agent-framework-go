// Copyright (c) Microsoft. All rights reserved.

package main

import "time"

type ExampleDefinition struct {
	Name                         string
	ProjectPath                  string
	RequiredEnvironmentVariables []string
	OptionalEnvironmentVariables []string
	SkipReason                   string
	MustContain                  []string
	MustNotContain               []string
	IsDeterministic              bool
	ExpectedOutputDescription    []string
	Inputs                       []*string
	InputDelay                   time.Duration
}

type ExampleRunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Elapsed  time.Duration
}

type VerificationResult struct {
	ExampleName string
	Passed      bool
	Summary     string
	Failures    []string
	AIReasoning string
	Stdout      string
	Stderr      string
	LogLines    []string
}

type SkippedExample struct {
	Name   string
	Reason string
}

type RunAllResult struct {
	Results      map[string]VerificationResult
	Skipped      []SkippedExample
	ExampleOrder []string
}
