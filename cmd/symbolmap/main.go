// Copyright (c) Microsoft. All rights reserved.

// symbolmap reports reviewed .NET/Go mappings and reconciles declarations.
// Go indexing uses the local toolchain and Git metadata, without network access.
// Declaration matches do not establish behavioral parity.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	identifier = `[\p{L}_][\p{L}\p{Nd}_]*`
	dotnetName = identifier + "(?:`[1-9][0-9]*|<[^()]+>)?"
)

var (
	areas             = []string{"agents", "messages", "tools", "providers", "hosting", "operations", "workflows"}
	statuses          = []string{"mapped", "adapted", "partial", "unmapped", "intentional"}
	kinds             = []string{"type", "constructor", "property", "method", "field", "constant"}
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	modulePattern     = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_-][A-Za-z0-9_.-]*)*$`)
	dotnetTypePattern = regexp.MustCompile(`^` + dotnetName + `(?:\.` + dotnetName + `)+$`)
	namespacePattern  = regexp.MustCompile(`^` + identifier + `(?:\.` + identifier + `)*$`)
	namePattern       = regexp.MustCompile(`^` + identifier + `$`)
	callablePattern   = regexp.MustCompile(`^` + dotnetName + `$`)
	goTargetPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*\.\p{Lu}[\p{L}\p{Nd}_]*(?:\.\p{Lu}[\p{L}\p{Nd}_]*)?$`)
)

type catalog struct {
	SchemaVersion *int                              `json:"schema_version"`
	Baseline      baseline                          `json:"baseline"`
	Reviews       map[string]reviewBaseline         `json:"reviews,omitempty"`
	Namespaces    map[string]map[string]typeMapping `json:"namespaces"`
}

type baseline struct {
	CheckedAt         string `json:"checked_at"`
	DotnetRepository  string `json:"dotnet_repository"`
	DotnetCommit      string `json:"dotnet_commit"`
	GoRepository      string `json:"go_repository"`
	GoCommit          string `json:"go_commit"`
	GoModule          string `json:"go_module"`
	InventoryComplete *bool  `json:"inventory_complete"`
	Scope             string `json:"scope"`
}

type typeMapping struct {
	Area         string             `json:"area"`
	Assembly     string             `json:"assembly,omitempty"`
	Mapping      *mapping           `json:"mapping,omitempty"`
	Properties   map[string]mapping `json:"properties,omitempty"`
	Methods      map[string]mapping `json:"methods,omitempty"`
	Constructors map[string]mapping `json:"constructors,omitempty"`
	Fields       map[string]mapping `json:"fields,omitempty"`
	Constants    map[string]mapping `json:"constants,omitempty"`
	Events       map[string]mapping `json:"events,omitempty"`
}

type mapping struct {
	Go        *string  `json:"go,omitempty"`
	GoSymbols []string `json:"go_symbols"`
	Status    string   `json:"status"`
	Note      string   `json:"note"`
	Review    string   `json:"review,omitempty"`
}

// A new review batch does not move the baseline of previously assessed leaves.
type reviewBaseline struct {
	CheckedAt       string `json:"checked_at"`
	DotnetCommit    string `json:"dotnet_commit"`
	GoCommit        string `json:"go_commit"`
	InventorySHA256 string `json:"inventory_sha256"`
	Scope           string `json:"scope"`
}

type mappingRow struct {
	Area      string   `json:"area"`
	Kind      string   `json:"kind"`
	Dotnet    string   `json:"dotnet"`
	Go        *string  `json:"go,omitempty"`
	GoSymbols []string `json:"go_symbols"`
	Status    string   `json:"status"`
	Note      string   `json:"note"`
	Review    string   `json:"review,omitempty"`
	Namespace string   `json:"namespace,omitempty"`
	Type      string   `json:"-"`
	Member    string   `json:"-"`
	Assembly  string   `json:"assembly,omitempty"`

	typeName string
}

type mappingsReport struct {
	Baseline baseline                  `json:"baseline"`
	Reviews  map[string]reviewBaseline `json:"reviews,omitempty"`
	Mappings []mappingRow              `json:"mappings"`
	Page     *pageInfo                 `json:"page,omitempty"`
}

