// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"

	"golang.org/x/exp/apidiff"
)

type goAPIChange struct {
	Message    string `json:"message"`
	Compatible bool   `json:"compatible"`
}

type goChangesSummary struct {
	Old          reconciliationGoInventory `json:"old"`
	New          reconciliationGoInventory `json:"new"`
	Compatible   int                       `json:"compatible_changes"`
	Incompatible int                       `json:"incompatible_changes"`
}

type goChangesReport struct {
	goChangesSummary
	Changes []goAPIChange `json:"changes"`
	Page    pageInfo      `json:"page"`
}

func runGoChanges(out io.Writer, options reportOptions) error {
	oldAPI, err := indexGo(options.oldRoot, options.goPatterns, options.tags)
	if err != nil {
		return fmt.Errorf("index old Go API: %w", err)
	}
	newAPI, err := indexGo(options.goRoot, options.goPatterns, options.tags)
	if err != nil {
		return fmt.Errorf("index new Go API: %w", err)
	}
	if oldAPI.Module != newAPI.Module {
		return fmt.Errorf("cannot compare different Go modules: %q and %q", oldAPI.Module, newAPI.Module)
	}
	for _, setting := range []struct{ name, old, new string }{
		{"GOOS", oldAPI.GOOS, newAPI.GOOS},
		{"GOARCH", oldAPI.GOARCH, newAPI.GOARCH},
		{"CGO_ENABLED", oldAPI.CGOEnabled, newAPI.CGOEnabled},
		{"Go version", oldAPI.GoVersion, newAPI.GoVersion},
		{"build tags", oldAPI.BuildTags, newAPI.BuildTags},
	} {
		if setting.old != setting.new {
			return fmt.Errorf("cannot compare Go APIs with different %s: %q and %q", setting.name, setting.old, setting.new)
		}
	}
	changes := apidiff.ModuleChanges(goAPIModule(oldAPI), goAPIModule(newAPI))
	return writeGoChanges(out, oldAPI, newAPI, changes, options)
}

func goAPIModule(api goInventory) *apidiff.Module {
	module := &apidiff.Module{Path: api.Module}
	// The index also retains dependency types for example validation. Compare
	// only selected SDK packages, not every package loaded to type-check them.
	for importPath, pkg := range api.packages {
		if (importPath == api.Module || strings.HasPrefix(importPath, api.Module+"/")) &&
			slices.Contains(api.Packages, goIndexPackageName(importPath, api.Module)) {
			module.Packages = append(module.Packages, pkg)
		}
	}
	return module
}

func goChangeSnapshot(api goInventory) reconciliationGoInventory {
	return reconciliationGoInventory{
		Module: api.Module, Commit: api.Commit, Dirty: api.Dirty,
		GOOS: api.GOOS, GOARCH: api.GOARCH, GoVersion: api.GoVersion,
		CGOEnabled: api.CGOEnabled, BuildTags: api.BuildTags,
		Packages: api.Packages, ExportedSymbols: len(api.Symbols),
	}
}

func writeGoChanges(out io.Writer, oldAPI, newAPI goInventory, diff apidiff.Report, options reportOptions) error {
	summary := goChangesSummary{Old: goChangeSnapshot(oldAPI), New: goChangeSnapshot(newAPI)}
	changes := make([]goAPIChange, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		if !strings.Contains(strings.ToLower(change.Message), strings.ToLower(options.filter.Symbol)) {
			continue
		}
		changes = append(changes, goAPIChange{Message: change.Message, Compatible: change.Compatible})
		if change.Compatible {
			summary.Compatible++
		} else {
			summary.Incompatible++
		}
	}
	// ModuleChanges walks package maps. Keep output and page boundaries stable.
	slices.SortFunc(changes, func(a, b goAPIChange) int {
		if a.Compatible != b.Compatible {
			if a.Compatible {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Message, b.Message)
	})
	visible, page := pageItems(changes, options.page)
	if options.asJSON {
		if options.brief {
			return writeReportJSON(out, summary)
		}
		return writeReportJSON(out, goChangesReport{goChangesSummary: summary, Changes: visible, Page: page})
	}
	var buffer bytes.Buffer
	fmt.Fprintln(&buffer, "Old Go API:")
	writeGoReportContext(&buffer, summary.Old)
	fmt.Fprintln(&buffer, "New Go API:")
	writeGoReportContext(&buffer, summary.New)
	fmt.Fprintln(&buffer, "Structural API changes only; implementations, documentation, and struct tags are not compared.")
	fmt.Fprintf(&buffer, "Changes: %d compatible; %d incompatible. Neither classification establishes .NET parity.\n", summary.Compatible, summary.Incompatible)
	if !options.brief {
		if err := writePageText(&buffer, page); err != nil {
			return err
		}
		for _, change := range visible {
			kind := "incompatible"
			if change.Compatible {
				kind = "compatible"
			}
			fmt.Fprintf(&buffer, "%s: %s\n", kind, reportCell(change.Message))
		}
	}
	_, err := buffer.WriteTo(out)
	return err
}
