// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
)

const defaultPageLimit = 20

type pageOptions struct {
	Limit  int
	Offset int
}

type pageInfo struct {
	Total      int  `json:"total"`
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	Returned   int  `json:"returned"`
	NextOffset *int `json:"next_offset,omitempty"`
}

// Page after filtering so total and next_offset describe the selected rows.
func pageItems[T any](items []T, options pageOptions) ([]T, pageInfo) {
	start := min(options.Offset, len(items))
	end := len(items)
	if options.Limit > 0 && options.Limit < end-start {
		end = start + options.Limit
	}
	page := pageInfo{Total: len(items), Offset: options.Offset, Limit: options.Limit, Returned: end - start}
	if end < len(items) {
		page.NextOffset = new(end)
	}
	return items[start:end], page
}

func writePageText(out io.Writer, page pageInfo) error {
	if _, err := fmt.Fprintf(out, "Page: offset %d; returned %d of %d matches", page.Offset, page.Returned, page.Total); err != nil {
		return err
	}
	if page.NextOffset != nil {
		if _, err := fmt.Fprintf(out, "; next offset %d", *page.NextOffset); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, ".")
	return err
}

// Retain the first write failure until the table is flushed to the output.
type tabularReportWriter struct {
	*tabwriter.Writer
	err error
}

func (w *tabularReportWriter) printf(format string, args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.Writer, format, args...)
	}
}

func (w *tabularReportWriter) page(page pageInfo) {
	if w.err == nil {
		w.err = writePageText(w.Writer, page)
	}
}

func (w *tabularReportWriter) flush() error {
	if w.err != nil {
		return w.err
	}
	return w.Flush()
}

func writeReportJSON(out io.Writer, value any) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	n, err := out.Write(buffer.Bytes())
	if err == nil && n != buffer.Len() {
		err = io.ErrShortWrite
	}
	return err
}

func writeMappingReport(out io.Writer, report mappingsReport, filter reportFilter, view string, pageOptions pageOptions, asJSON bool) error {
	typeFilter, symbolFilter := strings.ToLower(filter.Type), strings.ToLower(filter.Symbol)
	namespaceFilter := strings.ToLower(filter.Namespace)
	selected := make([]mappingRow, 0)
	for _, row := range report.Mappings {
		if (filter.Area != "" && row.Area != filter.Area) || (filter.Kind != "" && row.Kind != filter.Kind) || (filter.Status != "" && row.Status != filter.Status) {
			continue
		}
		if view == "gaps" && row.Status != "partial" && row.Status != "unmapped" {
			continue
		}
		if !strings.Contains(strings.ToLower(row.typeName), typeFilter) {
			continue
		}
		if !strings.Contains(strings.ToLower(row.Namespace), namespaceFilter) {
			continue
		}
		if !strings.Contains(strings.ToLower(row.typeName+"."+row.Member), symbolFilter) &&
			!strings.Contains(strings.ToLower(mappingExample(row.Go)), symbolFilter) && !slices.ContainsFunc(row.GoSymbols, func(target string) bool {
			return strings.Contains(strings.ToLower(target), symbolFilter)
		}) {
			continue
		}
		selected = append(selected, row)
	}
	report.Mappings = selected
	if view != "summary" {
		visible, page := pageItems(report.Mappings, pageOptions)
		report.Mappings, report.Page = visible, &page
	}
	if asJSON {
		var value any = report
		if view == "summary" {
			value = summarize(report)
		}
		return writeReportJSON(out, value)
	}
	table := tabularReportWriter{Writer: tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)}
	table.printf("Baseline: checked %s; .NET %s; Go %s.\n", report.Baseline.CheckedAt, report.Baseline.DotnetCommit, report.Baseline.GoCommit)
	if *report.Baseline.InventoryComplete {
		table.printf("Complete inventory as declared by the catalog; counts reflect the selected filters.\n")
	} else {
		table.printf("Incomplete inventory: only explicitly listed symbols are counted.\n")
	}
	table.printf("No behavioral parity audit: mapped means a counterpart, not semantic parity.\n")
	table.printf("Adapted means a Go idiom, not a gap; gaps include only partial and unmapped symbols.\n")
	if report.Page != nil {
		table.page(*report.Page)
	}
	if view == "summary" {
		s := summarize(report)
		table.printf("\n.NET symbols: %d; distinct Go symbols: %d. Counts are not a parity percentage.\n", s.DotnetSymbols, s.GoSymbols)
		for _, group := range []struct {
			name   string
			values []string
			counts map[string]int
		}{
			{"STATUS", statuses, s.ByStatus},
			{"KIND", slices.Sorted(maps.Keys(s.ByKind)), s.ByKind},
			{"AREA", areas, s.ByArea},
		} {
			table.printf("\n%s\tCOUNT\n", group.name)
			for _, value := range group.values {
				table.printf("%s\t%d\n", value, group.counts[value])
			}
		}
	} else {
		table.printf("\nAREA\tKIND\tSTATUS\tNAMESPACE\t.NET SYMBOL\tGO EXAMPLE\tGO SYMBOLS\tNOTE\n")
		for _, row := range report.Mappings {
			targets := strings.Join(row.GoSymbols, ", ")
			if targets == "" {
				targets = "-"
			}
			example := mappingExample(row.Go)
			if example == "" {
				example = "-"
			}
			table.printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Area, row.Kind, row.Status, row.Namespace, row.Dotnet, example, targets, row.Note)
		}
	}
	return table.flush()
}