type summary struct {
	Baseline      baseline                  `json:"baseline"`
	Reviews       map[string]reviewBaseline `json:"reviews,omitempty"`
	DotnetSymbols int                       `json:"dotnet_symbols"`
	GoSymbols     int                       `json:"go_symbols"`
	ByStatus      map[string]int            `json:"by_status"`
	ByKind        map[string]int            `json:"by_kind"`
	ByArea        map[string]int            `json:"by_area"`
}

type reportOptions struct {
	file, inventoryFile, goRoot, tags string
	filter                            reportFilter
	page                              pageOptions
	goPatterns                        []string
	asJSON, brief, check              bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out, diagnostics io.Writer) error {
	const usage = "Usage: symbolmap <mappings|gaps|go|reconcile> [flags]"
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
	case "mappings", "gaps", "go", "reconcile":
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
	rows, err := c.flatten()
	if err != nil {
		return err
	}
	report := mappingsReport{Baseline: c.Baseline, Reviews: c.Reviews, Mappings: rows}
	if command == "reconcile" {
		return runReconciliation(out, report, options)
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
	case "reconcile":
		addMappingFlags(flags, &options)
		addIndexFlags(flags, &options)
		flags.StringVar(&options.inventoryFile, "inventory", "docs/dotnet-sdk-symbol-inventory.json", "declaration inventory for reconcile")
		flags.StringVar(&options.filter.State, "state", "", "filter by state: "+strings.Join(reconciliationStates, ", "))
		flags.BoolVar(&options.check, "check", false, "fail on unresolved declarations or invalid Go targets")
	case "mappings":
		addMappingFlags(flags, &options)
		flags.BoolVar(&options.brief, "summary", false, "show counts by status, kind, and area")
	case "gaps":
		addMappingFlags(flags, &options)
	}
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, errors.New("unexpected positional arguments")
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
	} {
		if !slices.Contains(option.allowed, option.value) {
			return options, fmt.Errorf("invalid -%s %q", option.name, option.value)
		}
	}
	return options, nil
}

func addMappingFlags(flags *flag.FlagSet, options *reportOptions) {
	flags.StringVar(&options.file, "file", "docs/dotnet-go-sdk-symbol-mapping.json", "catalog path (default is relative to repository root)")
	flags.StringVar(&options.filter.Area, "area", "", "filter by area: "+strings.Join(areas, ", "))
	flags.StringVar(&options.filter.Kind, "kind", "", "filter by kind: "+strings.Join(append(slices.Clone(kinds), "event"), ", "))
	flags.StringVar(&options.filter.Status, "status", "", "filter by status: "+strings.Join(statuses, ", "))
	flags.StringVar(&options.filter.Type, "type", "", "case-insensitive substring of the containing .NET type")
	flags.StringVar(&options.filter.Symbol, "symbol", "", "case-insensitive substring of a full .NET or qualified Go symbol")
	flags.StringVar(&options.filter.Namespace, "namespace", "", "case-insensitive substring of the .NET namespace")
}

