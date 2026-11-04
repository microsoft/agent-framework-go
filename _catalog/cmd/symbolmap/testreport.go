// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"
)

var testReconciliationStates = []string{
	"linked", "unreviewed", "needs-reconciliation", "invalid-go-target",
}

type testReportFilter struct {
	Assembly  string
	Namespace string
	Type      string
	Symbol    string
	State     string
}

type testInventoryMetadata struct {
	IdentityFormat string                                    `json:"identity_format"`
	Assemblies     map[string]testinventory.AssemblyMetadata `json:"assemblies"`
	SHA256         string                                    `json:"sha256"`
}

// Type retains its CLR identity; Method is the test name without a signature.
type testReconciliationRow struct {
	Assembly      string `json:"assembly"`
	Type          string `json:"type"`
	Method        string `json:"method"`
	Dotnet        string `json:"dotnet"`
	State         string `json:"state"`
	GoTest        string `json:"go_test,omitempty"`
	Reason        string `json:"reason,omitempty"`
	InvalidGoTest string `json:"invalid_go_test,omitempty"`
}

type testReconciliationSummary struct {
	Inventory      testInventoryMetadata     `json:"inventory"`
	Go             goTestMetadata            `json:"go"`
	InventoryTests int                       `json:"inventory_tests"`
	MappedTests    int                       `json:"mapped_tests"`
	Counts         map[string]int            `json:"counts"`
	ByAssembly     map[string]map[string]int `json:"by_assembly"`
}

type testReconciliationReport struct {
	testReconciliationSummary
	Rows []testReconciliationRow `json:"rows"`
	Page *pageInfo               `json:"page,omitempty"`
}

func runTestReconciliation(out io.Writer, c symbolcatalog.Catalog, options reportOptions) error {
	inv, err := c.Declarations()
	if err != nil {
		return err
	}
	if inv.Tests == nil {
		return errors.New("tests requires test assembly metadata: the catalog has no tests section in dotnet metadata")
	}
	pairs := flattenTests(c)
	index, err := indexGoTests(options.goRoot)
	if err != nil {
		return err
	}
	if index.Module != c.Baseline.GoModule {
		return fmt.Errorf("indexed Go module %q does not match catalog go_module %q", index.Module, c.Baseline.GoModule)
	}
	report := reconcileTests(pairs, inv, index)
	return writeTestReconciliation(out, report, options.testFilter, options.page, options.asJSON, options.brief, options.check)
}

func reconcileTests(pairs map[testRef]string, inv symbolcatalog.Inventory, index goTestInventory) testReconciliationReport {
	result := testReconciliationReport{
		testReconciliationSummary: testReconciliationSummary{
			Inventory: testInventoryMetadata{
				IdentityFormat: inv.Tests.IdentityFormat, SHA256: inv.SHA256,
				Assemblies: make(map[string]testinventory.AssemblyMetadata),
			},
			Go: index.goTestMetadata,
		},
		Rows: make([]testReconciliationRow, 0),
	}
	pending := maps.Clone(pairs)
	newRow := func(ref testRef) testReconciliationRow {
		return testReconciliationRow{
			Assembly: ref.assembly, Type: ref.owner, Method: ref.method,
			Dotnet: ref.owner + "." + ref.method,
			State:  "unreviewed",
		}
	}
	apply := func(row *testReconciliationRow, target string) {
		row.GoTest = target
		if _, exists := slices.BinarySearch(index.Tests, target); !exists {
			row.InvalidGoTest = target
		}
		if row.State == "linked" && row.InvalidGoTest != "" {
			row.State = "invalid-go-target"
			row.Reason = "the recorded Go test function was not found in the module's source test index"
		}
	}
	for name, assembly := range inv.Tests.Assemblies {
		result.Inventory.Assemblies[name] = assembly.AssemblyMetadata
		for owner, methods := range assembly.Types {
			for _, method := range methods {
				ref := testRef{assembly: name, owner: owner, method: method}
				row := newRow(ref)
				result.InventoryTests++
				if target, ok := pending[ref]; ok {
					row.State = "linked"
					apply(&row, target)
					delete(pending, ref)
				}
				result.Rows = append(result.Rows, row)
			}
		}
	}
	for ref, target := range pending {
		row := newRow(ref)
		row.State = "needs-reconciliation"
		row.Reason = "the exact assembly, declaring type, and test name were not found in the test inventory; inspect build scope and revision"
		apply(&row, target)
		result.Rows = append(result.Rows, row)
	}
	slices.SortFunc(result.Rows, func(a, b testReconciliationRow) int {
		return cmp.Or(cmp.Compare(a.Assembly, b.Assembly), cmp.Compare(a.Type, b.Type), cmp.Compare(a.Method, b.Method))
	})
	summarizeTestReconciliation(&result)
	return result
}