func writeGoInventory(out io.Writer, api goInventory, symbol string, pageOptions pageOptions, asJSON, brief bool) error {
	total := len(api.Symbols)
	symbol = strings.ToLower(symbol)
	selected := make(map[string]goSymbol)
	for name, declaration := range api.Symbols {
		if strings.Contains(strings.ToLower(name), symbol) {
			selected[name] = declaration
		}
	}
	matched := len(selected)
	api.Symbols = selected
	if !brief {
		visible, page := pageItems(slices.Sorted(maps.Keys(selected)), pageOptions)
		api.Symbols = make(map[string]goSymbol, len(visible))
		for _, name := range visible {
			api.Symbols[name] = selected[name]
		}
		api.Page = &page
	}
	// Packages and build provenance describe the complete index, even when
	// the symbol filter limits the declarations or exported_symbols count.
	metadata := reconciliationGoInventory{
		Module: api.Module, Commit: api.Commit, Dirty: api.Dirty,
		GOOS: api.GOOS, GOARCH: api.GOARCH, GoVersion: api.GoVersion,
		CGOEnabled: api.CGOEnabled, BuildTags: api.BuildTags,
		Packages: api.Packages, ExportedSymbols: matched,
	}
	if asJSON {
		if brief {
			return writeReportJSON(out, metadata)
		}
		return writeReportJSON(out, api)
	}

	var buffer bytes.Buffer
	writeGoReportContext(&buffer, metadata)
	fmt.Fprintf(&buffer, "Exported symbols: %d selected of %d; indexed packages: %d (unfiltered).\n", matched, total, len(api.Packages))
	if api.Page != nil {
		if err := writePageText(&buffer, *api.Page); err != nil {
			return err
		}
	}
	if !brief {
		table := tabularReportWriter{Writer: tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)}
		table.printf("\nSYMBOL\tKIND\tSIGNATURE\n")
		for _, name := range slices.Sorted(maps.Keys(api.Symbols)) {
			declaration := api.Symbols[name]
			table.printf("%s\t%s\t%s\n", reportCell(name), reportCell(declaration.Kind), reportCell(declaration.Signature))
		}
		if err := table.flush(); err != nil {
			return err
		}
	}
	_, err := buffer.WriteTo(out)
	return err
}

type reportFilter struct {
	Area      string
	Kind      string
	Status    string
	Type      string
	Symbol    string
	Namespace string
	State     string
}

// Keep summary metadata explicit so rows is absent, rather than null or empty.
type reconciliationSummary struct {
	Baseline              baseline                  `json:"baseline"`
	Reviews               map[string]reviewBaseline `json:"reviews,omitempty"`
	Inventory             reconciliationInventory   `json:"inventory"`
	Go                    reconciliationGoInventory `json:"go"`
	InventoryDeclarations int                       `json:"inventory_declarations"`
	AssessedDeclarations  int                       `json:"assessed_declarations"`
	Counts                map[string]int            `json:"counts"`
	ByArea                map[string]map[string]int `json:"by_area"`
}