func addIndexFlags(flags *flag.FlagSet, options *reportOptions) {
	flags.StringVar(&options.goRoot, "go-root", ".", "Go module root (dependencies must already be local)")
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

func runReconciliation(out io.Writer, report mappingsReport, options reportOptions) error {
	inv, err := loadDeclarationInventory(options.inventoryFile)
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
		Baseline: report.Baseline, Reviews: report.Reviews, DotnetSymbols: len(report.Mappings),
		ByStatus: counts(statuses), ByKind: counts(kinds), ByArea: counts(areas),
	}
	targets := make(map[string]bool)
	for _, row := range report.Mappings {
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

func loadCatalog(name string) (catalog, error) {
	var c catalog
	data, err := os.ReadFile(name)
	if err != nil {
		return c, err
	}
	// Check keys before decoding into maps: encoding/json otherwise keeps only
	// the last value of a repeated key, hiding invalid or conflicting mappings.
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := checkJSONKeys(decoder); err != nil {
		return c, fmt.Errorf("%s: %w", name, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("%s: expected a single JSON object", name)
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", name, err)
	}
	return c, nil
}

func checkJSONKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if seen[name] {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = true
			if err := checkJSONKeys(decoder); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for decoder.More() {
			if err := checkJSONKeys(decoder); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = decoder.Token()
	return err
}

func (c catalog) flatten() ([]mappingRow, error) {
	if c.SchemaVersion == nil {
		return nil, errors.New("schema_version is required")
	}
	if *c.SchemaVersion != 0 {
		return nil, fmt.Errorf("unsupported schema_version %d", *c.SchemaVersion)
	}
	b := c.Baseline
	if !validDate(b.CheckedAt) || !shaPattern.MatchString(b.DotnetCommit) || !shaPattern.MatchString(b.GoCommit) {
		return nil, errors.New("baseline requires valid checked_at and pinned dotnet_commit/go_commit")
	}
	if !validURL(b.DotnetRepository) || !validURL(b.GoRepository) || !modulePattern.MatchString(b.GoModule) || b.InventoryComplete == nil || strings.TrimSpace(b.Scope) == "" {
		return nil, errors.New("baseline requires repository URLs, go_module, inventory_complete, and a nonempty scope")
	}
	for id, review := range c.Reviews {
		if strings.TrimSpace(id) == "" || !validDate(review.CheckedAt) || !shaPattern.MatchString(review.DotnetCommit) || !shaPattern.MatchString(review.GoCommit) || len(review.InventorySHA256) != 64 || strings.Trim(review.InventorySHA256, "0123456789abcdef") != "" || strings.TrimSpace(review.Scope) == "" {
			return nil, fmt.Errorf("invalid review baseline %q", id)
		}
	}
	if len(c.Namespaces) == 0 {
		return nil, errors.New("namespaces must not be empty")
	}
	types := make(map[string]typeMapping)
	owners := make(map[string][2]string)
	for namespace, entries := range c.Namespaces {
		if !namespacePattern.MatchString(namespace) || len(entries) == 0 {
			return nil, fmt.Errorf("invalid or empty namespace %q", namespace)
		}
		for name, entry := range entries {
			full := namespace + "." + name
			if _, exists := types[full]; exists {
				return nil, fmt.Errorf("duplicate .NET type %q", full)
			}
			types[full], owners[full] = entry, [2]string{namespace, name}
		}
	}
	rows := make([]mappingRow, 0)
	seenTypes, seenSymbols := make(map[string]bool), make(map[string]bool)
	for _, typeName := range slices.Sorted(maps.Keys(types)) {
		if !dotnetTypePattern.MatchString(typeName) || !balancedAngles(typeName) {
			return nil, fmt.Errorf("invalid namespace-qualified .NET type %q", typeName)
		}
		identity := symbolIdentity(typeName)
		if seenTypes[identity] {
			return nil, fmt.Errorf("duplicate .NET type %q", typeName)
		}
		seenTypes[identity] = true
		entry := types[typeName]
		if !slices.Contains(areas, entry.Area) {
			return nil, fmt.Errorf("%s: invalid area %q", typeName, entry.Area)
		}
		namespace, shortName := owners[typeName][0], owners[typeName][1]
		add := func(kind, symbol, member string, m mapping) error {
			identity := symbolIdentity(symbol)
			if seenSymbols[identity] {
				return fmt.Errorf("duplicate .NET symbol %q", symbol)
			}
			seenSymbols[identity] = true
			if err := m.validate(symbol, kind); err != nil {
				return err
			}
			if m.Review != "" {
				if _, exists := c.Reviews[m.Review]; !exists {
					return fmt.Errorf("%s: unknown review %q", symbol, m.Review)
				}
			}
			display := shortName
			if member != "" {
				display += "." + member
			}
			rows = append(rows, mappingRow{
				Area: entry.Area, Kind: kind, Dotnet: display,
				Go: m.Go, GoSymbols: m.GoSymbols, Status: m.Status, Note: m.Note, Review: m.Review,
				Namespace: namespace, Type: shortName, Member: member, Assembly: entry.Assembly,
				typeName: typeName,
			})
			return nil
		}
		// Types, kinds, and member keys are ordered lexically. A type row is
		// emitted only for an explicit mapping, never inferred from its members.
		for _, group := range []struct {
			kind    string
			members map[string]mapping
		}{
			{"constant", entry.Constants},
			{"constructor", entry.Constructors},
			{"event", entry.Events},
			{"field", entry.Fields},
			{"method", entry.Methods},
			{"property", entry.Properties},
		} {
			for _, member := range slices.Sorted(maps.Keys(group.members)) {
				valid := namePattern.MatchString(member)
				if group.kind == "method" || group.kind == "constructor" || group.kind == "property" {
					valid = validReadableMember(member, group.kind)
				}
				if !valid {
					return nil, fmt.Errorf("%s: invalid %s key %q", typeName, group.kind, member)
				}
				if err := add(group.kind, typeName+"."+member, member, group.members[member]); err != nil {
					return nil, err
				}
			}
		}
		if entry.Mapping != nil {
			if err := add("type", typeName, "", *entry.Mapping); err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}

func mappingExample(example *string) string {
	if example == nil {
		return ""
	}
	return *example
}

func (m mapping) validate(owner, kind string) error {
	if !slices.Contains(statuses, m.Status) {
		return fmt.Errorf("%s: invalid status %q", owner, m.Status)
	}
	if strings.TrimSpace(m.Note) == "" {
		return fmt.Errorf("%s: note must not be empty", owner)
	}
	if kind == "property" && m.Go != nil {
		return fmt.Errorf("%s: properties must not define Go examples", owner)
	}
	if kind != "property" && m.Go == nil {
		return fmt.Errorf("%s: go must be a non-null string", owner)
	}
	if m.GoSymbols == nil {
		return fmt.Errorf("%s: go_symbols must be a non-null array", owner)
	}
	if kind == "property" {
		if len(m.GoSymbols) == 0 && m.Status != "unmapped" && m.Status != "intentional" {
			return fmt.Errorf("%s: empty go_symbols requires unmapped or intentional status", owner)
		}
	} else if strings.TrimSpace(*m.Go) == "" {
		if m.Status != "unmapped" && m.Status != "intentional" {
			return fmt.Errorf("%s: empty go requires unmapped or intentional status", owner)
		}
		if len(m.GoSymbols) != 0 {
			return fmt.Errorf("%s: empty go requires empty go_symbols", owner)
		}
	} else {
		if strings.HasPrefix(strings.TrimSpace(*m.Go), "func(") {
			return fmt.Errorf("%s: go example must not use a synthetic outer function", owner)
		}
		if len(m.GoSymbols) == 0 {
			return fmt.Errorf("%s: nonempty go requires go_symbols", owner)
		}
		if _, _, err := parseGoExample(*m.Go); err != nil {
			return fmt.Errorf("%s: invalid Go example: %w", owner, err)
		}
	}
	seen := make(map[string]bool)
	for _, target := range m.GoSymbols {
		if !goTargetPattern.MatchString(target) {
			return fmt.Errorf("%s: invalid qualified Go symbol %q", owner, target)
		}
		if seen[target] {
			return fmt.Errorf("%s: duplicate Go target %q", owner, target)
		}
		seen[target] = true
	}
	return nil
}

func validCallable(value string) bool {
	open := strings.IndexByte(value, '(')
	if open < 1 || !strings.HasSuffix(value, ")") {
		return false
	}
	name := strings.TrimSpace(value[:open])
	if !callablePattern.MatchString(name) || !balancedAngles(name) {
		return false
	}
	// Parameter types may contain dots, generics, and nested tuple parentheses.
	depth := 0
	for i, r := range value[open:] {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && open+i != len(value)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func validReadableMember(value, kind string) bool {
	parts, ok := dnSplit(value, "->")
	if !ok || len(parts) > 2 || len(parts) == 2 && strings.TrimSpace(parts[1]) == "" {
		return false
	}
	head := parts[0]
	if kind == "property" && namePattern.MatchString(head) {
		return true
	}
	if strings.HasPrefix(head, ".ctor(") {
		head = "Constructor" + strings.TrimPrefix(head, ".ctor")
	}
	if kind == "method" && strings.HasPrefix(head, "<Clone>$(") {
		head = "Clone" + strings.TrimPrefix(head, "<Clone>$")
	}
	return validCallable(strings.ReplaceAll(head, "``", "`"))
}

func balancedAngles(value string) bool {
	depth := 0
	for _, r := range value {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func symbolIdentity(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}

func validDate(value string) bool {
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}

func validURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