func summarizeTestReconciliation(report *testReconciliationReport) {
	report.Counts = counts(testReconciliationStates)
	report.ByAssembly = make(map[string]map[string]int)
	report.MappedTests = 0
	for _, row := range report.Rows {
		report.Counts[row.State]++
		if report.ByAssembly[row.Assembly] == nil {
			report.ByAssembly[row.Assembly] = counts(testReconciliationStates)
		}
		report.ByAssembly[row.Assembly][row.State]++
		if row.GoTest != "" {
			report.MappedTests++
		}
	}
}

func writeTestReconciliation(out io.Writer, report testReconciliationReport, filter testReportFilter, paging pageOptions, asJSON, brief, check bool) error {
	total, failures := len(report.Rows), 0
	selected := make([]testReconciliationRow, 0, total)
	for _, row := range report.Rows {
		if row.State == "needs-reconciliation" || row.State == "invalid-go-target" || row.InvalidGoTest != "" {
			failures++
		}
		if filter.State != "" && row.State != filter.State {
			continue
		}
		if !strings.Contains(strings.ToLower(row.Assembly), strings.ToLower(filter.Assembly)) ||
			!strings.Contains(strings.ToLower(row.Type), strings.ToLower(filter.Type)) ||
			!strings.Contains(strings.ToLower(testDeclaringPrefix(row.Type)), strings.ToLower(filter.Namespace)) {
			continue
		}
		symbol := strings.ToLower(filter.Symbol)
		if !strings.Contains(strings.ToLower(row.Dotnet), symbol) && !strings.Contains(strings.ToLower(row.GoTest), symbol) {
			continue
		}
		selected = append(selected, row)
	}
	report.Rows, report.Page = selected, nil
	summarizeTestReconciliation(&report)
	if !brief {
		visible, page := pageItems(report.Rows, paging)
		report.Rows, report.Page = visible, &page
	}
	var err error
	if asJSON {
		if brief {
			err = writeReportJSON(out, report.testReconciliationSummary)
		} else {
			err = writeReportJSON(out, report)
		}
	} else {
		err = writeTestReconciliationText(out, report, brief)
	}
	if err != nil {
		return err
	}
	if check && failures != 0 {
		return fmt.Errorf("test reconciliation check failed: %d of %d unfiltered rows have dangling .NET test references or missing Go test functions", failures, total)
	}
	return nil
}

func testDeclaringPrefix(owner string) string {
	namespace, _ := symbolcatalog.Owner(owner)
	return namespace
}

func writeTestReconciliationText(out io.Writer, report testReconciliationReport, brief bool) error {
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, ".NET test metadata: identity %s.\n", reportCell(report.Inventory.IdentityFormat))
	fmt.Fprintf(&buffer, "Declaration inventory SHA256: %s.\n", reportCell(report.Inventory.SHA256))
	for _, name := range slices.Sorted(maps.Keys(report.Inventory.Assemblies)) {
		info := report.Inventory.Assemblies[name]
		fmt.Fprintf(&buffer, "Assembly %s: source commit %s; framework %s; SHA256 %s.\n", reportCell(name), reportCell(info.Commit), reportCell(info.TargetFramework), reportCell(info.SHA256))
	}
	writeGoTestContext(&buffer, report.Go)
	fmt.Fprintln(&buffer, "Rows count compiled test method declarations, not theory data rows or runtime subtests. No tests were run.")
	fmt.Fprintln(&buffer, "Only supplied test builds are inventoried; an omitted source commit is unknown.")
	fmt.Fprintln(&buffer, "Linked validates one-to-one references, not behavioral parity. Unreviewed does not imply a missing test.")
	fmt.Fprintf(&buffer, "\nInventory tests: %d (unfiltered); selected test pairs: %d.\n", report.InventoryTests, report.MappedTests)
	fmt.Fprintf(&buffer, "Discovered Go test functions: %d (unfiltered). Counts are not coverage percentages.\n", report.Go.TestFunctions)
	if report.Page != nil {
		if err := writePageText(&buffer, *report.Page); err != nil {
			return err
		}
	}
	table := tabularReportWriter{Writer: tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)}
	table.printf("\nSTATE\tSELECTED ROWS\n")
	for _, state := range testReconciliationStates {
		table.printf("%s\t%d\n", state, report.Counts[state])
	}
	table.printf("\nASSEMBLY")
	for _, state := range testReconciliationStates {
		table.printf("\t%s", state)
	}
	table.printf("\n")
	for _, assembly := range slices.Sorted(maps.Keys(report.ByAssembly)) {
		table.printf("%s", reportCell(assembly))
		for _, state := range testReconciliationStates {
			table.printf("\t%d", report.ByAssembly[assembly][state])
		}
		table.printf("\n")
	}
	if !brief {
		table.printf("\nASSEMBLY\t.NET TEST\tGO TEST\tSTATE\tREASON\n")
		for _, row := range report.Rows {
			var details []string
			if row.Reason != "" {
				details = append(details, row.Reason)
			}
			if row.InvalidGoTest != "" {
				details = append(details, "Missing Go test: "+row.InvalidGoTest)
			}
			cells := []string{row.Assembly, row.Dotnet, row.GoTest, row.State, strings.Join(details, "; ")}
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