func writeReconciliation(out io.Writer, report reconciliationReport, filter reportFilter, pageOptions pageOptions, asJSON, brief, check bool) error {
	total, failures := len(report.Rows), 0
	typeFilter := strings.ToLower(filter.Type)
	symbolFilter := strings.ToLower(filter.Symbol)
	namespaceFilter := strings.ToLower(filter.Namespace)
	selected := make([]reconciliationRow, 0, total)
	for _, row := range report.Rows {
		// Check the entire input, including invalid targets on rows whose
		// primary state describes a .NET resolution or scope issue.
		if row.State == "needs-reconciliation" || row.State == "invalid-go-target" || len(row.InvalidGoTargets) != 0 {
			failures++
		}
		if filter.Area != "" && row.Area != filter.Area ||
			filter.Kind != "" && row.Kind != filter.Kind ||
			filter.Status != "" && row.Status != filter.Status ||
			filter.State != "" && row.State != filter.State {
			continue
		}
		if !strings.Contains(strings.ToLower(row.Namespace), namespaceFilter) {
			continue
		}
		qualifiedType := row.Type
		if row.Namespace != "" {
			qualifiedType = row.Namespace + "." + qualifiedType
		}
		if !strings.Contains(strings.ToLower(qualifiedType), typeFilter) {
			continue
		}
		qualifiedSymbol := qualifiedType
		if row.Member != "" {
			qualifiedSymbol += "." + row.Member
		}
		if !strings.Contains(strings.ToLower(row.Dotnet), symbolFilter) &&
			!strings.Contains(strings.ToLower(qualifiedSymbol), symbolFilter) &&
			!strings.Contains(strings.ToLower(mappingExample(row.Go)), symbolFilter) &&
			!slices.ContainsFunc(row.GoSymbols, func(target string) bool {
				return strings.Contains(strings.ToLower(target), symbolFilter)
			}) {
			continue
		}
		selected = append(selected, row)
	}
	// Replace only selection-dependent data. Inventory totals and both
	// inventories' provenance still describe the complete input.
	report.Rows = selected
	summarizeReconciliation(&report)
	if !brief {
		visible, page := pageItems(report.Rows, pageOptions)
		report.Rows, report.Page = visible, &page
	}

	var err error
	if asJSON {
		if brief {
			err = writeReportJSON(out, reconciliationSummary{
				Baseline: report.Baseline, Reviews: report.Reviews,
				Inventory: report.Inventory, Go: report.Go,
				InventoryDeclarations: report.InventoryDeclarations,
				AssessedDeclarations:  report.AssessedDeclarations,
				Counts:                report.Counts,
				ByArea:                report.ByArea,
			})
		} else {
			err = writeReportJSON(out, report)
		}
	} else {
		err = writeReconciliationText(out, report, brief)
	}
	if err != nil {
		return err
	}
	if check && failures != 0 {
		return fmt.Errorf("reconciliation check failed: %d of %d unfiltered rows need reconciliation or have invalid Go targets", failures, total)
	}
	return nil
}

func writeReconciliationText(out io.Writer, report reconciliationReport, brief bool) error {
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "Baseline: checked %s; .NET source commit %s; Go commit %s.\n", reportCell(report.Baseline.CheckedAt), reportCell(report.Baseline.DotnetCommit), reportCell(report.Baseline.GoCommit))
	fmt.Fprintf(&buffer, "Baseline scope: %s\n", reportCell(report.Baseline.Scope))
	for _, id := range slices.Sorted(maps.Keys(report.Reviews)) {
		review := report.Reviews[id]
		fmt.Fprintf(&buffer, "Review %s: checked %s; .NET source commit %s; Go commit %s; inventory SHA256 %s.\n", reportCell(id), reportCell(review.CheckedAt), reportCell(review.DotnetCommit), reportCell(review.GoCommit), reportCell(review.InventorySHA256))
		fmt.Fprintf(&buffer, "  Scope: %s\n", reportCell(review.Scope))
	}
	fmt.Fprintf(&buffer, ".NET inventory: schema %d; identity %s; SHA256 %s.\n", report.Inventory.SchemaVersion, reportCell(report.Inventory.IdentityFormat), reportCell(report.Inventory.SHA256))
	namespaces := strings.Join(report.Inventory.Selection.Namespaces, ", ")
	if namespaces == "" {
		namespaces = "(all)"
	}
	fmt.Fprintf(&buffer, ".NET selection: namespaces %s; include protected %t.\n", reportCell(namespaces), report.Inventory.Selection.IncludeProtected)
	for _, name := range slices.Sorted(maps.Keys(report.Inventory.Packages)) {
		pkg := report.Inventory.Packages[name]
		fmt.Fprintf(&buffer, ".NET package %s: version %s; source commit %s; framework %s; asset group %s.\n", reportCell(name), reportCell(pkg.Version), reportCell(pkg.Commit), reportCell(pkg.Framework), reportCell(pkg.AssetGroup))
	}
	inv := declarationInventory{Packages: report.Inventory.Packages, Assemblies: report.Inventory.Assemblies}
	for _, name := range slices.Sorted(maps.Keys(report.Inventory.Assemblies)) {
		assembly := report.Inventory.Assemblies[name]
		fmt.Fprintf(&buffer, ".NET assembly %s: version %s; informational version %s; source commit %s; framework %s.\n", reportCell(name), reportCell(assembly.Version), reportCell(assembly.InformationalVersion), reportCell(declarationSourceCommit(inv, name)), reportCell(assembly.TargetFramework))
	}
	writeGoReportContext(&buffer, report.Go)
	fmt.Fprintln(&buffer, "Warning: Existing assessments retain their recorded baselines and have not been re-audited against these inventories.")
	fmt.Fprintln(&buffer, "Unreviewed declarations are not confirmed gaps; out-of-scope Go targets remain unvalidated.")
	fmt.Fprintln(&buffer, "Counts do not establish behavioral parity; selected rows may include unresolved catalog entries.")
	selectedRows := len(report.Rows)
	if report.Page != nil {
		selectedRows = report.Page.Total
	}
	fmt.Fprintf(&buffer, "\nInventory declarations: %d (unfiltered); selected rows: %d; selected assessments: %d.\n", report.InventoryDeclarations, selectedRows, report.AssessedDeclarations)
	fmt.Fprintf(&buffer, "Go exported symbols: %d; indexed packages: %d (both unfiltered).\n", report.Go.ExportedSymbols, len(report.Go.Packages))
	if report.Page != nil {
		if err := writePageText(&buffer, *report.Page); err != nil {
			return err
		}
	}

	table := tabularReportWriter{Writer: tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)}
	table.printf("\nSTATE\tSELECTED ROWS\n")
	for _, state := range reconciliationStates {
		table.printf("%s\t%d\n", state, report.Counts[state])
	}
	table.printf("\nAREA")
	for _, state := range reconciliationStates {
		table.printf("\t%s", state)
	}
	table.printf("\n")
	for _, area := range slices.Sorted(maps.Keys(report.ByArea)) {
		table.printf("%s", reportCell(area))
		for _, state := range reconciliationStates {
			table.printf("\t%d", report.ByArea[area][state])
		}
		table.printf("\n")
	}
	if !brief {
		table.printf("\nNAMESPACE\tTYPE\tMEMBER\tKIND\tAREA\tSTATE\tSTATUS\tGO EXAMPLE\tGO SYMBOLS\tEXPERIMENTAL\tMACHINERY\tREVIEW\tNOTE / REASON\n")
		for _, row := range report.Rows {
			var machinery []string
			if row.LanguageMachinery {
				machinery = append(machinery, "language")
			}
			if row.CompilerGenerated {
				machinery = append(machinery, "compiler-generated")
			}
			cells := []string{
				row.Namespace, row.Type, row.Member, row.Kind, row.Area,
				row.State, row.Status, mappingExample(row.Go), strings.Join(row.GoSymbols, ", "),
				row.Experimental, strings.Join(machinery, ", "), row.Review,
				reconciliationDetails(row),
			}
			for i, cell := range cells {
				cells[i] = reportCell(cell)
			}
			table.printf("%s\n", strings.Join(cells, "\t"))
		}
	}
	if err := table.flush(); err != nil {
		return err
	}
	_, err := buffer.WriteTo(out)
	return err
}

