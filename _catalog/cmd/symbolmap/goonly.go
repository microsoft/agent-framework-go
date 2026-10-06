// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
)

type goOnlyReport struct {
	Baseline symbolcatalog.Baseline                    `json:"baseline"`
	GoOnly   map[string]symbolcatalog.GoOnlyAssessment `json:"go_only"`
	Page     pageInfo                                  `json:"page"`
}

// Go-only assessments have no .NET identity and never enter the .NET row counts.
// Reconciliation validates their Go targets across the entire unfiltered catalog.
type goOnlyReconciliation struct {
	Assessed           int      `json:"assessed_symbols"`
	Present            int      `json:"present_symbols"`
	InvalidGoTargets   []string `json:"invalid_go_targets,omitempty"`
	UnindexedGoTargets []string `json:"unindexed_go_targets,omitempty"`
}

func writeGoOnlyReport(out io.Writer, report mappingsReport, symbol string, paging pageOptions, asJSON bool) error {
	selected := make([]string, 0, len(report.GoOnly))
	for _, name := range slices.Sorted(maps.Keys(report.GoOnly)) {
		if strings.Contains(strings.ToLower(name), strings.ToLower(symbol)) {
			selected = append(selected, name)
		}
	}
	visible, page := pageItems(selected, paging)
	assessments := make(map[string]symbolcatalog.GoOnlyAssessment, len(visible))
	for _, name := range visible {
		assessments[name] = report.GoOnly[name]
	}
	if asJSON {
		return writeReportJSON(out, goOnlyReport{Baseline: report.Baseline, GoOnly: assessments, Page: page})
	}
	table := tabularReportWriter{Writer: tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)}
	table.printf("Go-only assessments: no meaningful .NET counterpart was identified in the catalog's scope.\n")
	table.printf("Target existence requires reconcile; this view only reads the catalog.\n")
	table.page(page)
	table.printf("\nGO SYMBOL\tNOTE\n")
	for _, name := range visible {
		assessment := assessments[name]
		table.printf("%s\t%s\n", reportCell(name), reportCell(assessment.Note))
	}
	return table.flush()
}

func reconcileGoOnly(assessments map[string]symbolcatalog.GoOnlyAssessment, api goInventory, packages map[string]bool, allGoPackages bool) *goOnlyReconciliation {
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
