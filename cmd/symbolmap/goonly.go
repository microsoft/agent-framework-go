// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
)

type goOnlyAssessment struct {
	Note   string `json:"note"`
	Review string `json:"review"`
}

type goOnlyReport struct {
	Baseline baseline                    `json:"baseline"`
	Reviews  map[string]reviewBaseline   `json:"reviews,omitempty"`
	GoOnly   map[string]goOnlyAssessment `json:"go_only"`
	Page     pageInfo                    `json:"page"`
}

// Go-only assessments have no .NET identity and never enter the .NET row counts.
// Reconciliation validates their Go targets across the entire unfiltered catalog.
type goOnlyReconciliation struct {
	Assessed           int      `json:"assessed_symbols"`
	Present            int      `json:"present_symbols"`
	InvalidGoTargets   []string `json:"invalid_go_targets,omitempty"`
	UnindexedGoTargets []string `json:"unindexed_go_targets,omitempty"`
}

func (c catalog) validateGoOnly() error {
	for _, symbol := range slices.Sorted(maps.Keys(c.GoOnly)) {
		assessment := c.GoOnly[symbol]
		if !goTargetPattern.MatchString(symbol) {
			return fmt.Errorf("go_only: invalid qualified Go symbol %q", symbol)
		}
		if strings.TrimSpace(assessment.Note) == "" {
			return fmt.Errorf("go_only %s: note must not be empty", symbol)
		}
		if _, ok := c.Reviews[assessment.Review]; !ok || assessment.Review == "" {
			return fmt.Errorf("go_only %s: review must name an existing review batch", symbol)
		}
	}
	return nil
}

func writeGoOnlyReport(out io.Writer, report mappingsReport, symbol string, paging pageOptions, asJSON bool) error {
	selected := make([]string, 0, len(report.GoOnly))
	for _, name := range slices.Sorted(maps.Keys(report.GoOnly)) {
		if strings.Contains(strings.ToLower(name), strings.ToLower(symbol)) {
			selected = append(selected, name)
		}
	}
	visible, page := pageItems(selected, paging)
	assessments := make(map[string]goOnlyAssessment, len(visible))
	for _, name := range visible {
		assessments[name] = report.GoOnly[name]
	}
	if asJSON {
		return writeReportJSON(out, goOnlyReport{Baseline: report.Baseline, Reviews: report.Reviews, GoOnly: assessments, Page: page})
	}
	table := tabularReportWriter{Writer: tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)}
	table.printf("Go-only assessments: no meaningful .NET counterpart was identified in the recorded review scope.\n")
	table.printf("Target existence requires reconcile; this view only reads the catalog.\n")
	table.page(page)
	table.printf("\nGO SYMBOL\tREVIEW\tNOTE\n")
	for _, name := range visible {
		assessment := assessments[name]
		table.printf("%s\t%s\t%s\n", reportCell(name), reportCell(assessment.Review), reportCell(assessment.Note))
	}
	return table.flush()
}

func reconcileGoOnly(assessments map[string]goOnlyAssessment, api goInventory, packages map[string]bool, allGoPackages bool) *goOnlyReconciliation {
	if len(assessments) == 0 {
		return nil
	}
	result := &goOnlyReconciliation{Assessed: len(assessments)}
	for _, symbol := range slices.Sorted(maps.Keys(assessments)) {
		if _, ok := api.Symbols[symbol]; ok {
			result.Present++
			continue
		}
		pkg, _, _ := strings.Cut(symbol, ".")
		if allGoPackages || packages[pkg] {
			result.InvalidGoTargets = append(result.InvalidGoTargets, symbol)
		} else {
			result.UnindexedGoTargets = append(result.UnindexedGoTargets, symbol)
		}
	}
	return result
}