func writeGoReportContext(out *bytes.Buffer, api reconciliationGoInventory) {
	dirty := "unknown"
	if api.Dirty != nil {
		dirty = fmt.Sprint(*api.Dirty)
	}
	fmt.Fprintf(out, "Go inventory: module %s; commit %s; dirty %s.\n", reportCell(api.Module), reportCell(api.Commit), dirty)
	fmt.Fprintf(out, "Go build: GOOS=%s; GOARCH=%s; version=%s; CGO_ENABLED=%s; tags=%q.\n", reportCell(api.GOOS), reportCell(api.GOARCH), reportCell(api.GoVersion), reportCell(api.CGOEnabled), api.BuildTags)
}

func reconciliationDetails(row reconciliationRow) string {
	var details []string
	if row.Note != "" {
		details = append(details, row.Note)
	}
	if row.Reason != "" {
		details = append(details, "Reason: "+row.Reason)
	}
	if len(row.Candidates) != 0 {
		details = append(details, ".NET candidates: "+strings.Join(row.Candidates, ", "))
	}
	if len(row.SuggestedGo) != 0 {
		details = append(details, "Go name candidates (not assessed): "+strings.Join(row.SuggestedGo, ", "))
	}
	if len(row.InvalidGoTargets) != 0 {
		details = append(details, "Invalid Go targets: "+strings.Join(row.InvalidGoTargets, ", "))
	}
	if len(row.UnindexedGoTargets) != 0 {
		details = append(details, "Unindexed Go targets: "+strings.Join(row.UnindexedGoTargets, ", "))
	}
	if row.ReviewSourceChanged != nil && *row.ReviewSourceChanged {
		details = append(details, ".NET source commit changed since assessment")
	}
	if row.ReviewGoChanged != nil && *row.ReviewGoChanged {
		details = append(details, "Go commit changed since assessment")
	}
	return strings.Join(details, "; ")
}

var reportCellControls = strings.NewReplacer(
	"\t", `\t`, "\v", `\v`, "\f", `\f`, "\r", `\r`, "\n", `\n`,
)

func reportCell(value string) string {
	if value == "" {
		return "-"
	}
	return reportCellControls.Replace(value)
}
