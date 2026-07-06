// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleDefinitionsHaveUniqueNamesAndExistingPaths(t *testing.T) {
	repoRoot, err := resolveRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, example := range allExamples() {
		if example.Name == "" {
			t.Fatalf("example with path %q has empty name", example.ProjectPath)
		}
		if previousPath, ok := seen[example.Name]; ok {
			t.Fatalf("example name %q is duplicated for %q and %q", example.Name, previousPath, example.ProjectPath)
		}
		seen[example.Name] = example.ProjectPath
		if example.ProjectPath == "" {
			t.Fatalf("example %q has empty project path", example.Name)
		}
		path := filepath.Join(repoRoot, filepath.FromSlash(example.ProjectPath))
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("example %q project path %q is not an existing directory", example.Name, example.ProjectPath)
		}
		if example.SkipReason == "" {
			goFiles, err := filepath.Glob(filepath.Join(path, "*.go"))
			if err != nil {
				t.Fatal(err)
			}
			if len(goFiles) == 0 {
				t.Fatalf("example %q project path %q has no Go files and is not skipped", example.Name, example.ProjectPath)
			}
		}
	}
}
