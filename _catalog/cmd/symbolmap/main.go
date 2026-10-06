// Copyright (c) Microsoft. All rights reserved.

// symbolmap reports reviewed .NET/Go mappings and reconciles declarations.
// Go indexing uses the local toolchain and Git metadata, without network access.
// Declaration matches do not establish behavioral parity.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
)

const defaultCatalogFile = "dotnet-go-sdk-symbol-mapping.json"

var (
	areas         = []string{"agents", "messages", "tools", "providers", "hosting", "operations", "workflows"}
	statuses      = []string{"mapped", "adapted", "partial", "unmapped", "intentional"}
	kinds         = []string{"type", "constructor", "property", "method", "field", "constant"}
	modulePattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_-][A-Za-z0-9_.-]*)*$`)
)

type mappingRow struct {
	Area       string   `json:"area"`
	Kind       string   `json:"kind"`
	Dotnet     string   `json:"dotnet"`
	Go         *string  `json:"go,omitempty"`
	GoSymbols  []string `json:"go_symbols"`
	Status     string   `json:"status"`
	Unreviewed bool     `json:"unreviewed,omitempty"`
	Note       string   `json:"note,omitempty"`
	Namespace  string   `json:"namespace,omitempty"`
	Type       string   `json:"-"`
	Member     string   `json:"-"`
	Assembly   string   `json:"assembly,omitempty"`

	typeName    string
	unavailable bool
}

type mappingsReport struct {
	Baseline symbolcatalog.Baseline                    `json:"baseline"`
	Mappings []mappingRow                              `json:"mappings"`
	Page     *pageInfo                                 `json:"page,omitempty"`
	GoOnly   map[string]symbolcatalog.GoOnlyAssessment `json:"-"`
}

type summary struct {
	Baseline          symbolcatalog.Baseline `json:"baseline"`
	DotnetSymbols     int                    `json:"dotnet_symbols"`
	UnreviewedSymbols int                    `json:"unreviewed_symbols,omitempty"`
	GoSymbols         int                    `json:"go_symbols"`
	GoOnlySymbols     int                    `json:"go_only_symbols,omitempty"`
	ByStatus          map[string]int         `json:"by_status"`
	ByKind            map[string]int         `json:"by_kind"`
	ByArea            map[string]int         `json:"by_area"`
}

type reportOptions struct {
	file, goRoot, oldRoot, tags string
	filter                      reportFilter
	testFilter                  testReportFilter
	page                        pageOptions
	goPatterns                  []string
	asJSON, brief, check        bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out, diagnostics io.Writer) error {
	const usage = "Usage: symbolmap <mappings|gaps|go-only|go|changes|reconcile|go-tests|tests> [flags]"
	if len(args) == 0 {
		if _, err := fmt.Fprintln(diagnostics, usage); err != nil {
			return err
		}
		return errors.New("subcommand required")
	}
	command := args[0]
	if command == "-h" || command == "-help" {
		_, err := fmt.Fprintln(diagnostics, usage)
		return err
	}
	switch command {
	case "mappings", "gaps", "go-only", "go", "changes", "reconcile", "go-tests", "tests":
	default:
		return fmt.Errorf("unknown subcommand %q", command)
	}
	options, err := parseReportOptions(command, args[1:], diagnostics)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if command == "changes" {
		return runGoChanges(out, options)
	}
	if command == "go-tests" {
		index, err := indexGoTests(options.goRoot)
		if err != nil {
			return err
		}
		return writeGoTests(out, index, options.testFilter.Symbol, options.page, options.asJSON, options.brief)
	}
	if command == "go" {
		api, err := indexGo(options.goRoot, options.goPatterns, options.tags)
		if err != nil {
			return err
		}
		return writeGoInventory(out, api, options.filter.Symbol, options.page, options.asJSON, options.brief)
	}
	c, err := loadCatalog(options.file)
	if err != nil {
		return err
	}
	if command == "tests" {
		return runTestReconciliation(out, c, options)
	}
	report := mappingsReport{Baseline: c.Baseline, GoOnly: c.GoOnly}
	if command == "go-only" {
		return writeGoOnlyReport(out, report, options.filter.Symbol, options.page, options.asJSON)
	}
	report.Mappings = flattenMappings(c)
	if command == "reconcile" {
		return runReconciliation(out, c, report, options)
	}
	if options.brief {
		command = "summary"
	}
	return writeMappingReport(out, report, options.filter, command, options.page, options.asJSON)
}

func parseReportOptions(command string, args []string, diagnostics io.Writer) (reportOptions, error) {
	var options reportOptions
	flags := flag.NewFlagSet("symbolmap "+command, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.BoolVar(&options.asJSON, "json", true, "emit JSON (default; set false for human-readable text)")
	flags.IntVar(&options.page.Limit, "limit", defaultPageLimit, "maximum rows per page (0 returns all)")
	flags.IntVar(&options.page.Offset, "offset", 0, "number of filtered rows to skip")
	switch command {
	case "go":
		addIndexFlags(flags, &options)
		flags.StringVar(&options.filter.Symbol, "symbol", "", "case-insensitive substring of a qualified Go symbol")
	case "changes":
		addIndexFlags(flags, &options)
		flags.StringVar(&options.oldRoot, "old-root", "", "old Go module checkout (required; dependencies must already be local)")
		flags.StringVar(&options.filter.Symbol, "symbol", "", "case-insensitive substring of an API change message")
	case "reconcile":
		addMappingFlags(flags, &options)
		addIndexFlags(flags, &options)
		flags.StringVar(&options.filter.State, "state", "", "filter by state: "+strings.Join(reconciliationStates, ", "))
		flags.BoolVar(&options.check, "check", false, "fail on unresolved declarations or invalid Go targets")
	case "mappings":
		addMappingFlags(flags, &options)
		flags.BoolVar(&options.brief, "summary", false, "show counts by status, kind, and area")
	case "gaps":
		addMappingFlags(flags, &options)
	case "go-only":
		flags.StringVar(&options.file, "file", defaultCatalogFile, "catalog path (default is relative to the catalog module root)")
		flags.StringVar(&options.filter.Symbol, "symbol", "", "case-insensitive substring of a qualified Go symbol")
	case "go-tests":
		addGoTestFlags(flags, &options)
		flags.StringVar(&options.testFilter.Symbol, "symbol", "", "case-insensitive substring of a qualified Go test function")
	case "tests":
		addGoTestFlags(flags, &options)
		flags.StringVar(&options.file, "file", defaultCatalogFile, "catalog path (default is relative to the catalog module root)")
		flags.StringVar(&options.testFilter.Assembly, "assembly", "", "case-insensitive substring of the test assembly name")
		flags.StringVar(&options.testFilter.Namespace, "namespace", "", "case-insensitive substring of the declaring namespace")
		flags.StringVar(&options.testFilter.Type, "type", "", "case-insensitive substring of the full CLR declaring type")
		flags.StringVar(&options.testFilter.Symbol, "symbol", "", "case-insensitive substring of a test identity or qualified Go test function")
		flags.StringVar(&options.testFilter.State, "state", "", "filter by test state: "+strings.Join(testReconciliationStates, ", "))
		flags.BoolVar(&options.check, "check", false, "fail on dangling .NET test references or missing Go test functions across the entire report")
	}
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, errors.New("unexpected positional arguments")
	}
	if command == "changes" && strings.TrimSpace(options.oldRoot) == "" {
		return options, errors.New("changes requires -old-root")
	}
	if options.page.Limit < 0 || options.page.Offset < 0 {
		return options, errors.New("-limit and -offset must be nonnegative")
	}
	if options.brief {
		var paged bool
		flags.Visit(func(flag *flag.Flag) {
			paged = paged || flag.Name == "limit" || flag.Name == "offset"
		})
		if paged {
			return options, errors.New("-limit and -offset cannot be used with -summary")
		}
	}
	for _, option := range []struct {
		name    string
		value   string
		allowed []string
	}{
		{"area", options.filter.Area, append([]string{"", "unclassified"}, areas...)},
		{"kind", options.filter.Kind, append([]string{"", "event"}, kinds...)},
		{"status", options.filter.Status, append([]string{""}, statuses...)},
		{"state", options.filter.State, append([]string{""}, reconciliationStates...)},
		{"state", options.testFilter.State, append([]string{""}, testReconciliationStates...)},
	} {
		if !slices.Contains(option.allowed, option.value) {
			return options, fmt.Errorf("invalid -%s %q", option.name, option.value)
		}
	}
	return options, nil
}

func addMappingFlags(flags *flag.FlagSet, options *reportOptions) {
	flags.StringVar(&options.file, "file", defaultCatalogFile, "catalog path (default is relative to the catalog module root)")
	flags.BoolVar(&options.filter.Assessed, "assessed", false, "exclude extracted declarations awaiting assessment")
	flags.StringVar(&options.filter.Area, "area", "", "filter by area: "+strings.Join(areas, ", "))
	flags.StringVar(&options.filter.Kind, "kind", "", "filter by kind: "+strings.Join(append(slices.Clone(kinds), "event"), ", "))
	flags.StringVar(&options.filter.Status, "status", "", "filter by status: "+strings.Join(statuses, ", "))
	flags.StringVar(&options.filter.Type, "type", "", "case-insensitive substring of the containing .NET type")
	flags.StringVar(&options.filter.Symbol, "symbol", "", "case-insensitive substring of a full .NET or qualified Go symbol")
	flags.StringVar(&options.filter.Namespace, "namespace", "", "case-insensitive substring of the .NET namespace")
}

func addIndexFlags(flags *flag.FlagSet, options *reportOptions) {
	flags.StringVar(&options.goRoot, "go-root", "..", "Go module root (default is the SDK checkout above the catalog module; dependencies must already be local)")
	flags.StringVar(&options.tags, "tags", "", "Go build tags (default: inherited GOFLAGS)")
	flags.BoolVar(&options.brief, "summary", false, "emit counts without declaration rows")
	flags.Func("go-package", "repeat to replace the default SDK package set", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("go package pattern must not be empty")
		}
		options.goPatterns = append(options.goPatterns, value)
		return nil
	})
}

func addGoTestFlags(flags *flag.FlagSet, options *reportOptions) {
	flags.StringVar(&options.goRoot, "go-root", "..", "Go module root for static test discovery (default is the SDK checkout above the catalog module; no compilation or dependency loading)")
	flags.BoolVar(&options.brief, "summary", false, "emit counts without test declaration rows")
}

func runReconciliation(out io.Writer, c symbolcatalog.Catalog, report mappingsReport, options reportOptions) error {
	inv, err := c.Declarations()
	if err != nil {
		return err
	}
	api, err := indexGo(options.goRoot, options.goPatterns, options.tags)
	if err != nil {
		return err
	}
	if api.Module != report.Baseline.GoModule {
		return fmt.Errorf("indexed Go module %q does not match catalog go_module %q", api.Module, report.Baseline.GoModule)
	}
	for _, row := range report.Mappings {
		example := mappingExample(row.Go)
		if example == "" {
			continue
		}
		if err := checkGoExample(example, api); err != nil {
			return fmt.Errorf("%s.%s: Go example: %w", row.Namespace, row.Dotnet, err)
		}
	}
	r := reconcile(report, inv, api, len(options.goPatterns) == 0)
	return writeReconciliation(out, r, options.filter, options.page, options.asJSON, options.brief, options.check)
}

func summarize(report mappingsReport) summary {
	r := summary{
		Baseline: report.Baseline, DotnetSymbols: len(report.Mappings),
		GoOnlySymbols: len(report.GoOnly),
		ByStatus:      counts(statuses), ByKind: counts(kinds), ByArea: counts(areas),
	}
	targets := make(map[string]bool)
	for _, row := range report.Mappings {
		if row.Unreviewed {
			r.UnreviewedSymbols++
		}
		r.ByStatus[row.Status]++
		r.ByKind[row.Kind]++
		r.ByArea[row.Area]++
		for _, target := range row.GoSymbols {
			targets[target] = true
		}
	}
	r.GoSymbols = len(targets)
	return r
}

func counts(values []string) map[string]int {
	m := make(map[string]int, len(values))
	for _, value := range values {
		m[value] = 0
	}
	return m
}

func loadCatalog(name string) (symbolcatalog.Catalog, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return symbolcatalog.Catalog{}, err
	}
	c, err := symbolcatalog.Decode(data)
	if err != nil {
		return symbolcatalog.Catalog{}, fmt.Errorf("%s: %w", name, err)
	}
	return c, nil
}

// flattenMappings reads a catalog already validated by loadCatalog.
func flattenMappings(c symbolcatalog.Catalog) []mappingRow {
	types := make(map[string]symbolcatalog.Type)
	owners := make(map[string][2]string)
	for namespace, entries := range c.Namespaces {
		for name, entry := range entries {
			full := namespace + "." + name
			types[full], owners[full] = entry, [2]string{namespace, name}
		}
	}
	rows := make([]mappingRow, 0)
	for _, typeName := range slices.Sorted(maps.Keys(types)) {
		entry := types[typeName]
		namespace, shortName := owners[typeName][0], owners[typeName][1]
		add := func(kind, member string, m symbolcatalog.Mapping) {
			display := shortName
			if member != "" {
				display += "." + member
			}
			rows = append(rows, mappingRow{
				Area: entry.Area, Kind: kind, Dotnet: display,
				Go: m.Go, GoSymbols: m.GoSymbols, Status: m.Status, Note: m.Note,
				Unreviewed: m.Unreviewed,
				Namespace:  namespace, Type: shortName, Member: member, Assembly: entry.Assembly,
				typeName: typeName, unavailable: entry.Unavailable || m.Unavailable,
			})
		}
		// Types, kinds, and member keys are ordered lexically. A type row is
		// emitted only for an explicit mapping, never inferred from its members.
		for _, group := range []struct {
			kind    string
			members map[string]symbolcatalog.Mapping
		}{
			{"constant", entry.Constants},
			{"constructor", entry.Constructors},
			{"event", entry.Events},
			{"field", entry.Fields},
			{"method", entry.Methods},
			{"property", entry.Properties},
		} {
			for _, member := range slices.Sorted(maps.Keys(group.members)) {
				add(group.kind, member, group.members[member])
			}
		}
		if entry.Mapping != nil {
			add("type", "", *entry.Mapping)
		}
	}
	return rows
}

func mappingExample(example *string) string {
	if example == nil {
		return ""
	}
	return *example
}
