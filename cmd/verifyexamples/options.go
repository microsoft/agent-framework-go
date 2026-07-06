// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

type VerifyOptions struct {
	MaxParallelism   int
	CsvFilePath      string
	MarkdownFilePath string
	LogFilePath      string
	BuildExamples    bool
	Examples         []ExampleDefinition
}

func parseOptions(args []string, stderr io.Writer) (VerifyOptions, bool) {
	argList := slices.Clone(args)
	categoryFilter, ok := extractArg(&argList, "--category", stderr)
	if !ok {
		return VerifyOptions{}, false
	}
	logFilePath, ok := extractArg(&argList, "--log", stderr)
	if !ok {
		return VerifyOptions{}, false
	}
	csvFilePath, ok := extractArg(&argList, "--csv", stderr)
	if !ok {
		return VerifyOptions{}, false
	}
	markdownFilePath, ok := extractArg(&argList, "--md", stderr)
	if !ok {
		return VerifyOptions{}, false
	}
	buildExamples := extractFlag(&argList, "--build")

	maxParallelism := 8
	parallelArg, ok := extractArg(&argList, "--parallel", stderr)
	if !ok {
		return VerifyOptions{}, false
	}
	if parallelArg != "" {
		p, err := strconv.Atoi(parallelArg)
		if err != nil || p <= 0 {
			_, _ = fmt.Fprintf(stderr, "Invalid value for --parallel: %s.\n", parallelArg)
			return VerifyOptions{}, false
		}
		maxParallelism = p
	}

	var examples []ExampleDefinition
	if categoryFilter != "" {
		categoryList, ok := lookupExampleSet(categoryFilter)
		if !ok {
			_, _ = fmt.Fprintf(stderr, "Unknown category %q. Available: %s\n", categoryFilter, strings.Join(availableCategories(), ", "))
			return VerifyOptions{}, false
		}
		examples = categoryList
	} else {
		examples = allExamples()
	}

	if len(argList) > 0 {
		nameFilter := make(map[string]bool, len(argList))
		for _, name := range argList {
			nameFilter[strings.ToLower(name)] = true
		}

		var filtered []ExampleDefinition
		for _, example := range examples {
			if nameFilter[strings.ToLower(example.Name)] {
				filtered = append(filtered, example)
			}
		}
		examples = filtered
	}

	if len(examples) == 0 {
		var names []string
		for _, example := range allExamples() {
			names = append(names, example.Name)
		}
		_, _ = fmt.Fprintf(stderr, "No matching examples found. Available: %s\n", strings.Join(names, ", "))
		return VerifyOptions{}, false
	}

	return VerifyOptions{
		MaxParallelism:   maxParallelism,
		CsvFilePath:      csvFilePath,
		MarkdownFilePath: markdownFilePath,
		LogFilePath:      logFilePath,
		BuildExamples:    buildExamples,
		Examples:         examples,
	}, true
}

func extractArg(args *[]string, flag string, stderr io.Writer) (string, bool) {
	for i, arg := range *args {
		if arg != flag {
			continue
		}
		if i+1 >= len(*args) {
			_, _ = fmt.Fprintf(stderr, "Missing value for %s.\n", flag)
			*args = append((*args)[:i], (*args)[i+1:]...)
			return "", false
		}
		value := (*args)[i+1]
		*args = append((*args)[:i], (*args)[i+2:]...)
		return value, true
	}
	return "", true
}

func extractFlag(args *[]string, flag string) bool {
	for i, arg := range *args {
		if arg == flag {
			*args = append((*args)[:i], (*args)[i+1:]...)
			return true
		}
	}
	return false
}
