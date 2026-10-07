// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
)

// Decode the command's public JSON rather than inspecting its internal catalog.
type reportedMapping struct {
	Area       string   `json:"area"`
	Kind       string   `json:"kind"`
	Namespace  string   `json:"namespace"`
	Dotnet     string   `json:"dotnet"`
	Go         string   `json:"go"`
	GoSymbols  []string `json:"go_symbols"`
	Status     string   `json:"status"`
	Unreviewed bool     `json:"unreviewed,omitempty"`
	Note       string   `json:"note"`
	Assembly   string   `json:"assembly,omitempty"`
}

type reportedMappings struct {
	Baseline map[string]any    `json:"baseline"`
	Mappings []reportedMapping `json:"mappings"`
	Page     *pageInfo         `json:"page,omitempty"`
}

type reportedSummary struct {
	Baseline          map[string]any `json:"baseline"`
	DotnetSymbols     int            `json:"dotnet_symbols"`
	UnreviewedSymbols int            `json:"unreviewed_symbols,omitempty"`
	GoSymbols         int            `json:"go_symbols"`
	GoOnlySymbols     int            `json:"go_only_symbols,omitempty"`
	ByStatus          map[string]int `json:"by_status"`
	ByKind            map[string]int `json:"by_kind"`
	ByArea            map[string]int `json:"by_area"`
}

const (
	agentType  = "Agent"
	stateType  = "StateBag"
	runnerType = "Runner"
	runString  = "RunAsync(string, System.Threading.CancellationToken)"
	runList    = "RunAsync(System.Collections.Generic.IEnumerable<Example.Messages.Message>, System.Threading.CancellationToken)"
	serialize  = "SerializeSessionAsync(Example.Agents.Session)"
	getValue   = "Get<T>(System.String)"

	sampleBaselineJSON = `{
  "checked_at": "2026-09-23",
  "dotnet_repository": "https://example.org/dotnet",
  "dotnet_commit": "1111111111111111111111111111111111111111",
  "go_repository": "https://example.org/go",
  "go_commit": "2222222222222222222222222222222222222222",
  "go_module": "example.org/sdk",
  "inventory_complete": false,
  "scope": "Selected declarations, not a behavioral audit."
}`
	sampleNamespacesJSON = `{
	"Example.Workflows": {
		"Runner": {
			"area": "workflows",
			"mapping": {"go": "inproc.ExecutionEnvironment{}", "go_symbols": ["workflow/inproc.ExecutionEnvironment"], "status": "mapped", "note": "Execution runtime counterpart."},
			"methods": {
				"RunAsync(string, System.Threading.CancellationToken)": {"go": "(*inproc.ExecutionEnvironment).Run", "go_symbols": ["workflow/inproc.ExecutionEnvironment.Run"], "status": "partial", "note": "Background execution is unavailable."}
			}
		}
	},
	"Example.Agents": {
		"StateBag": {
			"area": "agents",
			"methods": {
				"Get<T>(System.String)": {"go": "(*agent.Session).Get", "go_symbols": ["agent.Session.Get"], "status": "adapted", "note": "Writes into a destination pointer."}
			}
		},
		"Agent": {
			"area": "agents",
			"mapping": {"go": "agent.Agent{}", "go_symbols": ["agent.Agent"], "status": "adapted", "note": "Configured concrete agent."},
			"properties": {
				"Name": {"go_symbols": ["agent.Agent.Name"], "status": "mapped", "note": "Name accessor."},
				"Options": {"go_symbols": ["agent.WithSession"], "status": "adapted", "note": "Session option factory."},
				"Missing": {"go_symbols": [], "status": "unmapped", "note": "No counterpart inventoried."},
				"ExtensionData": {"go_symbols": [], "status": "intentional", "note": "Provider-specific metadata is intentional."},
				"Metadata": {"go_symbols": ["agent.Session.Get"], "status": "partial", "note": "Does not preserve all metadata."}
			},
			"methods": {
				"SerializeSessionAsync(Example.Agents.Session)": {"go": "session.MarshalJSON()", "go_symbols": ["agent.Session.MarshalJSON"], "status": "mapped", "note": "Serialization belongs to Session."},
				"RunAsync(string, System.Threading.CancellationToken)": {"go": "a.RunText(ctx, \"hello\").Collect()", "go_symbols": ["agent.Agent.RunText", "agent.ResponseStream.Collect"], "status": "adapted", "note": "Collects the string overload."},
				"RunAsync(System.Collections.Generic.IEnumerable<Example.Messages.Message>, System.Threading.CancellationToken)": {"go": "a.Run(ctx, messages).Collect()", "go_symbols": ["agent.Agent.Run", "agent.ResponseStream.Collect"], "status": "adapted", "note": "Collects the message overload."}
			},
			"constructors": {
				"Agent(string)": {"go": "agent.New(agent.ProviderConfig{}, agent.Config{})", "go_symbols": ["agent.New"], "status": "adapted", "note": "Go constructor function."}
			},
			"fields": {
				"Enabled": {"go": "agent.Config{Enabled: true}", "go_symbols": ["agent.Config.Enabled"], "status": "mapped", "note": "Configuration field."}
			},
			"constants": {
				"DefaultName": {"go": "agent.DefaultName", "go_symbols": ["agent.DefaultName"], "status": "mapped", "note": "Default name constant."}
			}
		}
	}
}`
	sampleCatalogJSON = `{"schema_version": 0, "baseline": ` + sampleBaselineJSON + `, "namespaces": ` + sampleNamespacesJSON + `}`
	validMappingJSON  = `{"go":"agent.Agent{}","go_symbols":["agent.Agent"],"status":"mapped","note":"Counterpart only."}`
	validPropertyJSON = `{"go_symbols":["agent.Agent"],"status":"mapped","note":"Counterpart only."}`
)

func TestRepositoryCatalog(t *testing.T) {
	// Exercise the default path from the catalog module root, without Git or source
	// line assertions that would require maintaining a second symbol inventory.
	t.Chdir(filepath.Join("..", ".."))
	if _, err := os.Stat(defaultCatalogFile); errors.Is(err, os.ErrNotExist) {
		t.Skip("development catalog is not included in module downloads")
	}
	var got struct {
		Baseline map[string]any    `json:"baseline"`
		Mappings []json.RawMessage `json:"mappings"`
	}
	if err := json.Unmarshal([]byte(commandOutput(t, "mappings", "-json")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Mappings) == 0 || len(got.Baseline) == 0 {
		t.Fatal("repository report must contain mappings and their baseline")
	}
}

func TestRepositoryIndexedViews(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Go indexing needs the module, not the mapping catalog or the .NET inventory.
	t.Chdir(t.TempDir())

	var goReport reconciliationGoInventory
	decodeOutput(t, commandOutput(t, "go", "-go-root", root, "-summary", "-json"), &goReport)
	if goReport.Module != "github.com/microsoft/agent-framework-go" || goReport.ExportedSymbols == 0 || len(goReport.Packages) == 0 {
		t.Fatalf("Go inventory is incomplete: %+v", goReport)
	}
	var goPage goInventory
	decodeOutput(t, commandOutput(t, "go", "-go-root", root, "-symbol", "agent.Session"), &goPage)
	if goPage.Page == nil || goPage.Page.Total < len(goPage.Symbols) || goPage.Page.Returned != len(goPage.Symbols) || goPage.Page.Limit != defaultPageLimit {
		t.Fatalf("default Go page is incomplete: %+v", goPage.Page)
	}

	t.Chdir(filepath.Join(root, "_catalog"))
	t.Run("catalog reconciliation", func(t *testing.T) {
		if _, err := os.Stat(defaultCatalogFile); errors.Is(err, os.ErrNotExist) {
			t.Skip("development catalog is not included in module downloads")
		}
		var reconciliation reconciliationSummary
		decodeOutput(t, commandOutput(t, "reconcile", "-summary", "-check", "-json"), &reconciliation)
		if reconciliation.InventoryDeclarations == 0 || reconciliation.AssessedDeclarations == 0 {
			t.Fatalf("reconciliation is incomplete: %+v", reconciliation)
		}
		if reconciliation.Counts["needs-reconciliation"] != 0 || reconciliation.Counts["invalid-go-target"] != 0 {
			t.Fatalf("strict reconciliation contains failing states: %v", reconciliation.Counts)
		}
		var queue reconciliationReport
		decodeOutput(t, commandOutput(t, "reconcile", "-state", "unreviewed", "-area", "workflows"), &queue)
		if queue.Page == nil || queue.Page.Total <= defaultPageLimit || queue.Page.Returned != defaultPageLimit || len(queue.Rows) != defaultPageLimit || queue.Counts["unreviewed"] != queue.Page.Total {
			t.Fatalf("default review queue page is incomplete: page=%+v rows=%d counts=%v", queue.Page, len(queue.Rows), queue.Counts)
		}
	})

	var out, diagnostics bytes.Buffer
	err = run([]string{"reconcile", "-file", writeCatalog(t, catalogWithInventory(t, sampleCatalogJSON, sampleAPIInventoryJSON)), "-summary"}, &out, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "does not match catalog go_module") || out.Len() != 0 {
		t.Fatalf("wrong Go module must fail without a report: %v, %q", err, out.String())
	}
}

func TestReconcileStates(t *testing.T) {
	const sourceCommit = "1111111111111111111111111111111111111111"
	const goCommit = "2222222222222222222222222222222222222222"
	example := "agent.Agent{}"
	row := func(name string, symbols ...string) mappingRow {
		return mappingRow{
			Area: "agents", Kind: "type", Namespace: "Example", Type: name, Dotnet: name,
			Assembly: "Core", Status: "mapped", Note: "Reviewed.", Go: &example, GoSymbols: symbols,
			typeName: "Example." + name,
		}
	}
	report := mappingsReport{
		Baseline: symbolcatalog.Baseline{DotnetCommit: sourceCommit, GoCommit: goCommit},
		Mappings: []mappingRow{
			row("Agent", "agent.Agent"),
			func() mappingRow {
				r := row("Agent", "agent.Agent.Run")
				r.Kind, r.Member, r.Dotnet = "method", "Run(string)", "Agent.Run(string)"
				r.Go = new("a.Run(value)")
				return r
			}(),
			row("BrokenTarget", "agent.Missing"),
			row("OutsideGo", "provider/example.Agent"),
			row("Missing", "agent.Agent"),
			func() mappingRow {
				r := row("External", "agent.Agent")
				r.Assembly = "External"
				return r
			}(),
		},
	}
	inv := symbolcatalog.Inventory{
		SchemaVersion: 1, IdentityFormat: "ecma335-v1", SHA256: "inventory",
		Assemblies: map[string]symbolcatalog.Assembly{"Core": {InformationalVersion: "1.0.0+" + sourceCommit}},
		Types: map[string]symbolcatalog.Declaration{
			"Example.Agent": {
				Assembly: "Core", Kind: "class",
				Methods: map[string]symbolcatalog.Method{
					"Run(System.String) -> System.String": {
						ReturnType: "System.String", Parameters: []symbolcatalog.Parameter{{Type: "System.String"}},
					},
				},
			},
			"Example.BrokenTarget": {Assembly: "Core", Kind: "class"},
			"Example.OutsideGo":    {Assembly: "Core", Kind: "class"},
			"Example.Unreviewed":   {Assembly: "Core", Kind: "class"},
		},
	}
	api := goInventory{
		Module: "example.org/sdk", Commit: goCommit, Packages: []string{"agent"},
		Symbols: map[string]goSymbol{
			"agent.Agent":     {Kind: "type", Signature: "type agent.Agent struct"},
			"agent.Agent.Run": {Kind: "method", Signature: "func (agent.Agent).Run(string) string"},
		},
	}
	got := reconcile(report, inv, api, false)
	want := map[string]int{
		"linked": 2, "unreviewed": 1, "needs-reconciliation": 1,
		"invalid-go-target": 1, "outside-inventory-scope": 1, "go-outside-scope": 1,
	}
	if !reflect.DeepEqual(got.Counts, want) {
		t.Fatalf("reconciliation states = %v, want %v", got.Counts, want)
	}
	if got.InventoryDeclarations != 5 || got.AssessedDeclarations != 6 {
		t.Fatalf("reconciliation totals = %d inventory, %d assessed", got.InventoryDeclarations, got.AssessedDeclarations)
	}
	if row := got.Rows[0]; row.Dotnet != "Agent.Run(string) -> string" || row.Member != "Run(string) -> string" || row.State != "linked" {
		t.Errorf("short source method did not resolve to its complete return label: %+v", row)
	}
	if report.Mappings[1].Member != "Run(string)" {
		t.Fatal("reconciliation changed the short source method key")
	}

	duplicate := row("Agent", "agent.Agent")
	conflict := reconcile(mappingsReport{Baseline: report.Baseline, Mappings: []mappingRow{report.Mappings[0], duplicate}}, inv, api, true)
	if conflict.Counts["needs-reconciliation"] != 2 || conflict.Counts["unreviewed"] != 5 {
		t.Fatalf("duplicate claims states = %v", conflict.Counts)
	}
	var output bytes.Buffer
	err := writeReconciliation(&output, got, reportFilter{}, pageOptions{Limit: 1}, true, false, true)
	if err == nil || !strings.Contains(err.Error(), "reconciliation check failed") {
		t.Fatalf("an invalid target outside the first page must fail -check: %v", err)
	}
	var page reconciliationReport
	decodeOutput(t, output.String(), &page)
	if page.Page == nil || page.Page.Total != len(got.Rows) || page.Page.Returned != 1 || len(page.Rows) != 1 || !reflect.DeepEqual(page.Counts, got.Counts) {
		t.Fatalf("paginated reconciliation changed counts: page=%+v rows=%d counts=%v", page.Page, len(page.Rows), page.Counts)
	}
	for _, obsolete := range []string{"reviews", "review"} {
		if strings.Contains(output.String(), `"`+obsolete+`":`) {
			t.Fatalf("reconciliation retained obsolete %q metadata: %s", obsolete, output.String())
		}
	}
	for _, brief := range []bool{false, true} {
		var text bytes.Buffer
		if err := writeReconciliation(&text, got, reportFilter{}, pageOptions{}, false, brief, false); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text.String(), "REVIEW") || strings.Contains(text.String(), "Review ") {
			t.Fatalf("reconciliation text retained named review metadata: %s", text.String())
		}
		for _, want := range []string{sourceCommit, goCommit, ".NET inventory: schema 1; identity ecma335-v1; SHA256 inventory.", "Go inventory: module example.org/sdk"} {
			if !strings.Contains(text.String(), want) {
				t.Fatalf("reconciliation text lost baseline or extraction provenance %q: %s", want, text.String())
			}
		}
	}
	for _, test := range []struct {
		name, source, goCommit   string
		sourceChanged, goChanged *bool
	}{
		{"original commits", sourceCommit, goCommit, new(false), new(false)},
		{"changed source", strings.Repeat("3", 40), goCommit, new(true), new(false)},
		{"changed Go", sourceCommit, strings.Repeat("4", 40), new(false), new(true)},
		{"unknown commits", "", "", nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := inv
			inventory.Assemblies = maps.Clone(inv.Assemblies)
			inventory.Assemblies["Core"] = symbolcatalog.Assembly{InformationalVersion: "1.0.0+" + test.source}
			index := api
			index.Commit = test.goCommit
			current := reconcile(report, inventory, index, false)
			for _, row := range current.Rows {
				if row.State == "linked" && (!reflect.DeepEqual(row.ReviewSourceChanged, test.sourceChanged) || !reflect.DeepEqual(row.ReviewGoChanged, test.goChanged)) {
					t.Fatalf("commit comparisons did not use the original baseline: %+v", row)
				}
			}
		})
	}
}

func TestFlattenMappings(t *testing.T) {
	file := writeCatalog(t, sampleCatalogJSON)
	got := readMappings(t, "-file", file)
	if want := expectedMappings(); !reflect.DeepEqual(got.Mappings, want) {
		t.Fatalf("mappings = %#v, want %#v", got.Mappings, want)
	}
	assertBaseline(t, got.Baseline)
	if got := commandOutput(t, "mappings", "-file", file); got != commandOutput(t, "mappings", "-file", file, "-json=true") {
		t.Fatal("mappings must emit JSON by default")
	}

	emptyExample := strings.Replace(sampleCatalogJSON,
		`"go": "agent.Config{Enabled: true}", "go_symbols": ["agent.Config.Enabled"], "status": "mapped"`,
		`"go": "", "go_symbols": [], "status": "unmapped"`, 1)
	readMappings(t, "-file", writeCatalog(t, emptyExample))
	t.Run("mapped notes optional", func(t *testing.T) {
		data := strings.Replace(sampleCatalogJSON, `"go_symbols": ["agent.New"], "status": "adapted"`, `"go_symbols": ["agent.New"], "status": "mapped"`, 1)
		for _, note := range []string{
			"Execution runtime counterpart.", "Name accessor.", "Serialization belongs to Session.",
			"Go constructor function.", "Configuration field.", "Default name constant.",
		} {
			data = strings.Replace(data, `, "note": "`+note+`"`, "", 1)
		}
		output := commandOutput(t, "mappings", "-file", writeCatalog(t, data), "-status", "mapped", "-limit=0")
		var got reportedMappings
		decodeOutput(t, output, &got)
		var want []reportedMapping
		for _, row := range expectedMappings() {
			if row.Kind == "constructor" {
				row.Status = "mapped"
			}
			if row.Status == "mapped" {
				row.Note = ""
				want = append(want, row)
			}
		}
		if !reflect.DeepEqual(got.Mappings, want) || strings.Contains(output, `"note":`) {
			t.Fatalf("mapped entries without notes changed or emitted empty notes: %s", output)
		}
	})
}

func TestUnreviewedMappings(t *testing.T) {
	t.Setenv("GOWORK", "off")
	const namespaces = `{"Example":{"Placeholder":{"area":"agents","assembly":"Core","mapping":{"go":"","go_symbols":[],"status":"unmapped","note":"No counterpart found."}}}}`
	data := strings.Replace(sampleCatalogJSON, sampleNamespacesJSON, namespaces, 1)
	inventory := strings.Replace(sampleAPIInventoryJSON, `"kind":"class"`, `"kind":"class","methods":{"Pending() -> System.Void":{"return_type":"System.Void"}},"properties":{"Name -> System.String":{"type":"System.String"}}`, 1)
	inventory = strings.Replace(inventory, `"types":{`, `"types":{"Example.Unreviewed":{"assembly":"Core","kind":"class"},`, 1)
	file := writeCatalog(t, catalogWithInventory(t, data, inventory))
	got := readMappings(t, "-file", file, "-limit=0")
	assertBaseline(t, got.Baseline)
	if len(got.Mappings) != 4 || got.Page.Total != 4 {
		t.Fatalf("default mappings omitted declarations: %+v", got)
	}
	want := []reportedMapping{{
		Area: "agents", Kind: "type", Namespace: "Example", Dotnet: "Placeholder",
		GoSymbols: []string{}, Status: "unmapped", Note: "No counterpart found.", Assembly: "Core",
	}}
	wantUnreviewed := map[string]string{"Unreviewed": "type", "Placeholder.Pending() -> void": "method", "Placeholder.Name": "property"}
	unreviewed := make(map[string]string)
	for _, row := range got.Mappings {
		if !row.Unreviewed {
			if !reflect.DeepEqual(row, want[0]) {
				t.Fatalf("extraction changed the existing assessment: %+v", row)
			}
			continue
		}
		if row.Status != "unmapped" || row.Go != "" || len(row.GoSymbols) != 0 || row.Note != "" || row.Namespace != "Example" || row.Assembly != "Core" {
			t.Fatalf("an extracted placeholder inherited an assessment or counterpart: %+v", row)
		}
		unreviewed[row.Dotnet] = row.Kind
	}
	if !reflect.DeepEqual(unreviewed, wantUnreviewed) {
		t.Fatalf("unreviewed declarations = %v, want %v", unreviewed, wantUnreviewed)
	}
	assessed := readMappings(t, "-file", file, "-assessed", "-limit=0")
	if !reflect.DeepEqual(assessed.Mappings, want) || !reflect.DeepEqual(assessed.Baseline, got.Baseline) {
		t.Fatalf("-assessed changed the recorded assessment or retained placeholders: %+v", assessed)
	}
	if all := readMappings(t, "-file", file, "-assessed=false", "-limit=0"); !reflect.DeepEqual(all, got) {
		t.Fatal("-assessed=false must retain unreviewed declarations")
	}
	for _, extra := range [][]string{nil, {"-assessed"}, {"-assessed=false"}} {
		args := append([]string{"-file", file, "-limit=0"}, extra...)
		gaps := readMappingsView(t, "gaps", args...)
		if !reflect.DeepEqual(gaps.Mappings, want) {
			t.Fatalf("gaps treated unreviewed placeholders as assessed gaps: %+v", gaps)
		}
	}
	for _, test := range []struct {
		name         string
		args         []string
		all, pending int
	}{
		{"default", nil, 4, 3},
		{"assessed", []string{"-assessed"}, 1, 0},
		{"explicit false", []string{"-assessed=false"}, 4, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			var summary reportedSummary
			decodeOutput(t, commandOutput(t, append([]string{"mappings", "-file", file, "-summary"}, test.args...)...), &summary)
			assertBaseline(t, summary.Baseline)
			if summary.DotnetSymbols != test.all || summary.UnreviewedSymbols != test.pending || summary.GoSymbols != 0 || summary.ByStatus["unmapped"] != test.all {
				t.Fatalf("summary confused extracted declarations with assessments: %+v", summary)
			}
		})
	}
	root := writeGoCheckout(t, "example.org/sdk", map[string]string{"agent/agent.go": "package agent\n"})
	args := []string{"reconcile", "-file", file, "-go-root", root, "-go-package", "./agent", "-check"}
	var report reconciliationReport
	decodeOutput(t, commandOutput(t, append(slices.Clone(args), "-limit=0")...), &report)
	var baseline symbolcatalog.Baseline
	decodeOutput(t, sampleBaselineJSON, &baseline)
	if report.InventoryDeclarations != 4 || report.AssessedDeclarations != 1 || len(report.Rows) != 4 || report.Counts["linked"] != 1 || report.Counts["unreviewed"] != 3 || !reflect.DeepEqual(report.Baseline, baseline) {
		t.Fatalf("reconciliation counted placeholders as assessments or advanced the baseline: %+v", report)
	}
	unreviewed = make(map[string]string)
	for _, row := range report.Rows {
		if row.State != "unreviewed" {
			if row.Dotnet != "Placeholder" || row.ReviewSourceChanged == nil || *row.ReviewSourceChanged || row.ReviewGoChanged != nil {
				t.Fatalf("reconciliation lost the original assessment baseline: %+v", row)
			}
			continue
		}
		if row.Status != "" || row.Note != "" || len(row.GoSymbols) != 0 || row.ReviewSourceChanged != nil || row.ReviewGoChanged != nil {
			t.Fatalf("unreviewed reconciliation inherited assessment metadata: %+v", row)
		}
		unreviewed[row.Dotnet] = row.Kind
	}
	if !reflect.DeepEqual(unreviewed, wantUnreviewed) {
		t.Fatalf("reconciliation lost unreviewed declarations: %v", unreviewed)
	}
	var summary reconciliationSummary
	decodeOutput(t, commandOutput(t, append(slices.Clone(args), "-assessed", "-summary")...), &summary)
	if summary.InventoryDeclarations != 4 || summary.AssessedDeclarations != 1 || summary.Counts["linked"] != 1 || summary.Counts["unreviewed"] != 0 || !reflect.DeepEqual(summary.Baseline, baseline) {
		t.Fatalf("-assessed reconciliation changed totals or the baseline: %+v", summary)
	}
	t.Run("assessed method return", func(t *testing.T) {
		const method = `{"go":"a.Pending()","go_symbols":["agent.Agent.Pending"],"status":"adapted","note":"Go method counterpart."}`
		assessedData := strings.Replace(data, `"mapping":`, `"methods":{"Pending()":`+method+`},"mapping":`, 1)
		assessedFile := writeCatalog(t, catalogWithInventory(t, assessedData, inventory))
		got := readMappings(t, "-file", assessedFile, "-kind", "method", "-assessed", "-limit=0")
		assertBaseline(t, got.Baseline)
		want := []reportedMapping{{
			Area: "agents", Kind: "method", Namespace: "Example", Dotnet: "Placeholder.Pending() -> void",
			Go: "a.Pending()", GoSymbols: []string{"agent.Agent.Pending"}, Status: "adapted",
			Note: "Go method counterpart.", Assembly: "Core",
		}}
		if !reflect.DeepEqual(got.Mappings, want) {
			t.Fatalf("merged method lost its return label or assessment: %+v", got.Mappings)
		}
	})
}

func TestMappingPages(t *testing.T) {
	file := writeCatalog(t, sampleCatalogJSON)
	first := readMappings(t, "-file", file, "-limit", "4")
	if first.Page == nil || first.Page.Total != 15 || first.Page.Offset != 0 || first.Page.Limit != 4 || first.Page.Returned != 4 || first.Page.NextOffset == nil || *first.Page.NextOffset != 4 {
		t.Fatalf("first page = %+v", first.Page)
	}
	if !reflect.DeepEqual(first.Mappings, expectedMappings()[:4]) {
		t.Fatalf("first page mappings = %+v", first.Mappings)
	}
	second := readMappings(t, "-file", file, "-limit", "4", "-offset", "4")
	if second.Page == nil || second.Page.Total != 15 || second.Page.Offset != 4 || second.Page.NextOffset == nil || *second.Page.NextOffset != 8 || !reflect.DeepEqual(second.Mappings, expectedMappings()[4:8]) {
		t.Fatalf("second page = %+v, mappings = %+v", second.Page, second.Mappings)
	}
	last := readMappings(t, "-file", file, "-limit", "4", "-offset", "12")
	if last.Page == nil || last.Page.Returned != 3 || last.Page.NextOffset != nil || !reflect.DeepEqual(last.Mappings, expectedMappings()[12:]) {
		t.Fatalf("last page = %+v, mappings = %+v", last.Page, last.Mappings)
	}
	empty := readMappings(t, "-file", file, "-limit", "4", "-offset", "50")
	if empty.Page == nil || empty.Page.Total != 15 || empty.Page.Returned != 0 || empty.Page.NextOffset != nil || len(empty.Mappings) != 0 {
		t.Fatalf("empty page = %+v, mappings = %+v", empty.Page, empty.Mappings)
	}
	full := readMappings(t, "-file", file, "-limit", "0")
	if full.Page == nil || full.Page.Total != 15 || full.Page.Limit != 0 || full.Page.Returned != 15 || full.Page.NextOffset != nil || !reflect.DeepEqual(full.Mappings, expectedMappings()) {
		t.Fatalf("unbounded page = %+v, mappings = %+v", full.Page, full.Mappings)
	}
	gaps := readMappingsView(t, "gaps", "-file", file, "-limit", "2")
	if gaps.Page == nil || gaps.Page.Total != 3 || gaps.Page.Returned != 2 || gaps.Page.NextOffset == nil || *gaps.Page.NextOffset != 2 {
		t.Fatalf("filtered gaps page = %+v", gaps.Page)
	}
}

func TestGoInventoryPages(t *testing.T) {
	api := goInventory{
		Module: "example.org/sdk", Packages: []string{"agent", "tool"},
		Symbols: map[string]goSymbol{
			"agent.A": {Kind: "type"}, "agent.Z": {Kind: "type"}, "tool.B": {Kind: "type"},
		},
	}
	var out bytes.Buffer
	if err := writeGoInventory(&out, api, "AGENT.", pageOptions{Limit: 1, Offset: 1}, true, false); err != nil {
		t.Fatal(err)
	}
	var got goInventory
	decodeOutput(t, out.String(), &got)
	if got.Page == nil || got.Page.Total != 2 || got.Page.Offset != 1 || got.Page.Returned != 1 || got.Page.NextOffset != nil || len(got.Symbols) != 1 || got.Symbols["agent.Z"].Kind != "type" {
		t.Fatalf("filtered Go index page = %+v, symbols = %v", got.Page, got.Symbols)
	}
	out.Reset()
	if err := writeGoInventory(&out, api, "agent.", pageOptions{Limit: 0}, true, false); err != nil {
		t.Fatal(err)
	}
	decodeOutput(t, out.String(), &got)
	if got.Page == nil || got.Page.Total != 2 || got.Page.Returned != 2 || len(got.Symbols) != 2 {
		t.Fatalf("unbounded Go index page = %+v, symbols = %v", got.Page, got.Symbols)
	}
}

func TestGoChanges(t *testing.T) {
	t.Setenv("GOWORK", "off")
	oldRoot := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent.go": `package agent
import "example.org/sdk/internal/helper"
type Config struct { Name string }
type ConfigAlias = Config
type ProviderConfig struct { ServiceDoesNotManageHistory bool }
type Response struct{}
type ResponseUpdate struct{}
type Box[T any] struct { Value T }
const Default = 1
func Run(int) {}
func Behavior() int { return helper.Value }
func init() { panic("indexing must not execute initializers") }
`,
		"internal/helper/helper.go": "package helper\nconst Value = 1\n",
		"tool/removed/tool.go":      "package removed\nfunc OldTool() {}\n",
		"agent/cmd/main.go":         "package main\nfunc main() {}\nfunc OldCommand() {}\n",
	})
	newRoot := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent.go": `package agent
import "example.org/sdk/internal/helper"
type Config struct {
	Name string
	RequirePerServiceCallHistoryPersistence bool
}
type ConfigAlias = Config
type ProviderConfig struct{}
type Response struct { ConversationID *string }
type ResponseUpdate struct { ConversationID *string }
type Box[T any] struct { Value T }
const Default = 2
func Run(string) {}
func (Config) Enabled() bool { return true }
func Behavior() int { return helper.Value }
func init() { panic("indexing must not execute initializers") }
`,
		"internal/helper/helper.go": "package helper\nconst Value = 2\nfunc NewInternal() {}\n",
		"tool/added/tool.go":        "package added\nfunc NewTool() {}\n",
		"agent/cmd/main.go":         "package main\nfunc main() {}\nfunc NewCommand() {}\n",
	})
	// No catalog or .NET inventory exists here: candidate generation is independent.
	t.Chdir(t.TempDir())
	args := []string{"changes", "-old-root", oldRoot, "-go-root", newRoot, "-go-package", "./..."}
	fullArgs := append(append([]string{}, args...), "-limit=0")
	output := commandOutput(t, fullArgs...)
	var got goChangesReport
	decodeOutput(t, output, &got)
	if got.Old.Module != "example.org/sdk" || got.New.Module != got.Old.Module || got.Old.GOOS == "" || got.New.GOOS != got.Old.GOOS {
		t.Fatalf("missing comparison provenance: %+v", got.goChangesSummary)
	}
	if !reflect.DeepEqual(got.Old.Packages, []string{"agent", "tool/removed"}) || !reflect.DeepEqual(got.New.Packages, []string{"agent", "tool/added"}) {
		t.Fatalf("unexpected comparison packages: old=%v new=%v", got.Old.Packages, got.New.Packages)
	}
	for _, want := range []struct {
		message    string
		compatible bool
	}{
		{"Config.RequirePerServiceCallHistoryPersistence: added", true},
		{"Response.ConversationID: added", true},
		{"ResponseUpdate.ConversationID: added", true},
		{"ProviderConfig.ServiceDoesNotManageHistory: removed", false},
		{"Config.Enabled: added", true},
		{"Run: changed", false},
		{"Default: value changed", false},
		{"package example.org/sdk/tool/added: added", true},
		{"package example.org/sdk/tool/removed: removed", false},
	} {
		found := false
		for _, change := range got.Changes {
			if strings.Contains(change.Message, want.message) {
				found = true
				if change.Compatible != want.compatible {
					t.Errorf("change %q compatible=%v, want %v", change.Message, change.Compatible, want.compatible)
				}
			}
		}
		if !found {
			t.Errorf("missing API change %q: %s", want.message, output)
		}
	}
	for _, change := range got.Changes {
		for _, excluded := range []string{"internal/", "OldCommand", "NewCommand", "Behavior", "Box"} {
			if strings.Contains(change.Message, excluded) {
				t.Errorf("unexpected API change: %+v", change)
			}
		}
	}
	if got.Compatible == 0 || got.Incompatible == 0 || got.Compatible+got.Incompatible != len(got.Changes) || got.Page.Total != len(got.Changes) {
		t.Fatalf("incorrect change counts: %+v", got)
	}
	if next := commandOutput(t, fullArgs...); next != output {
		t.Fatal("API change output is not deterministic")
	}
	var page goChangesReport
	decodeOutput(t, commandOutput(t, append(args, "-limit=1", "-offset=1")...), &page)
	if page.Page.Total != len(got.Changes) || page.Page.Returned != 1 || !reflect.DeepEqual(page.Changes, got.Changes[1:2]) || page.Compatible != got.Compatible || page.Incompatible != got.Incompatible {
		t.Fatalf("paging changed coverage counts or order: %+v", page)
	}
	decodeOutput(t, commandOutput(t, append(args, "-symbol", "REQUIREPERSERVICECALLHISTORYPERSISTENCE", "-limit=0")...), &page)
	if page.Page.Total == 0 || page.Incompatible != 0 || page.Compatible != len(page.Changes) {
		t.Fatalf("compatible additions disappeared from filtered output: %+v", page)
	}
	var summary goChangesSummary
	decodeOutput(t, commandOutput(t, append(args, "-summary")...), &summary)
	if !reflect.DeepEqual(summary, got.goChangesSummary) {
		t.Fatalf("summary changed counts or provenance: %+v", summary)
	}
	text := commandOutput(t, append(fullArgs, "-json=false")...)
	for _, want := range []string{"Structural API changes only", "compatible:", "incompatible:", "RequirePerServiceCallHistoryPersistence"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output missing %q: %s", want, text)
		}
	}
}

func TestGoChangesScopeAndLimits(t *testing.T) {
	t.Setenv("GOWORK", "off")
	oldRoot := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent.go":  "package agent\n// Config is the original configuration.\ntype Config struct { Value string `json:\"old\"` }\nfunc Value() int { return 1 }\n",
		"agent/tagged.go": "//go:build api_extra\n\npackage agent\ntype Extra struct{}\n",
	})
	newRoot := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent.go":  "package agent\n// Config has updated documentation.\ntype Config struct { Value string `json:\"new\"` }\nfunc Value() int { return 2 }\n",
		"agent/tagged.go": "//go:build api_extra\n\npackage agent\ntype Extra struct { Enabled bool }\n",
	})
	args := []string{"changes", "-old-root", oldRoot, "-go-root", newRoot, "-go-package", "./agent"}
	var got goChangesReport
	decodeOutput(t, commandOutput(t, args...), &got)
	if got.Changes == nil || len(got.Changes) != 0 || got.Page.Total != 0 || got.Compatible != 0 || got.Incompatible != 0 {
		t.Fatalf("body/doc/tag-only changes must not appear as structural API changes: %+v", got)
	}
	decodeOutput(t, commandOutput(t, append(args, "-tags", "api_extra")...), &got)
	if got.Old.BuildTags != "api_extra" || got.New.BuildTags != "api_extra" || len(got.Changes) != 1 || !strings.Contains(got.Changes[0].Message, "Extra.Enabled: added") {
		t.Fatalf("build tags were not applied to both snapshots: %+v", got)
	}
}

func TestGoChangesInputErrors(t *testing.T) {
	t.Setenv("GOWORK", "off")
	valid := writeGoCheckout(t, "example.org/sdk", map[string]string{"agent.go": "package agent\ntype Config struct{}\n"})
	broken := writeGoCheckout(t, "example.org/sdk", map[string]string{"agent.go": "package agent\nvar Broken MissingType\n"})
	other := writeGoCheckout(t, "example.org/other", map[string]string{"agent.go": "package agent\ntype Config struct{}\n"})
	for _, test := range []struct {
		name, old, new, want string
	}{
		{"old load", broken, valid, "index old Go API"},
		{"new load", valid, broken, "index new Go API"},
		{"module mismatch", valid, other, "different Go modules"},
		{"missing checkout", filepath.Join(t.TempDir(), "missing"), valid, "index old Go API"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			err := run([]string{"changes", "-old-root", test.old, "-go-root", test.new, "-go-package", "./..."}, &out, &diagnostics)
			if err == nil || !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
				t.Fatalf("incomplete comparison must fail without a report: %v, %q", err, out.String())
			}
		})
	}
}

func TestSummary(t *testing.T) {
	file := writeCatalog(t, sampleCatalogJSON)
	var got reportedSummary
	decodeOutput(t, commandOutput(t, "mappings", "-summary", "-file", file, "-json"), &got)
	assertBaseline(t, got.Baseline)
	if got.DotnetSymbols != 15 || got.GoSymbols != 13 {
		t.Fatalf("summary must count 15 .NET mappings and 13 distinct Go symbols: %+v", got)
	}
	for _, test := range []struct {
		name string
		got  map[string]int
		want map[string]int
	}{
		{"status", got.ByStatus, map[string]int{"mapped": 5, "adapted": 6, "partial": 2, "unmapped": 1, "intentional": 1}},
		{"kind", got.ByKind, map[string]int{"type": 2, "constructor": 1, "property": 5, "method": 5, "field": 1, "constant": 1}},
		{"area", got.ByArea, map[string]int{"agents": 13, "messages": 0, "tools": 0, "providers": 0, "hosting": 0, "operations": 0, "workflows": 2}},
	} {
		if !reflect.DeepEqual(test.got, test.want) {
			t.Errorf("by_%s = %v, want %v", test.name, test.got, test.want)
		}
	}

	decodeOutput(t, commandOutput(t, "mappings", "-summary", "-file", file, "-json", "-area", "hosting"), &got)
	assertBaseline(t, got.Baseline)
	if got.DotnetSymbols != 0 || got.GoSymbols != 0 {
		t.Fatalf("empty selection must have zero symbol counts: %+v", got)
	}
	for _, counts := range []map[string]int{got.ByStatus, got.ByKind, got.ByArea} {
		for key, count := range counts {
			if count != 0 {
				t.Errorf("empty summary has %s count %d", key, count)
			}
		}
	}
}

func TestGoOnlyReports(t *testing.T) {
	data := catalogWithGoOnly(sampleCatalogJSON, `{
		"agent.LocalOptions": {"note":"Go-specific configuration."},
		"agent.OnlyInGo": {"note":"Go-specific helper."},
		"provider/other.Extra": {"note":"Go-specific provider helper."}
	}`)
	file := writeCatalog(t, data)
	read := func(args ...string) goOnlyReport {
		var report goOnlyReport
		output := commandOutput(t, append([]string{"go-only", "-file", file}, args...)...)
		decodeOutput(t, output, &report)
		if strings.Contains(output, `"reviews":`) || strings.Contains(output, `"review":`) {
			t.Fatalf("Go-only report retained obsolete review metadata: %s", output)
		}
		return report
	}
	got := read("-symbol", "AGENT.", "-limit", "1")
	if got.Page.Total != 2 || got.Page.Returned != 1 || got.Page.NextOffset == nil || *got.Page.NextOffset != 1 || got.GoOnly["agent.LocalOptions"].Note != "Go-specific configuration." {
		t.Fatalf("unexpected Go-only first page: %+v", got)
	}
	var baseline symbolcatalog.Baseline
	decodeOutput(t, sampleBaselineJSON, &baseline)
	if !reflect.DeepEqual(got.Baseline, baseline) {
		t.Fatalf("Go-only report changed the original baseline: %+v", got)
	}
	got = read("-symbol", "agent.", "-limit", "1", "-offset", "1")
	if got.Page.Total != 2 || got.Page.NextOffset != nil || got.GoOnly["agent.OnlyInGo"].Note != "Go-specific helper." {
		t.Fatalf("unexpected Go-only next page: %+v", got)
	}
	got = read("-limit", "0")
	if got.Page.Total != 3 || len(got.GoOnly) != 3 {
		t.Fatalf("incomplete Go-only export: %+v", got)
	}
	got = read("-symbol", "not-present")
	if got.GoOnly == nil || len(got.GoOnly) != 0 || got.Page.Total != 0 {
		t.Fatalf("empty query must return an empty object: %+v", got)
	}
	var summary reportedSummary
	decodeOutput(t, commandOutput(t, "mappings", "-summary", "-file", file), &summary)
	if summary.DotnetSymbols != 15 || summary.GoSymbols != 13 || summary.GoOnlySymbols != 3 {
		t.Fatalf("Go-only assessments changed .NET mapping counts: %+v", summary)
	}
	decodeOutput(t, commandOutput(t, "mappings", "-summary", "-file", file, "-symbol", "not-present"), &summary)
	if summary.DotnetSymbols != 0 || summary.GoSymbols != 0 || summary.GoOnlySymbols != 3 {
		t.Fatalf("Go-only catalog count must remain separate and unfiltered: %+v", summary)
	}
	if mappings := readMappings(t, "-file", file, "-limit", "0"); !reflect.DeepEqual(mappings.Mappings, expectedMappings()) {
		t.Fatal("Go-only entries changed the .NET mapping rows")
	}
	text := commandOutput(t, "go-only", "-file", file, "-json=false")
	for _, want := range []string{"Go-only assessments", "agent.OnlyInGo", "Go-specific helper."} {
		if !strings.Contains(text, want) {
			t.Errorf("text output missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "REVIEW") {
		t.Fatalf("Go-only text report retained a review column: %s", text)
	}
	got = read("-file", writeCatalog(t, sampleCatalogJSON))
	if got.GoOnly == nil || len(got.GoOnly) != 0 || got.Page.Total != 0 {
		t.Fatalf("catalog without optional section must report zero Go-only entries: %+v", got)
	}
}

func TestInvalidGoOnlyAssessments(t *testing.T) {
	for _, test := range []struct{ name, entries, want string }{
		{"unqualified", `{"OnlyInGo":{"note":"Go helper."}}`, "invalid qualified Go symbol"},
		{"unexported", `{"agent.onlyInGo":{"note":"Go helper."}}`, "invalid qualified Go symbol"},
		{"empty note", `{"agent.OnlyInGo":{"note":" "}}`, "note must not be empty"},
		{"obsolete review", `{"agent.OnlyInGo":{"note":"Go helper.","review":"baseline"}}`, `unknown field "review"`},
		{"obsolete null review", `{"agent.OnlyInGo":{"note":"Go helper.","review":null}}`, `unknown field "review"`},
		{"null entry", `{"agent.OnlyInGo":null}`, "note must not be empty"},
		{"no mapping status", `{"agent.OnlyInGo":{"note":"Go helper.","status":"unmapped"}}`, "unknown field"},
		{"mapped counterpart", `{"agent.Agent":{"note":"Go helper."}}`, "also assessed as go_only"},
		{"duplicate key", `{"agent.OnlyInGo":{"note":"Go helper."},"agent.OnlyInGo":{"note":"Another note."}}`, "duplicate JSON key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidCatalog(t, catalogWithGoOnly(sampleCatalogJSON, test.entries), test.want)
		})
	}
}

func TestGoOnlyReconciliation(t *testing.T) {
	t.Setenv("GOWORK", "off")
	root := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent.go":          "package agent\nfunc OnlyInGo() {}\n",
		"message/message.go":      "package message\n",
		"tool/tool.go":            "package tool\n",
		"workflow/workflow.go":    "package workflow\n",
		"provider/other/other.go": "package other\nfunc Extra() {}\n",
	})
	minimal := `{"schema_version":0,"baseline":` + sampleBaselineJSON + `,"namespaces":{"Example":{"Placeholder":{"area":"agents","assembly":"Core","mapping":{"go":"","go_symbols":[],"status":"unmapped","note":"No counterpart found."}}}}}`
	entries := `{"agent.OnlyInGo":{"note":"Go helper."},"agent.Missing":{"note":"Removed Go helper."},"provider/other.Extra":{"note":"Go provider helper."}}`
	inventory := sampleAPIInventoryJSON
	file := writeCatalog(t, catalogWithInventory(t, catalogWithGoOnly(minimal, entries), inventory))
	args := []string{"reconcile", "-file", file, "-go-root", root}
	var report reconciliationReport
	decodeOutput(t, commandOutput(t, args...), &report)
	if report.InventoryDeclarations != 1 || report.AssessedDeclarations != 1 || len(report.Rows) != 1 || report.Counts["linked"] != 1 {
		t.Fatalf("Go-only entries must not enter .NET row counts: %+v", report)
	}
	if report.GoOnly == nil || report.GoOnly.Assessed != 3 || report.GoOnly.Present != 2 || !reflect.DeepEqual(report.GoOnly.InvalidGoTargets, []string{"agent.Missing"}) || len(report.GoOnly.UnindexedGoTargets) != 0 {
		t.Fatalf("incorrect Go-only reconciliation: %+v", report.GoOnly)
	}
	var out, diagnostics bytes.Buffer
	err := run(append(args, "-symbol", "not-present", "-limit", "1", "-check"), &out, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "invalid Go-only targets") {
		t.Fatalf("hidden invalid Go-only target must fail strict checks: %v", err)
	}
	report = reconciliationReport{}
	decodeOutput(t, out.String(), &report)
	if len(report.Rows) != 0 || report.GoOnly.Assessed != 3 || len(report.GoOnly.InvalidGoTargets) != 1 {
		t.Fatalf("filters hid Go-only validation results: %+v", report)
	}
	var summary reconciliationSummary
	decodeOutput(t, commandOutput(t, append(args, "-summary", "-go-package", "./agent")...), &summary)
	if summary.GoOnly.Present != 1 || !reflect.DeepEqual(summary.GoOnly.UnindexedGoTargets, []string{"provider/other.Extra"}) {
		t.Fatalf("unindexed symbols were treated as missing or validated: %+v", summary.GoOnly)
	}
	validFile := writeCatalog(t, catalogWithInventory(t, catalogWithGoOnly(minimal, `{"agent.OnlyInGo":{"note":"Go helper."},"provider/other.Extra":{"note":"Go provider helper."}}`), inventory))
	validArgs := []string{"reconcile", "-file", validFile, "-go-root", root, "-check", "-summary"}
	summary = reconciliationSummary{}
	decodeOutput(t, commandOutput(t, validArgs...), &summary)
	if summary.GoOnly.Assessed != 2 || summary.GoOnly.Present != 2 {
		t.Fatalf("valid Go-only assessments rejected: %+v", summary.GoOnly)
	}
	// Like .NET target checks, an explicitly unindexed package is unknown, not invalid.
	commandOutput(t, append(validArgs, "-go-package", "./agent")...)
	text := commandOutput(t, append(args, "-json=false", "-summary")...)
	if !strings.Contains(text, "Invalid Go-only targets: agent.Missing") || !strings.Contains(text, "Go-only assessments: 3") {
		t.Fatalf("Go-only text diagnostics missing: %s", text)
	}
}

func TestFilteredReports(t *testing.T) {
	file := writeCatalog(t, sampleCatalogJSON)
	for _, test := range []struct {
		name    string
		args    []string
		symbols []string
		goCount int
	}{
		{"area", []string{"-area", "workflows"}, []string{runnerType + "." + runString, runnerType}, 2},
		{"kind", []string{"-kind", "constructor"}, []string{agentType + ".Agent(string)"}, 1},
		{"status", []string{"-status", "partial"}, []string{agentType + ".Metadata", runnerType + "." + runString}, 2},
		{"member-only type", []string{"-type", "EXAMPLE.AGENTS.STATEBAG"}, []string{stateType + "." + getValue}, 1},
		{"type mappings only", []string{"-type", "example.agents", "-kind", "type"}, []string{agentType}, 1},
		{"type does not match members", []string{"-type", "SerializeSessionAsync"}, nil, 0},
		{"type does not match Go", []string{"-type", "agent.Session"}, nil, 0},
		{"forward", []string{"-symbol", "EXAMPLE.AGENTS.AGENT.NAME"}, []string{agentType + ".Name"}, 1},
		{"overloads and parameter dots", []string{"-symbol", "System.Threading.CancellationToken"}, []string{agentType + "." + runList, agentType + "." + runString, runnerType + "." + runString}, 4},
		{"reverse", []string{"-symbol", "AGENT.RESPONSESTREAM.COLLECT"}, []string{agentType + "." + runList, agentType + "." + runString}, 3},
		{"different Go owner", []string{"-symbol", "agent.Session"}, []string{agentType + "." + serialize, agentType + ".Metadata", stateType + "." + getValue}, 2},
		{"Go function", []string{"-symbol", "agent.WithSession"}, []string{agentType + ".Options"}, 1},
		{"qualified Go package", []string{"-symbol", "WORKFLOW/INPROC.EXECUTIONENVIRONMENT.RUN"}, []string{runnerType + "." + runString}, 1},
		{"composed", []string{"-area", "agents", "-kind", "method", "-status", "adapted", "-type", "example.agents.agent", "-symbol", "agent.ResponseStream.Collect"}, []string{agentType + "." + runList, agentType + "." + runString}, 3},
		{"intersecting filters", []string{"-area", "workflows", "-status", "unmapped"}, nil, 0},
		{"notes are not symbols", []string{"-symbol", "No counterpart inventoried"}, nil, 0},
		{"empty area", []string{"-area", "hosting"}, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"-file", file}, test.args...)
			got := readMappings(t, args...)
			assertSelectedMappings(t, got, test.symbols)
			var s reportedSummary
			decodeOutput(t, commandOutput(t, append([]string{"mappings", "-summary", "-json"}, args...)...), &s)
			assertBaseline(t, s.Baseline)
			if s.DotnetSymbols != len(test.symbols) || s.GoSymbols != test.goCount {
				t.Fatalf("filtered summary = %+v, want %d .NET and %d Go symbols", s, len(test.symbols), test.goCount)
			}
		})
	}
}

func TestGaps(t *testing.T) {
	file := writeCatalog(t, sampleCatalogJSON)
	for _, test := range []struct {
		name    string
		args    []string
		symbols []string
	}{
		{"all", nil, []string{agentType + ".Metadata", agentType + ".Missing", runnerType + "." + runString}},
		{"mapped is not a gap", []string{"-status", "mapped"}, nil},
		{"adapted is not a gap", []string{"-status", "adapted"}, nil},
		{"intentional is not a gap", []string{"-status", "intentional"}, nil},
		{"unmapped", []string{"-status", "unmapped"}, []string{agentType + ".Missing"}},
		{"inherited area", []string{"-area", "agents", "-kind", "property"}, []string{agentType + ".Metadata", agentType + ".Missing"}},
		{"composed", []string{"-area", "workflows", "-kind", "method", "-status", "partial", "-type", "RUNNER", "-symbol", "EXECUTIONENVIRONMENT.RUN"}, []string{runnerType + "." + runString}},
		{"member-only adapted type", []string{"-type", "StateBag"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"-file", file}, test.args...)
			assertSelectedMappings(t, readMappingsView(t, "gaps", args...), test.symbols)
		})
	}
}

func TestGenericSymbolFormats(t *testing.T) {
	const typeName = "Box<T, System.Collections.Generic.List<U>>"
	const method = "Map<TResult>(System.Collections.Generic.Dictionary<string, Example.Value>, (int, System.String))"
	namespaces := `{"Example":{"` + typeName + `":{"area":"tools","mapping":` + validMappingJSON + `,
      "constructors":{"Box<T>()":` + validMappingJSON + `,"Box<T>(System.String)":` + validMappingJSON + `},
      "methods":{"` + method + `":` + validMappingJSON + `}}}}`
	file := writeCatalog(t, strings.Replace(sampleCatalogJSON, sampleNamespacesJSON, namespaces, 1))
	got := readMappings(t, "-file", file)
	want := []string{typeName + ".Box<T>()", typeName + ".Box<T>(System.String)", typeName + "." + method, typeName}
	if len(got.Mappings) != len(want) {
		t.Fatalf("mappings = %+v, want %v", got.Mappings, want)
	}
	for i, symbol := range want {
		if row := got.Mappings[i]; row.Namespace != "Example" || row.Dotnet != symbol {
			t.Errorf("mapping = %+v, want namespace %q and symbol %q", row, "Example", symbol)
		}
	}
}

func TestTextReports(t *testing.T) {
	file := writeCatalog(t, sampleCatalogJSON)
	for _, view := range []string{"mappings", "summary", "gaps"} {
		t.Run(view, func(t *testing.T) {
			out := commandOutput(t, mappingViewArgs(view, "-file", file, "-json=false")...)
			for _, warning := range []string{"Incomplete inventory", "No behavioral parity audit", "not semantic parity", "Go idiom, not a gap"} {
				if !strings.Contains(out, warning) {
					t.Errorf("text report is missing %q: %s", warning, out)
				}
			}
			if view == "summary" {
				if !strings.Contains(out, ".NET symbols: 15; distinct Go symbols: 13") {
					t.Fatalf("unexpected summary: %s", out)
				}
			} else {
				for _, symbol := range []string{runnerType + "." + runString, "workflow/inproc.ExecutionEnvironment.Run", agentType + ".Missing"} {
					if !strings.Contains(out, symbol) {
						t.Errorf("text report is missing symbol %q", symbol)
					}
				}
				if view == "mappings" && !strings.Contains(out, "agent.Session.MarshalJSON") {
					t.Fatal("text must retain the child's actual Go owner")
				}
				if view == "gaps" && strings.Contains(out, agentType+".ExtensionData") {
					t.Fatal("intentional differences must not appear as gaps")
				}
			}
		})
	}
	file = writeCatalog(t, strings.Replace(sampleCatalogJSON, `"inventory_complete": false`, `"inventory_complete": true`, 1))
	if out := commandOutput(t, "mappings", "-file", file, "-json=false"); !strings.Contains(out, "Complete inventory as declared") || !strings.Contains(out, "No behavioral parity audit") {
		t.Fatalf("inventory completeness must not imply behavioral parity: %s", out)
	}
	if out := commandOutput(t, "mappings", "-file", file, "-limit", "2", "-json=false"); !strings.Contains(out, "Page: offset 0; returned 2 of 15 matches; next offset 2.") {
		t.Fatalf("paginated text must show the next offset: %s", out)
	}
}

func TestTextReportWriteError(t *testing.T) {
	file := writeCatalog(t, sampleCatalogJSON)
	want := errors.New("output failed")
	var diagnostics bytes.Buffer
	err := run([]string{"mappings", "-file", file, "-json=false"}, errorReportWriter{want}, &diagnostics)
	if !errors.Is(err, want) {
		t.Fatalf("text report write error = %v, want %v", err, want)
	}
}

type errorReportWriter struct{ err error }

func (w errorReportWriter) Write([]byte) (int, error) { return 0, w.err }

func TestStableReadOnlyOutput(t *testing.T) {
	var reordered any
	if err := json.Unmarshal([]byte(sampleCatalogJSON), &reordered); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	first, second := writeCatalog(t, sampleCatalogJSON), writeCatalog(t, string(data))
	for _, view := range []string{"mappings", "summary", "gaps"} {
		for _, format := range []string{"-json=false", "-json=true"} {
			want := commandOutput(t, mappingViewArgs(view, "-file", first, format)...)
			for range 3 {
				if got := commandOutput(t, mappingViewArgs(view, "-file", second, format)...); got != want {
					t.Fatalf("%s %s output depends on object order or map iteration", view, format)
				}
			}
		}
	}
	if data, err := os.ReadFile(first); err != nil || string(data) != sampleCatalogJSON {
		t.Fatalf("command changed its input: %v", err)
	}
}

func TestInvalidCatalog(t *testing.T) {
	for _, test := range []struct {
		name string
		old  string
		new  string
		want string
	}{
		{"version", `"schema_version": 0`, `"schema_version": 1`, "schema_version"},
		{"missing version", `"schema_version": 0,`, ``, "schema_version"},
		{"obsolete reviews", `"namespaces":`, `"reviews":{},"namespaces":`, `unknown field "reviews"`},
		{"obsolete null reviews", `"namespaces":`, `"reviews":null,"namespaces":`, `unknown field "reviews"`},
		{"obsolete Go test queue", `"namespaces":`, `"go_tests":{"unreviewed":[],"reviewed":[]},"namespaces":`, `unknown field "go_tests"`},
		{"obsolete null Go test queue", `"namespaces":`, `"go_tests":null,"namespaces":`, `unknown field "go_tests"`},
		{"obsolete type review", `"mapping": {"go": "agent.Agent{}",`, `"mapping": {"review":"baseline","go": "agent.Agent{}",`, `unknown field "review"`},
		{"obsolete method review", `"go": "session.MarshalJSON()"`, `"go": "session.MarshalJSON()", "review":"baseline"`, `unknown field "review"`},
		{"obsolete property review", `"Name": {"go_symbols":`, `"Name": {"review":"baseline","go_symbols":`, `unknown field "review"`},
		{"obsolete null review", `"mapping": {"go": "agent.Agent{}",`, `"mapping": {"review":null,"go": "agent.Agent{}",`, `unknown field "review"`},
		{"date", `2026-09-23`, `2026-02-30`, "baseline"},
		{"dotnet commit", `1111111111111111111111111111111111111111`, `main`, "dotnet_commit"},
		{"Go commit", `2222222222222222222222222222222222222222`, `HEAD`, "go_commit"},
		{"null Go commit", `"go_commit": "2222222222222222222222222222222222222222"`, `"go_commit": null`, "go_commit"},
		{"missing Go commit", `"go_commit": "2222222222222222222222222222222222222222",`, ``, "go_commit"},
		{"dotnet repository", `https://example.org/dotnet`, `not a URL`, "baseline"},
		{"Go repository", `https://example.org/go`, `file:///local`, "baseline"},
		{"Go module", `example.org/sdk`, `../sdk`, "go_module"},
		{"scope", `Selected declarations, not a behavioral audit.`, ` `, "scope"},
		{"missing completeness", `"inventory_complete": false,`, ``, "inventory_complete"},
		{"null completeness", `"inventory_complete": false`, `"inventory_complete": null`, "inventory_complete"},
		{"empty namespaces", sampleNamespacesJSON, `{}`, "namespaces must not be empty"},
		{"null namespaces", sampleNamespacesJSON, `null`, "namespaces must not be empty"},
		{"invalid namespace", `"Example.Agents":`, `"Example..Agents":`, "invalid or empty namespace"},
		{"invalid type", `"Agent":`, `"Example..Agent":`, "namespace-qualified"},
		{"invalid generic type", `"Agent":`, `"Agent<T>>":`, "namespace-qualified"},
		{"area", `"area": "workflows"`, `"area": "samples"`, "invalid area"},
		{"status", `"status": "adapted"`, `"status": "aligned"`, "invalid status"},
		{"child status required", `"go_symbols": ["agent.Agent.Name"], "status": "mapped"`, `"go_symbols": ["agent.Agent.Name"]`, "invalid status"},
		{"adapted note required", `, "note": "Session option factory."`, ``, "note"},
		{"blank adapted note", `"note": "Session option factory."`, `"note": " \t "`, "note"},
		{"partial note required", `, "note": "Background execution is unavailable."`, ``, "note"},
		{"unmapped note required", `, "note": "No counterpart inventoried."`, ``, "note"},
		{"intentional note required", `, "note": "Provider-specific metadata is intentional."`, ``, "note"},
		{"child Go required", `"mapping": {"go": "agent.Agent{}", `, `"mapping": {`, "non-null string"},
		{"null Go", `"go": "agent.Agent{}"`, `"go": null`, "non-null string"},
		{"empty mapped Go", `"go": "agent.Agent{}"`, `"go": ""`, "empty go"},
		{"empty adapted Go", `"go": "agent.Agent{}"`, `"go": ""`, "empty go"},
		{"empty partial Go", `"go": "(*inproc.ExecutionEnvironment).Run"`, `"go": ""`, "empty go"},
		{"missing Go symbols", `, "go_symbols": ["agent.Agent"]`, ``, "go_symbols must be a non-null array"},
		{"null Go symbols", `"go_symbols": ["agent.Agent"]`, `"go_symbols": null`, "go_symbols must be a non-null array"},
		{"empty Go symbols", `"go_symbols": ["agent.Agent"]`, `"go_symbols": []`, "nonempty go requires go_symbols"},
		{"symbols on empty Go", `"go": "agent.Agent{}", "go_symbols": ["agent.Agent"], "status": "adapted"`, `"go": "", "go_symbols": ["agent.Agent"], "status": "unmapped"`, "empty go requires empty go_symbols"},
		{"property Go example", `"Name": {"go_symbols": ["agent.Agent.Name"]`, `"Name": {"go": "agent.Agent{}", "go_symbols": ["agent.Agent.Name"]`, "properties must not define Go examples"},
		{"duplicate Go target", `"go_symbols": ["agent.Agent"]`, `"go_symbols": ["agent.Agent", "agent.Agent"]`, "duplicate Go target"},
		{"property key", `"Name":`, `"Name(":`, "invalid property key"},
		{"field key", `"Enabled":`, `"Config.Enabled":`, "invalid field key"},
		{"constant key", `"DefaultName":`, `"Default Name":`, "invalid constant key"},
		{"constructor key", `"Agent(string)":`, `"Agent":`, "invalid constructor key"},
		{"method parentheses required", `"Get<T>(System.String)":`, `"Get<T>":`, "invalid method key"},
		{"unclosed method", `"Get<T>(System.String)":`, `"Get<T>((System.String)":`, "invalid method key"},
		{"extra method parentheses", `"Get<T>(System.String)":`, `"Get<T>()()":`, "invalid method key"},
		{"unknown group", `"constants":`, `"callbacks":`, `unknown field "callbacks"`},
		{"legacy path", `"go_symbols": ["agent.Agent"]`, `"go_symbols": ["agent.Agent"], "path": "agent/agent.go"`, `unknown field "path"`},
		{"legacy line", `"go_symbols": ["agent.Agent"]`, `"go_symbols": ["agent.Agent"], "line": 12`, `unknown field "line"`},
		{"legacy package", `"go_symbols": ["agent.Agent"]`, `"go_symbols": ["agent.Agent"], "package": "agent"`, `unknown field "package"`},
		{"child area is not a field", `"go_symbols": ["agent.Agent.Name"]`, `"go_symbols": ["agent.Agent.Name"], "area": "agents"`, `unknown field "area"`},
		{"explicit kind is not a field", `"go_symbols": ["agent.Agent.Name"]`, `"go_symbols": ["agent.Agent.Name"], "kind": "property"`, `unknown field "kind"`},
		{"legacy flat array", `"go": "agent.Agent{}"`, `"go": ["agent.Agent"]`, "cannot unmarshal array"},
		{"legacy target object", `"go": "agent.Agent{}"`, `"go": {"types": [{"package":"agent","symbol":"Agent"}]}`, "cannot unmarshal object"},
		{"outer function example", `"go": "agent.Agent{}"`, `"go": "func() { _ = agent.Agent{} }"`, "synthetic outer function"},
		{"invalid example", `"go": "agent.Agent{}"`, `"go": "agent.Agent{"`, "invalid Go example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(sampleCatalogJSON, test.old) {
				t.Fatal("test replacement does not match the catalog")
			}
			assertInvalidCatalog(t, strings.Replace(sampleCatalogJSON, test.old, test.new, 1), test.want)
		})
	}
}

func TestInvalidGoTargets(t *testing.T) {
	for _, target := range []string{
		"", "Agent", "agent.agent", "agent.Agent.run", "agent.Agent.Run.Extra", "agent.Agent.Run()",
		"agent/agent.go:12", "agent.Agent:12", "/agent.Agent", "../agent.Agent", "agent/../agent.Agent",
		`agent\sub.Agent`, "agent//sub.Agent", "example.org/sdk/agent.Agent", " agent.Agent", "agent.Agent ",
	} {
		t.Run(target, func(t *testing.T) {
			encoded, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			data := strings.Replace(sampleCatalogJSON, `"go_symbols": ["agent.Agent"]`, `"go_symbols": [`+string(encoded)+`]`, 1)
			assertInvalidCatalog(t, data, "invalid qualified Go symbol")
		})
	}
}

func TestGoExampleFreeVariables(t *testing.T) {
	pkg := types.NewPackage("example.org/sdk", "sdk")
	parameter := types.NewVar(token.NoPos, pkg, "value", types.Universe.Lookup("any").Type())
	signature := types.NewSignatureType(nil, nil, nil, types.NewTuple(parameter), nil, false)
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "Call", signature))
	results := types.NewTuple(
		types.NewVar(token.NoPos, pkg, "value", types.Universe.Lookup("int").Type()),
		types.NewVar(token.NoPos, pkg, "err", types.Universe.Lookup("error").Type()),
	)
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "Pair", types.NewSignatureType(nil, nil, nil, types.NewTuple(parameter), results, false)))
	pkg.MarkComplete()
	api := goInventory{
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
		packages:  map[string]*types.Package{pkg.Path(): pkg},
	}
	if err := checkGoExample("sdk.Call(ctx); client.Call(ctx)", api); err != nil {
		t.Fatalf("check concise example: %v", err)
	}
	if err := checkGoExample("sdk.Pair(ctx)", api); err != nil {
		t.Fatalf("check multi-result call: %v", err)
	}
	if err := checkGoExample("sdk.Missing(ctx)", api); err == nil {
		t.Fatal("missing package member must not be treated as a free variable")
	}
}

func TestResolveGoExampleStandardImport(t *testing.T) {
	standard := types.NewPackage("encoding/json", "json")
	standard.Scope().Insert(types.NewFunc(token.NoPos, standard, "Unmarshal", types.NewSignatureType(nil, nil, nil, nil, nil, false)))
	standard.MarkComplete()
	dependency := types.NewPackage("example.org/internal/json", "json")
	dependency.Scope().Insert(types.NewFunc(token.NoPos, dependency, "Unmarshal", types.NewSignatureType(nil, nil, nil, nil, nil, false)))
	dependency.MarkComplete()
	imports, err := resolveGoExampleImports(
		map[string]map[string]bool{"json": {"Unmarshal": true}},
		map[string]*types.Package{standard.Path(): standard, dependency.Path(): dependency},
	)
	if err != nil {
		t.Fatal(err)
	}
	if imports["json"] != standard.Path() {
		t.Fatalf("json import = %q, want %q", imports["json"], standard.Path())
	}
}

func TestResolveGoExamplePublicImport(t *testing.T) {
	packages := make(map[string]*types.Package)
	for _, path := range []string{"example.org/sdk/checkpoint", "example.org/sdk/internal/checkpoint"} {
		pkg := types.NewPackage(path, "checkpoint")
		pkg.Scope().Insert(types.NewTypeName(token.NoPos, pkg, "Manager", types.Typ[types.Int]))
		pkg.MarkComplete()
		packages[path] = pkg
	}
	imports, err := resolveGoExampleImports(map[string]map[string]bool{"checkpoint": {"Manager": true}}, packages)
	if err != nil {
		t.Fatal(err)
	}
	if imports["checkpoint"] != "example.org/sdk/checkpoint" {
		t.Fatalf("checkpoint import = %q, want public package", imports["checkpoint"])
	}
}

func TestDuplicateJSONKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		old  string
		new  string
	}{
		{"root", `"schema_version": 0`, `"schema_version": 1, "schema_version": 0`},
		{"baseline", `"checked_at": "2026-09-23"`, `"checked_at": "invalid", "checked_at": "2026-09-23"`},
		{"type", `"Agent":`, `"Agent": {"area":"invalid"}, "Agent":`},
		{"group", `"properties":`, `"properties": {}, "properties":`},
		{"member", `"Name":`, `"Name": {}, "Name":`},
		{"escaped member", `"Name":`, `"Name": {}, "Na\u006de":`},
		{"go", `"go": "agent.Agent{}"`, `"go": null, "go": "agent.Agent{}"`},
		{"go symbols", `"go_symbols": ["agent.Agent"]`, `"go_symbols": null, "go_symbols": ["agent.Agent"]`},
		{"status", `"status": "adapted"`, `"status": "invalid", "status": "adapted"`},
		{"note", `"note": "Name accessor."`, `"note": "", "note": "Name accessor."`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(sampleCatalogJSON, test.old) {
				t.Fatal("test replacement does not match the catalog")
			}
			assertInvalidCatalog(t, strings.Replace(sampleCatalogJSON, test.old, test.new, 1), "duplicate JSON key")
		})
	}
}

func TestDuplicateSymbolIdentities(t *testing.T) {
	m := validMappingJSON
	p := validPropertyJSON
	for _, test := range []struct {
		name       string
		namespaces string
		want       string
	}{
		{"property and field", `{"Example":{"Agent":{"area":"agents","properties":{"Name":` + p + `},"fields":{"Name":` + m + `}}}}`, "duplicate .NET symbol"},
		{"property and constant", `{"Example":{"Agent":{"area":"agents","properties":{"Name":` + p + `},"constants":{"Name":` + m + `}}}}`, "duplicate .NET symbol"},
		{"constructor and method", `{"Example":{"Agent":{"area":"agents","constructors":{"Agent()":` + m + `},"methods":{"Agent()":` + m + `}}}}`, "duplicate .NET symbol"},
		{"whitespace in overload", `{"Example":{"Agent":{"area":"agents","methods":{"Run(string,int)":` + m + `,"Run( string, int )":` + m + `}}}}`, "duplicate .NET symbol"},
		{"whitespace in generic type", `{"Example":{"Pair<T,U>":{"area":"agents","methods":{"Get()":` + m + `}},"Pair<T, U>":{"area":"agents","methods":{"Get()":` + m + `}}}}`, "duplicate .NET type"},
		{"type and member", `{"Example":{"Agent":{"area":"agents","properties":{"Options":` + p + `}},"Agent.Options":{"area":"agents","mapping":` + m + `}}}`, "duplicate .NET symbol"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidCatalog(t, strings.Replace(sampleCatalogJSON, sampleNamespacesJSON, test.namespaces, 1), test.want)
		})
	}
}

func TestInvalidInput(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{"unknown root field", `{"schema_version":0,"unexpected":true}`, "unknown field"},
		{"legacy root types", `{"schema_version":0,"baseline":` + sampleBaselineJSON + `,"types":{}}`, `unknown field "types"`},
		{"empty namespace", strings.Replace(sampleCatalogJSON, sampleNamespacesJSON, `{"Example.Agents":{}}`, 1), "invalid or empty namespace"},
		{"trailing object", sampleCatalogJSON + `{}`, "single JSON object"},
		{"trailing garbage", sampleCatalogJSON + `invalid`, "single JSON object"},
		{"malformed JSON", `{`, ""},
		{"empty input", ``, "single JSON object"},
		{"array root", `[]`, "single JSON object"},
		{"null root", `null`, "single JSON object"},
		{"null type", strings.Replace(sampleCatalogJSON, sampleNamespacesJSON, `{"Example":{"Agent":null}}`, 1), "invalid area"},
		{"null member", strings.Replace(sampleCatalogJSON, sampleNamespacesJSON, `{"Example":{"Agent":{"area":"agents","methods":{"Run()":null}}}}`, 1), "invalid status"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidCatalog(t, test.data, test.want)
		})
	}
	var out, diagnostics bytes.Buffer
	if err := run([]string{"mappings", "-file", filepath.Join(t.TempDir(), "missing.json")}, &out, &diagnostics); err == nil || out.Len() != 0 {
		t.Fatalf("missing catalog must fail without output: %v, %q", err, out.String())
	}
}

func TestInvalidOptionsAndHelp(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"summary"},
		{"-view", "go"},
		{"mappings", "-area", "unknown"},
		{"mappings", "-area", "samples"},
		{"mappings", "-kind", "feature"},
		{"mappings", "-kind", "unknown"},
		{"mappings", "-status", "aligned"},
		{"mappings", "-status", "MAPPED"},
		{"mappings", "-coverage", "partial"},
		{"mappings", "-priority", "1"},
		{"mappings", "-state", "linked"},
		{"mappings", "-check"},
		{"mappings", "-inventory", "ignored.json"},
		{"mappings", "-assessed=invalid"},
		{"gaps", "-inventory", "ignored.json"},
		{"gaps", "-assessed=invalid"},
		{"gaps", "-summary"},
		{"mappings", "-limit", "-1"},
		{"mappings", "-offset", "-1"},
		{"go", "-limit", "-1"},
		{"reconcile", "-offset", "-1"},
		{"reconcile", "-inventory", "ignored.json"},
		{"reconcile", "-assessed=invalid"},
		{"mappings", "-summary", "-limit", "0"},
		{"go", "-summary", "-offset", "0"},
		{"reconcile", "-summary", "-limit", "10"},
		{"gaps", "-tags", "alternate"},
		{"mappings", "-go-root", "./other"},
		{"mappings", "-go-package", "./agent/..."},
		{"go", "-file", "ignored.json"},
		{"go", "-area", "agents"},
		{"go", "-inventory", "ignored.json"},
		{"go", "-assessed"},
		{"go", "-check"},
		{"go", "-go-package", " "},
		{"go-only", "-summary"},
		{"go-only", "-go-root", "."},
		{"go-only", "-type", "Agent"},
		{"go-only", "-inventory", "ignored.json"},
		{"go-only", "-assessed"},
		{"go-only", "-limit", "-1"},
		{"changes"},
		{"changes", "-old-root", " "},
		{"changes", "-old-root", ".", "-limit", "-1"},
		{"changes", "-old-root", ".", "-summary", "-limit", "0"},
		{"changes", "-old-root", ".", "-file", "ignored.json"},
		{"changes", "-old-root", ".", "-inventory", "ignored.json"},
		{"changes", "-old-root", ".", "-assessed"},
		{"changes", "-old-root", ".", "-check"},
		{"reconcile", "-state", "ready"},
		{"go-tests", "-file", "ignored.json"},
		{"go-tests", "-inventory", "ignored.json"},
		{"go-tests", "-assessed"},
		{"go-tests", "-check"},
		{"go-tests", "-go-package", "./..."},
		{"go-tests", "-tags", "alternate"},
		{"go-tests", "-status", "mapped"},
		{"go-tests", "-limit", "-1"},
		{"go-tests", "-offset", "-1"},
		{"go-tests", "-summary", "-limit", "0"},
		{"tests", "-area", "agents"},
		{"tests", "-kind", "method"},
		{"tests", "-inventory", "ignored.json"},
		{"tests", "-assessed"},
		{"tests", "-go-package", "./..."},
		{"tests", "-tags", "alternate"},
		{"tests", "-status", "mapped"},
		{"tests", "-status", "unreviewed"},
		{"tests", "-status", "MAPPED"},
		{"tests", "-state", "outside-inventory-scope"},
		{"tests", "-state", "go-outside-scope"},
		{"tests", "-state", "ready"},
		{"tests", "-limit", "-1"},
		{"tests", "-offset", "-1"},
		{"tests", "-summary", "-offset", "0"},
		{"tests", "-assembly"},
		{"tests", "unexpected"},
		{"mappings", "unexpected"},
		{"mappings", "--", "unexpected"},
		{"mappings", "-unknown"},
		{"mappings", "-json=invalid"},
		{"mappings", "-type"},
		{"mappings", "-symbol"},
		{"mappings", "-file"},
	} {
		name := strings.Join(args, " ")
		if name == "" {
			name = "missing subcommand"
		}
		t.Run(name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			if err := run(args, &out, &diagnostics); err == nil || out.Len() != 0 {
				t.Fatalf("options %v must fail without a report: %v, %q", args, err, out.String())
			}
		})
	}
	for _, command := range []struct {
		name    string
		present []string
		absent  []string
	}{
		{"mappings", []string{"-file", "-assessed", "-area", "-kind", "-status", "-type", "-symbol", "-json", "-summary", "-limit", "-offset"}, []string{"-go-root", "-inventory", "-check"}},
		{"gaps", []string{"-file", "-assessed", "-area", "-status", "-limit", "-offset"}, []string{"-go-root", "-inventory", "-summary"}},
		{"go-only", []string{"-file", "-symbol", "-json", "-limit", "-offset"}, []string{"-go-root", "-inventory", "-assessed", "-summary", "-type", "-status"}},
		{"go", []string{"-go-root", "-go-package", "-tags", "-summary", "-symbol", "-json", "-limit", "-offset"}, []string{"-file", "-area", "-inventory", "-assessed", "-check"}},
		{"changes", []string{"-old-root", "-go-root", "-go-package", "-tags", "-summary", "-symbol", "-json", "-limit", "-offset"}, []string{"-file", "-inventory", "-assessed", "-check"}},
		{"reconcile", []string{"-file", "-assessed", "-go-root", "-check", "-state", "-summary", "-limit", "-offset"}, []string{"-inventory"}},
		{"go-tests", []string{"-go-root", "-summary", "-symbol", "-json", "-limit", "-offset"}, []string{"-file", "-inventory", "-assessed", "-check", "-go-package", "-tags", "-area", "-kind", "-type", "-status"}},
		{"tests", []string{"-file", "-go-root", "-assembly", "-namespace", "-type", "-symbol", "-state", "-summary", "-check", "-json", "-limit", "-offset"}, []string{"-inventory", "-assessed", "-area", "-kind", "-go-package", "-tags", "-status"}},
	} {
		t.Run(command.name, func(t *testing.T) {
			var out, help bytes.Buffer
			err := run([]string{command.name, "-help", "-file", filepath.Join(t.TempDir(), "missing.json")}, &out, &help)
			if err != nil || out.Len() != 0 {
				t.Fatalf("help must not load inputs: %v, %q", err, out.String())
			}
			for _, text := range command.present {
				if !strings.Contains(help.String(), text) {
					t.Errorf("%s help is missing %q: %s", command.name, text, help.String())
				}
			}
			for _, text := range command.absent {
				if strings.Contains(help.String(), "\n  "+text+" ") || strings.Contains(help.String(), "\n  "+text+"\n") {
					t.Errorf("%s help unexpectedly includes %q: %s", command.name, text, help.String())
				}
			}
		})
	}
	var out, help bytes.Buffer
	if err := run([]string{"-help"}, &out, &help); err != nil || out.Len() != 0 {
		t.Fatalf("root help must not load inputs: %v, %q", err, out.String())
	}
	for _, name := range []string{"symbolmap", "mappings", "gaps", "go-only", "go", "changes", "reconcile", "go-tests", "tests"} {
		if !strings.Contains(help.String(), name) {
			t.Errorf("root help is missing %q: %s", name, help.String())
		}
	}
	if strings.Contains(help.String(), "|summary|") {
		t.Errorf("root help still lists a summary subcommand: %s", help.String())
	}
}

func commandOutput(t *testing.T, args ...string) string {
	t.Helper()
	var out, diagnostics bytes.Buffer
	if err := run(args, &out, &diagnostics); err != nil {
		t.Fatalf("run(%v): %v; diagnostics: %s", args, err, diagnostics.String())
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %s", diagnostics.String())
	}
	return out.String()
}

func mappingViewArgs(view string, args ...string) []string {
	if view == "summary" {
		return append([]string{"mappings", "-summary"}, args...)
	}
	return append([]string{view}, args...)
}

func decodeOutput(t *testing.T, data string, value any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("expected a single report, got %v", err)
	}
}

func readMappings(t *testing.T, args ...string) reportedMappings {
	t.Helper()
	return readMappingsView(t, "mappings", args...)
}

func readMappingsView(t *testing.T, command string, args ...string) reportedMappings {
	t.Helper()
	output := commandOutput(t, append([]string{command, "-json"}, args...)...)
	var got reportedMappings
	decodeOutput(t, output, &got)
	if got.Mappings == nil {
		t.Fatal("mappings must encode as an array, including [] for empty results")
	}
	var raw struct {
		Mappings []map[string]json.RawMessage `json:"mappings"`
	}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatal(err)
	}
	for i, row := range raw.Mappings {
		kind := strings.Trim(string(bytes.TrimSpace(row["kind"])), `"`)
		if value := bytes.TrimSpace(row["go"]); kind == "property" {
			if len(value) != 0 {
				t.Fatalf("property mapping %d must omit go", i)
			}
		} else if len(value) == 0 || value[0] != '"' {
			t.Fatalf("mapping %d must encode go as a non-null string", i)
		}
		if value := bytes.TrimSpace(row["go_symbols"]); len(value) == 0 || value[0] != '[' {
			t.Fatalf("mapping %d must encode go_symbols as a non-null array", i)
		}
	}
	return got
}

func assertBaseline(t *testing.T, got map[string]any) {
	t.Helper()
	var want map[string]any
	if err := json.Unmarshal([]byte(sampleBaselineJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("baseline = %v, want %v", got, want)
	}
}

func assertSelectedMappings(t *testing.T, got reportedMappings, symbols []string) {
	t.Helper()
	assertBaseline(t, got.Baseline)
	known := make(map[string]reportedMapping)
	for _, row := range expectedMappings() {
		known[row.Dotnet] = row
	}
	want := make([]reportedMapping, 0, len(symbols))
	for _, symbol := range symbols {
		row, ok := known[symbol]
		if !ok {
			t.Fatalf("unknown test symbol %q", symbol)
		}
		want = append(want, row)
	}
	if !reflect.DeepEqual(got.Mappings, want) {
		t.Fatalf("selected mappings = %#v, want %#v", got.Mappings, want)
	}
}

func assertInvalidCatalog(t *testing.T, data, want string) {
	t.Helper()
	file := writeCatalog(t, data)
	for _, view := range []string{"mappings", "summary", "gaps", "go-only", "reconcile", "tests"} {
		var out, diagnostics bytes.Buffer
		// No fixture symbols match this filter. Validation must still inspect
		// the entire catalog before any view applies its filters.
		err := run(mappingViewArgs(view, "-file", file, "-symbol", "not-present", "-json"), &out, &diagnostics)
		if err == nil || (want != "" && !strings.Contains(err.Error(), want)) {
			t.Fatalf("%s error = %v, want %q", view, err, want)
		}
		if out.Len() != 0 {
			t.Fatalf("invalid catalog produced a successful-looking report: %s", out.String())
		}
	}
}

func catalogWithGoOnly(data, entries string) string {
	return strings.Replace(data, `"namespaces":`, `"go_only":`+entries+`,"namespaces":`, 1)
}

func catalogWithInventory(t *testing.T, data, inventoryJSON string) string {
	t.Helper()
	c, err := symbolcatalog.Decode([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := symbolcatalog.DecodeInventory([]byte(inventoryJSON))
	if err != nil {
		t.Fatal(err)
	}
	c, err = symbolcatalog.Merge(c, inv)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := symbolcatalog.Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func writeGoCheckout(t *testing.T, module string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeCatalog(t *testing.T, data string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func expectedMappings() []reportedMapping {
	return []reportedMapping{
		{Area: "agents", Kind: "constant", Namespace: "Example.Agents", Dotnet: agentType + ".DefaultName", Go: "agent.DefaultName", GoSymbols: []string{"agent.DefaultName"}, Status: "mapped", Note: "Default name constant."},
		{Area: "agents", Kind: "constructor", Namespace: "Example.Agents", Dotnet: agentType + ".Agent(string)", Go: "agent.New(agent.ProviderConfig{}, agent.Config{})", GoSymbols: []string{"agent.New"}, Status: "adapted", Note: "Go constructor function."},
		{Area: "agents", Kind: "field", Namespace: "Example.Agents", Dotnet: agentType + ".Enabled", Go: "agent.Config{Enabled: true}", GoSymbols: []string{"agent.Config.Enabled"}, Status: "mapped", Note: "Configuration field."},
		{Area: "agents", Kind: "method", Namespace: "Example.Agents", Dotnet: agentType + "." + runList, Go: "a.Run(ctx, messages).Collect()", GoSymbols: []string{"agent.Agent.Run", "agent.ResponseStream.Collect"}, Status: "adapted", Note: "Collects the message overload."},
		{Area: "agents", Kind: "method", Namespace: "Example.Agents", Dotnet: agentType + "." + runString, Go: "a.RunText(ctx, \"hello\").Collect()", GoSymbols: []string{"agent.Agent.RunText", "agent.ResponseStream.Collect"}, Status: "adapted", Note: "Collects the string overload."},
		{Area: "agents", Kind: "method", Namespace: "Example.Agents", Dotnet: agentType + "." + serialize, Go: "session.MarshalJSON()", GoSymbols: []string{"agent.Session.MarshalJSON"}, Status: "mapped", Note: "Serialization belongs to Session."},
		{Area: "agents", Kind: "property", Namespace: "Example.Agents", Dotnet: agentType + ".ExtensionData", GoSymbols: []string{}, Status: "intentional", Note: "Provider-specific metadata is intentional."},
		{Area: "agents", Kind: "property", Namespace: "Example.Agents", Dotnet: agentType + ".Metadata", GoSymbols: []string{"agent.Session.Get"}, Status: "partial", Note: "Does not preserve all metadata."},
		{Area: "agents", Kind: "property", Namespace: "Example.Agents", Dotnet: agentType + ".Missing", GoSymbols: []string{}, Status: "unmapped", Note: "No counterpart inventoried."},
		{Area: "agents", Kind: "property", Namespace: "Example.Agents", Dotnet: agentType + ".Name", GoSymbols: []string{"agent.Agent.Name"}, Status: "mapped", Note: "Name accessor."},
		{Area: "agents", Kind: "property", Namespace: "Example.Agents", Dotnet: agentType + ".Options", GoSymbols: []string{"agent.WithSession"}, Status: "adapted", Note: "Session option factory."},
		{Area: "agents", Kind: "type", Namespace: "Example.Agents", Dotnet: agentType, Go: "agent.Agent{}", GoSymbols: []string{"agent.Agent"}, Status: "adapted", Note: "Configured concrete agent."},
		{Area: "agents", Kind: "method", Namespace: "Example.Agents", Dotnet: stateType + "." + getValue, Go: "(*agent.Session).Get", GoSymbols: []string{"agent.Session.Get"}, Status: "adapted", Note: "Writes into a destination pointer."},
		{Area: "workflows", Kind: "method", Namespace: "Example.Workflows", Dotnet: runnerType + "." + runString, Go: "(*inproc.ExecutionEnvironment).Run", GoSymbols: []string{"workflow/inproc.ExecutionEnvironment.Run"}, Status: "partial", Note: "Background execution is unavailable."},
		{Area: "workflows", Kind: "type", Namespace: "Example.Workflows", Dotnet: runnerType, Go: "inproc.ExecutionEnvironment{}", GoSymbols: []string{"workflow/inproc.ExecutionEnvironment"}, Status: "mapped", Note: "Execution runtime counterpart."},
	}
}

const (
	validTestMappingJSON           = `"agent.TestCombined"`
	sampleAPIInventoryJSON         = `{"schema_version":1,"identity_format":"ecma335-v1","assemblies":{"Core":{"informational_version":"1.0.0+1111111111111111111111111111111111111111","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},"types":{"Example.Placeholder":{"assembly":"Core","kind":"class"}}}`
	sampleTestAssemblyMetadataJSON = `"commit":"3333333333333333333333333333333333333333","target_framework":".NETCoreApp,Version=v10.0","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"`
	sampleTestNestedType           = "Outer`1+Inner`1"
	sampleTestGenericMethod        = "Generic"
	sampleTestAssembliesJSON       = `{
		"Core.Tests": {` + sampleTestAssemblyMetadataJSON + `,"types": {
			"Example.Tests.AgentTests": ["RunAsync", "RejectsNull", "MoreCases", "Pending", "NoGo", "PlatformSpecific"],
			"Example.Tests.` + sampleTestNestedType + `": ["` + sampleTestGenericMethod + `"],
			"GlobalTests": ["All"]
		}},
		"Other.Tests": {` + sampleTestAssemblyMetadataJSON + `,"types": {
			"Example.Tests.AgentTests": ["RunAsync"]
		}}
	}`
	sampleTestSectionJSON  = `{"identity_format":"test-name-v1","assemblies":` + sampleTestAssembliesJSON + `}`
	sampleTestMappingsJSON = `{
		"Core.Tests": {
			"Example.Tests.AgentTests": {
				"RunAsync": "internal/check.TestAlternate",
				"RejectsNull": ` + validTestMappingJSON + `
			},
			"Example.Tests.` + sampleTestNestedType + `": {
				"` + sampleTestGenericMethod + `": "examples/demo.TestRetry"
			},
			"GlobalTests": {"All": "..TestRoot"}
		}
	}`
)

func catalogWithTests(data, entries string) string {
	return strings.Replace(data, `"namespaces":`, `"tests":`+entries+`,"namespaces":`, 1)
}

func inventoryWithTests(section string) string {
	return strings.TrimSuffix(sampleAPIInventoryJSON, "}") + `,"tests":` + section + `}`
}

func singleTestMapping(entry string) string {
	return `{"Core.Tests":{"Example.Tests.AgentTests":{"RunAsync":` + entry + `}}}`
}

func testCommandArgs(t *testing.T, entries, inventory string) []string {
	t.Helper()
	root := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent.go": `package agent
import _ "example.invalid/uncached"
func init() { panic("source discovery must not execute initializers or load dependencies") }
`,
		"agent/agent_test.go": `package agent_test
import "testing"
func TestCombined(t *testing.T) { panic("source discovery must not run tests") }
`,
		"internal/check/check_test.go": `package check
import tt "testing"
func TestAlternate(t *tt.T) { panic("source discovery must not run tests") }
`,
		"examples/demo/demo_test.go": `package main
import . "testing"
func TestRetry(*T) { panic("source discovery must not run tests") }
`,
		"root_test.go": `package sdk
import "testing"
func TestRoot(*testing.T) { panic("source discovery must not run tests") }
`,
	})
	return []string{"tests", "-file", writeCatalog(t, catalogWithInventory(t, catalogWithTests(sampleCatalogJSON, entries), inventory)), "-go-root", root}
}

func TestGoTestsSourceDiscovery(t *testing.T) {
	// These settings would break or execute a wrapper in a toolchain-based
	// index. Static test discovery must not consult them or require dependencies.
	t.Setenv("GOTOOLCHAIN", "missing-test-toolchain")
	t.Setenv("GOPACKAGESDRIVER", "must-not-execute")
	t.Setenv("GOFLAGS", "-mod=mod -toolexec=must-not-execute -tags=unused")
	t.Setenv("GOCACHEPROG", "must-not-execute")
	root := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/entry_test.go": `package agent
import "testing"
type suite struct{}
type alias = testing.T
const sourceText = "func TestInString(t *testing.T) {}"
func init() { panic("must not execute") }
func Test(t *testing.T) { panic("must not execute") }
func TestAlpha(t *testing.T) { t.Run("case", func(t *testing.T) { panic("must not execute") }) }
func Test1(*testing.T) {}
func Test_Extra(_ *testing.T) {}
func TestÉlan(*testing.T) {}
func TestAssembly(*testing.T)
func Testlower(*testing.T) {}
func Testélan(*testing.T) {}
func TestMain(*testing.T) {}
func TestGeneric[P any](*testing.T) {}
func (suite) TestMethod(*testing.T) {}
func TestResults(*testing.T) bool { return true }
func TestNoArgument() {}
func TestTwo(t, other *testing.T) {}
func TestTwoFields(t *testing.T, other int) {}
func TestValue(testing.T) {}
func TestVariadic(...*testing.T) {}
func TestAliasType(*alias) {}
func TestBenchmarkArgument(*testing.B) {}
func TestMainArgument(*testing.M) {}
func TestFuzzArgument(*testing.F) {}
func Helper(*testing.T) {}
func BenchmarkValue(*testing.B) {}
func ExampleValue() {}
func FuzzValue(*testing.F) {}
`,
		"agent/source.go":                "package agent\nimport \"testing\"\nfunc TestOrdinarySource(*testing.T) {}\n",
		"agent/cases_windows_test.go":    "//go:build windows\n\npackage agent\nimport \"testing\"\nfunc TestCombined(*testing.T) {}\n",
		"agent/cases_linux_test.go":      "//go:build linux\n\npackage agent\nimport \"testing\"\nfunc TestCombined(*testing.T) {}\n",
		"agent/cases_plan9_test.go":      "//go:build plan9 && unavailable_tag\n\npackage agent\nimport \"testing\"\nfunc TestPlan9(*testing.T) {}\n",
		"agent/external_test.go":         "package agent_test\nimport \"testing\"\nfunc TestCombined(*testing.T) {}\nfunc TestExternal(*testing.T) {}\n",
		"internal/check/check_test.go":   "package check\nimport tt \"testing\"\nfunc TestAlias(*tt.T) {}\n",
		"cmd/tool/main_test.go":          "package main\nimport . \"testing\"\nfunc TestDot(*T) {}\n",
		"examples/demo/main_test.go":     "package main\nimport \"testing\"\nfunc TestExampleProject(*testing.T) {}\n",
		"harness/main_test.go":           "package harness\nimport \"testing\"\nfunc TestMain(*testing.M) {}\n",
		"root_test.go":                   "package sdk\nimport \"testing\"\nfunc TestRoot(*testing.T) {}\n",
		"sdk/root_test.go":               "package sdk\nimport \"testing\"\nfunc TestRoot(*testing.T) {}\n",
		"fake/fake_test.go":              "package fake\nimport testing \"example.invalid/testing\"\nfunc TestFakeImport(*testing.T) {}\n",
		"fake/noimport_test.go":          "package fake\ntype T struct{}\nfunc TestNoImport(*T) {}\n",
		"fake/blank_test.go":             "package fake\nimport _ \"testing\"\nfunc TestBlankImport(*testing.T) {}\n",
		"fake/otherfile_test.go":         "package fake\nfunc TestOtherFileImport(*testing.T) {}\n",
		"testdata/invalid_test.go":       "not Go source",
		"agent/testdata/invalid_test.go": "not Go source",
		"vendor/invalid_test.go":         "not Go source",
		".hidden/invalid_test.go":        "not Go source",
		"_ignored/invalid_test.go":       "not Go source",
		"agent/.hidden_test.go":          "not Go source",
		"agent/_ignored_test.go":         "not Go source",
		"nested/go.mod":                  "not a valid module file",
		"nested/invalid_test.go":         "not Go source",
	})
	// No catalog or .NET inventory exists in this working directory.
	t.Chdir(t.TempDir())
	output := commandOutput(t, "go-tests", "-go-root", root, "-limit=0")
	var got goTestInventory
	decodeOutput(t, output, &got)
	want := []string{
		"..TestRoot", "agent.Test", "agent.Test1", "agent.TestAlpha", "agent.TestAssembly",
		"agent.TestCombined", "agent.TestExternal", "agent.TestMain", "agent.TestPlan9", "agent.Test_Extra", "agent.TestÉlan",
		"cmd/tool.TestDot", "examples/demo.TestExampleProject", "internal/check.TestAlias", "sdk.TestRoot",
	}
	if !reflect.DeepEqual(got.Tests, want) {
		t.Fatalf("source test entrypoints = %v, want %v", got.Tests, want)
	}
	if got.Module != "example.org/sdk" || got.TestFunctions != len(want) || got.Page.Total != len(want) || got.Page.Returned != len(want) || got.Scope == "" {
		t.Fatalf("missing source provenance or incorrect declaration count: %+v", got)
	}
	if strings.Contains(output, "_test.go\"") || strings.Contains(output, `"line"`) || strings.Contains(output, `"goos"`) || strings.Contains(output, `"signature"`) {
		t.Fatalf("source discovery recorded redundant signatures, locations, or a misleading build configuration: %s", output)
	}
	if again := commandOutput(t, "go-tests", "-go-root", root, "-limit=0"); again != output {
		t.Fatal("source discovery depends on map iteration or build-variant order")
	}
	text := commandOutput(t, "go-tests", "-go-root", root, "-symbol", "TestCombined", "-json=false")
	for _, want := range []string{"at least one source declaration", "not buildability", "no compilation or execution", "agent.TestCombined"} {
		if !strings.Contains(text, want) {
			t.Errorf("source discovery text omitted %q: %s", want, text)
		}
	}
}

func TestGoTestsPages(t *testing.T) {
	var source strings.Builder
	source.WriteString("package sample\nimport \"testing\"\n")
	for i := range 26 {
		fmt.Fprintf(&source, "func Test%02d(*testing.T) {}\n", i)
	}
	root := writeGoCheckout(t, "example.org/sdk", map[string]string{"sample/all_test.go": source.String()})
	t.Chdir(root)
	var first goTestInventory
	decodeOutput(t, commandOutput(t, "go-tests", "-go-root", "."), &first)
	if first.Page == nil || first.Page.Total != 26 || first.Page.Limit != defaultPageLimit || first.Page.Returned != defaultPageLimit || first.Page.NextOffset == nil || *first.Page.NextOffset != 20 || first.TestFunctions != 26 {
		t.Fatalf("default test page lost counts: %+v", first)
	}
	var second goTestInventory
	decodeOutput(t, commandOutput(t, "go-tests", "-go-root", ".", "-offset=20"), &second)
	if second.Page.Total != 26 || second.Page.Returned != 6 || second.Page.NextOffset != nil || second.TestFunctions != 26 {
		t.Fatalf("last test page lost counts: %+v", second)
	}
	var filtered goTestInventory
	decodeOutput(t, commandOutput(t, "go-tests", "-go-root", ".", "-symbol", "SAMPLE.TEST0", "-limit=2", "-offset=3"), &filtered)
	if filtered.Page.Total != 10 || filtered.TestFunctions != 10 || filtered.Page.Returned != 2 || filtered.Page.NextOffset == nil || *filtered.Page.NextOffset != 5 || !reflect.DeepEqual(filtered.Tests, []string{"sample.Test03", "sample.Test04"}) {
		t.Fatalf("filtering must precede paging: %+v", filtered)
	}
	var summary goTestMetadata
	text := commandOutput(t, "go-tests", "-go-root", ".", "-symbol", "sample.Test0", "-summary")
	decodeOutput(t, text, &summary)
	if summary.TestFunctions != 10 || summary.Module != first.Module || summary.Scope != first.Scope || strings.Contains(text, `"tests"`) || strings.Contains(text, `"page"`) {
		t.Fatalf("summary must count every selected test without rows or paging: %s", text)
	}
	for _, args := range [][]string{{"-offset=100"}, {"-symbol", "not-present"}} {
		var empty goTestInventory
		decodeOutput(t, commandOutput(t, append([]string{"go-tests", "-go-root", "."}, args...)...), &empty)
		if empty.Tests == nil || len(empty.Tests) != 0 || empty.Page.NextOffset != nil {
			t.Fatalf("empty page must contain an array and no next offset: %+v", empty)
		}
	}
}

func TestGoTestsInputErrors(t *testing.T) {
	broken := writeGoCheckout(t, "example.org/sdk", map[string]string{
		"valid_test.go":         "package sdk\nimport \"testing\"\nfunc TestValid(*testing.T) {}\n",
		"invalid_plan9_test.go": "//go:build plan9\n\npackage sdk\nfunc Broken(\n",
	})
	malformedModule := writeGoCheckout(t, "example.org/sdk", map[string]string{"go.mod": "module (\n"})
	noModuleDirective := writeGoCheckout(t, "example.org/sdk", map[string]string{"go.mod": "go 1.26.0\n"})
	for _, test := range []struct{ name, root, want string }{
		{"syntax outside current build", broken, "parse test source"},
		{"module parse", malformedModule, "parse Go test module"},
		{"missing module directive", noModuleDirective, "module directive"},
		{"missing module file", t.TempDir(), "read Go test module"},
		{"root is file", filepath.Join(broken, "go.mod"), "not a directory"},
		{"missing root", filepath.Join(t.TempDir(), "missing"), "index Go tests root"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			err := run([]string{"go-tests", "-go-root", test.root, "-symbol", "not-present", "-limit=1"}, &out, &diagnostics)
			if err == nil || !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
				t.Fatalf("input error must not produce partial discovery: %v, %q", err, out.String())
			}
		})
	}
	root := writeGoCheckout(t, "example.org/sdk", nil)
	var empty goTestInventory
	decodeOutput(t, commandOutput(t, "go-tests", "-go-root", root), &empty)
	if empty.Tests == nil || empty.TestFunctions != 0 || empty.Page.Total != 0 {
		t.Fatalf("a module without test source is a valid empty Go index: %+v", empty)
	}
}

func TestInvalidTestMappings(t *testing.T) {
	for _, test := range []struct{ name, entry, want string }{
		{"empty object", `{}`, "cannot unmarshal object"},
		{"target object", `{"go_test":"agent.TestCombined"}`, "cannot unmarshal object"},
		{"old assessment", `{"go_tests":["agent.TestCombined"],"status":"mapped","note":"Reviewed assertion.","review":"tests-reviewed"}`, "cannot unmarshal object"},
		{"note metadata", `{"go_test":"agent.TestCombined","note":"Reviewed assertion."}`, "cannot unmarshal object"},
		{"review metadata", `{"go_test":"agent.TestCombined","review":"baseline"}`, "cannot unmarshal object"},
		{"status metadata", `{"go_test":"agent.TestCombined","status":"mapped"}`, "cannot unmarshal object"},
		{"unmapped placeholder", `{"go_tests":[],"status":"unmapped","note":"No counterpart."}`, "cannot unmarshal object"},
		{"intentional placeholder", `{"go_tests":[],"status":"intentional","note":"Not applicable."}`, "cannot unmarshal object"},
		{"API example", `{"go":"agent.TestCombined(t)"}`, "cannot unmarshal object"},
		{"API targets", `{"go_symbols":["agent.TestCombined"]}`, "cannot unmarshal object"},
		{"empty array", `[]`, "cannot unmarshal array"},
		{"single target array", `["agent.TestCombined"]`, "cannot unmarshal array"},
		{"multiple target array", `["agent.TestCombined","internal/check.TestAlternate"]`, "cannot unmarshal array"},
		{"boolean leaf", `true`, "cannot unmarshal bool"},
		{"number leaf", `1`, "cannot unmarshal number"},
		{"duplicate legacy leaf field", `{"status":"mapped","status":"adapted"}`, "duplicate JSON key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidCatalog(t, catalogWithTests(sampleCatalogJSON, singleTestMapping(test.entry)), test.want)
		})
	}
	for _, target := range []string{
		"", " ", "TestCombined", "agent.Testlower", "agent.Helper", "agent.BenchmarkRun", "agent.ExampleRun", "agent.FuzzRun",
		"agent.TestCombined/case", "agent.TestCombined()", "agent.TestCombined:12", "/agent.TestCombined", "../agent.TestCombined",
		"agent/../agent.TestCombined", `agent\nested.TestCombined`, "agent//nested.TestCombined", ".TestCombined", " agent.TestCombined", "agent.TestCombined ",
	} {
		t.Run("target "+target, func(t *testing.T) {
			encoded, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			assertInvalidCatalog(t, catalogWithTests(sampleCatalogJSON, singleTestMapping(string(encoded))), "invalid qualified Go test function")
		})
	}
	// Reverse the JSON key order so the diagnostic must use sorted assembly,
	// type, and method identities rather than map iteration or input order.
	for _, test := range []struct{ name, entries, previous, duplicate string }{
		{
			"same declaring type",
			`{"Core.Tests":{"Example.Tests.AgentTests":{"RunAsync":"agent.TestCombined","RejectsNull":"agent.TestCombined"}}}`,
			"Core.Tests Example.Tests.AgentTests.RejectsNull", "Core.Tests Example.Tests.AgentTests.RunAsync",
		},
		{
			"different declaring types",
			`{"Core.Tests":{"Example.Tests.ZTests":{"RunAsync":"agent.TestCombined"},"Example.Tests.AgentTests":{"RunAsync":"agent.TestCombined"}}}`,
			"Core.Tests Example.Tests.AgentTests.RunAsync", "Core.Tests Example.Tests.ZTests.RunAsync",
		},
		{
			"different assemblies",
			`{"Other.Tests":{"Example.Tests.AgentTests":{"RunAsync":"agent.TestCombined"}},"Core.Tests":{"Example.Tests.AgentTests":{"RunAsync":"agent.TestCombined"}}}`,
			"Core.Tests Example.Tests.AgentTests.RunAsync", "Other.Tests Example.Tests.AgentTests.RunAsync",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := catalogWithTests(sampleCatalogJSON, test.entries)
			want := fmt.Sprintf("tests %s: Go test %q is already mapped to %s; test mappings must be one-to-one", test.duplicate, "agent.TestCombined", test.previous)
			assertInvalidCatalog(t, data, want)
			file := writeCatalog(t, data)
			for _, command := range []string{"go-only", "reconcile", "tests"} {
				var out, diagnostics bytes.Buffer
				err := run([]string{command, "-file", file, "-symbol", "not-present"}, &out, &diagnostics)
				if err == nil || !strings.Contains(err.Error(), want) || out.Len() != 0 {
					t.Fatalf("%s allowed a reused Go test or changed its diagnostic: %v, %q", command, err, out.String())
				}
			}
		})
	}
}

func TestInvalidTestMappingIdentities(t *testing.T) {
	valid := singleTestMapping(validTestMappingJSON)
	for _, test := range []struct{ name, entries, want string }{
		{"null section", `null`, "tests must be a non-null object"},
		{"array section", `[]`, "cannot unmarshal array"},
		{"null assembly", `{"Core.Tests":null}`, "invalid or empty assembly"},
		{"empty assembly", `{"Core.Tests":{}}`, "invalid or empty assembly"},
		{"invalid assembly", strings.Replace(valid, `Core.Tests`, `../Core.Tests`, 1), "invalid or empty assembly"},
		{"invalid namespace in type", strings.Replace(valid, `Example.Tests`, `Example..Tests`, 1), "invalid or empty declaring type"},
		{"empty type", `{"Core.Tests":{"Example.Tests.AgentTests":{}}}`, "invalid or empty declaring type"},
		{"null type", `{"Core.Tests":{"Example.Tests.AgentTests":null}}`, "invalid or empty declaring type"},
		{"invalid type", strings.Replace(valid, `AgentTests`, `AgentTests<T>>`, 1), "invalid or empty declaring type"},
		{"empty name", strings.Replace(valid, `RunAsync`, ``, 1), "invalid test name"},
		{"parameter list", strings.Replace(valid, `RunAsync`, `RunAsync()`, 1), "invalid test name"},
		{"full signature", strings.Replace(valid, `RunAsync`, `RunAsync(System.Boolean) -> System.Void`, 1), "invalid test name"},
		{"whitespace", strings.Replace(valid, `RunAsync`, `Run Async`, 1), "invalid test name"},
		{"qualified method", strings.Replace(valid, `RunAsync`, `AgentTests.RunAsync`, 1), "invalid test name"},
		{"duplicate method", strings.Replace(valid, `"RunAsync":`, `"RunAsync":`+validTestMappingJSON+`,"RunAsync":`, 1), "duplicate JSON key"},
		{"escaped duplicate method", strings.Replace(valid, `"RunAsync":`, `"Run\u0041sync":`+validTestMappingJSON+`,"RunAsync":`, 1), "duplicate JSON key"},
		{"duplicate type", `{"Core.Tests":{"Example.Tests.AgentTests":{"RunAsync":` + validTestMappingJSON + `},"Example.Tests.AgentTests":{"RunAsync":` + validTestMappingJSON + `}}}`, "duplicate JSON key"},
		{"duplicate assembly", `{"Core.Tests":{"Example.Tests.AgentTests":{"RunAsync":` + validTestMappingJSON + `}},"Core.Tests":{"Example.Tests.AgentTests":{"RunAsync":` + validTestMappingJSON + `}}}`, "duplicate JSON key"},
		{"old namespace hierarchy", `{"Core.Tests":{"Example.Tests":{"AgentTests":{"RunAsync":` + validTestMappingJSON + `}}}}`, "cannot unmarshal object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := catalogWithTests(sampleCatalogJSON, test.entries)
			assertInvalidCatalog(t, data, test.want)
			file := writeCatalog(t, data)
			// Every catalog-loading command validates test mappings even when
			// the requested view is API-only and all its rows are hidden.
			for _, command := range []string{"go-only", "reconcile", "tests"} {
				var out, diagnostics bytes.Buffer
				err := run([]string{command, "-file", file, "-symbol", "not-present"}, &out, &diagnostics)
				if err == nil || !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
					t.Fatalf("%s ignored an invalid tests section: %v, %q", command, err, out.String())
				}
			}
		})
	}
}

func TestTestInventoryValidation(t *testing.T) {
	assemblies := func(value string) string {
		return strings.Replace(sampleTestSectionJSON, sampleTestAssembliesJSON, value, 1)
	}
	types := func(value string) string {
		return assemblies(`{"Core.Tests":{` + sampleTestAssemblyMetadataJSON + `,"types":` + value + `}}`)
	}
	for _, test := range []struct{ name, section, want string }{
		{"null section", `null`, "non-null object"},
		{"array section", `[]`, "cannot unmarshal array"},
		{"scalar section", `true`, "cannot unmarshal bool"},
		{"empty section", `{}`, "identity_format"},
		{"identity", strings.Replace(sampleTestSectionJSON, "test-name-v1", "ecma335-v1", 1), "identity_format"},
		{"unknown section field", strings.Replace(sampleTestSectionJSON, `"identity_format":`, `"unknown":true,"identity_format":`, 1), "unknown field"},
		{"duplicate section field", strings.Replace(sampleTestSectionJSON, `"identity_format":`, `"identity_format":"test-name-v1","identity_format":`, 1), "duplicate JSON key"},
		{"unversioned commit", strings.Replace(sampleTestSectionJSON, strings.Repeat("3", 40), "main", 1), "full source revision"},
		{"nonhex commit", strings.Replace(sampleTestSectionJSON, strings.Repeat("3", 40), strings.Repeat("g", 40), 1), "full source revision"},
		{"empty hash", strings.Replace(sampleTestSectionJSON, strings.Repeat("b", 64), "", 1), "assembly SHA256"},
		{"invalid hash", strings.Replace(sampleTestSectionJSON, strings.Repeat("b", 64), strings.Repeat("g", 64), 1), "assembly SHA256"},
		{"empty assemblies", assemblies(`{}`), "assemblies must not be empty"},
		{"null assemblies", assemblies(`null`), "assemblies must not be empty"},
		{"empty assembly", assemblies(`{"Core.Tests":{}}`), "invalid or empty assembly"},
		{"null assembly", assemblies(`{"Core.Tests":null}`), "invalid or empty assembly"},
		{"invalid assembly", strings.Replace(sampleTestSectionJSON, `"Core.Tests"`, `"../Core.Tests"`, 1), "invalid or empty assembly"},
		{"invalid type", strings.Replace(sampleTestSectionJSON, `"Example.Tests.AgentTests"`, `"Example..AgentTests"`, 1), "invalid or empty metadata type"},
		{"empty type", types(`{"Example.Tests.AgentTests":[]}`), "invalid or empty metadata type"},
		{"null type", types(`{"Example.Tests.AgentTests":null}`), "invalid or empty metadata type"},
		{"duplicate type", types(`{"Example.Tests.AgentTests":["RunAsync"],"Example.Tests.AgentTests":["Pending"]}`), "duplicate JSON key"},
		{"signature instead of name", strings.Replace(sampleTestSectionJSON, `"Pending"`, `"Pending() -> System.Void"`, 1), "invalid test name"},
		{"empty name", strings.Replace(sampleTestSectionJSON, `"Pending"`, `""`, 1), "invalid test name"},
		{"null name", strings.Replace(sampleTestSectionJSON, `"Pending"`, `null`, 1), "invalid test name"},
		{"old method objects", types(`{"Example.Tests.AgentTests":{"Pending() -> System.Void":{"attribute":"Xunit.FactAttribute"}}}`), "cannot unmarshal object"},
		{"version not in schema", strings.Replace(sampleTestSectionJSON, `"target_framework":`, `"version":"1.0.0.0","target_framework":`, 1), "unknown field"},
		{"informational version not in schema", strings.Replace(sampleTestSectionJSON, `"target_framework":`, `"informational_version":"1.0.0","target_framework":`, 1), "unknown field"},
		{"location not in schema", strings.Replace(sampleTestSectionJSON, `"target_framework":`, `"path":"old.dll","target_framework":`, 1), "unknown field"},
		{"duplicate name", strings.Replace(sampleTestSectionJSON, `"Pending"`, `"Pending","Pending"`, 1), "duplicate test name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := symbolcatalog.DecodeInventory([]byte(inventoryWithTests(test.section))); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("malformed test inventory error = %v, want %q", err, test.want)
			}
		})
	}
	// Unknown optional API metadata remains compatible.
	compatible := strings.Replace(inventoryWithTests(sampleTestSectionJSON), `"kind":"class"`, `"kind":"class","future_optional_metadata":true`, 1)
	inv, err := symbolcatalog.DecodeInventory([]byte(compatible))
	if err != nil || inv.Tests == nil || inv.Tests.IdentityFormat != "test-name-v1" || len(inv.Types) != 1 {
		t.Fatalf("compatible API metadata or valid tests rejected: %+v, %v", inv, err)
	}
	data := catalogWithInventory(t, catalogWithTests(sampleCatalogJSON, sampleTestMappingsJSON), compatible)
	for _, test := range []struct{ name, old, replacement, want string }{
		{"unified API metadata", `"dotnet": {`, `"dotnet": {"future_optional_metadata":true,`, "unknown field"},
		{"unified test metadata", `"identity_format": "test-name-v1"`, `"identity_format": "test-name-v1", "unknown":true`, "unknown field"},
		{"unified test identity", `"identity_format": "test-name-v1"`, `"identity_format": "ecma335-v1"`, "dotnet.tests"},
		{"unified test commit", strings.Repeat("3", 40), "main", "full source revision"},
		{"unified test hash", strings.Repeat("b", 64), strings.Repeat("g", 64), "assembly SHA256"},
		{"unified API hash", `"sha256": "` + strings.Repeat("a", 64) + `",`, `"sha256": "",`, "API assembly SHA256"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(data, test.old) {
				t.Fatal("test replacement does not match the catalog")
			}
			file := writeCatalog(t, strings.Replace(data, test.old, test.replacement, 1))
			for _, command := range []string{"reconcile", "tests"} {
				var out, diagnostics bytes.Buffer
				err := run([]string{command, "-file", file, "-go-root", filepath.Join(t.TempDir(), "not-needed"), "-symbol", "not-present", "-summary"}, &out, &diagnostics)
				if err == nil || !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
					t.Fatalf("%s ignored malformed unified test metadata or emitted a partial report: %v, %q", command, err, out.String())
				}
			}
		})
	}
	for _, missing := range []string{"test metadata section", "test assembly metadata"} {
		t.Run(missing, func(t *testing.T) {
			c, err := symbolcatalog.Decode([]byte(data))
			if err != nil {
				t.Fatal(err)
			}
			if missing == "test metadata section" {
				c.Dotnet.Tests = nil
			} else {
				delete(c.Dotnet.Tests.Assemblies, "Core.Tests")
			}
			encoded, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			file := writeCatalog(t, string(encoded))
			for _, command := range []string{"reconcile", "tests"} {
				var out, diagnostics bytes.Buffer
				err := run([]string{command, "-file", file, "-go-root", filepath.Join(t.TempDir(), "not-needed"), "-symbol", "not-present", "-summary", "-check"}, &out, &diagnostics)
				if err == nil || !strings.Contains(err.Error(), "dotnet.tests") || out.Len() != 0 {
					t.Fatalf("%s failed to reject missing test provenance before indexing: %v, stdout %q", command, err, out.String())
				}
			}
		})
	}
	// Unavailable source revisions remain unknown, while a full SHA256 revision
	// is accepted alongside the usual SHA1 revision.
	for _, commit := range []string{"", strings.Repeat("a", 64)} {
		field := `"commit":"` + commit + `",`
		if commit == "" {
			field = ""
		}
		section := strings.ReplaceAll(sampleTestSectionJSON, `"commit":"3333333333333333333333333333333333333333",`, field)
		got, err := symbolcatalog.DecodeInventory([]byte(inventoryWithTests(section)))
		if err != nil || got.Tests == nil || got.Tests.Assemblies["Core.Tests"].Commit != commit {
			t.Fatalf("test commit %q rejected or inferred: %+v, %v", commit, got.Tests, err)
		}
	}
}

func TestTestMappingsPreserveAPIReports(t *testing.T) {
	absentData := sampleCatalogJSON
	emptyData := catalogWithTests(absentData, `{}`)
	fullData := catalogWithTests(absentData, sampleTestMappingsJSON)
	absent, empty, full := writeCatalog(t, absentData), writeCatalog(t, emptyData), writeCatalog(t, fullData)
	unpaired := writeCatalog(t, catalogWithTests(absentData, singleTestMapping(`null`)))
	for _, args := range [][]string{{"mappings", "-limit=0"}, {"mappings", "-summary"}, {"gaps", "-limit=0"}, {"go-only", "-limit=0"}} {
		before := commandOutput(t, append(slices.Clone(args), "-file", absent)...)
		for _, file := range []string{empty, full, unpaired} {
			after := commandOutput(t, append(slices.Clone(args), "-file", file)...)
			if after != before {
				t.Fatalf("%v changed API output because of optional test pairs", args)
			}
			var sections map[string]json.RawMessage
			if err := json.Unmarshal([]byte(after), &sections); err != nil {
				t.Fatal(err)
			}
			if sections["tests"] != nil || sections["go_tests"] != nil || sections["reviews"] != nil || strings.Contains(after, `"review":`) {
				t.Fatalf("%v injected test or obsolete review metadata into the API report: %s", args, after)
			}
		}
	}
	var summary reportedSummary
	decodeOutput(t, commandOutput(t, "mappings", "-file", full, "-summary"), &summary)
	assertBaseline(t, summary.Baseline)
	if summary.DotnetSymbols != 15 || summary.GoSymbols != 13 || summary.GoOnlySymbols != 0 {
		t.Fatalf("test mappings changed API counts: %+v", summary)
	}
	mappings := readMappings(t, "-file", full, "-limit=0")
	assertBaseline(t, mappings.Baseline)
	if !reflect.DeepEqual(mappings.Mappings, expectedMappings()) {
		t.Fatalf("test pairs changed API notes, statuses, targets, or examples: %+v", mappings)
	}
	before, err := symbolcatalog.DecodeInventory([]byte(sampleAPIInventoryJSON))
	if err != nil || before.Tests != nil {
		t.Fatalf("the tests section must remain optional for API reconciliation: %v", err)
	}
	after, err := symbolcatalog.DecodeInventory([]byte(inventoryWithTests(sampleTestSectionJSON)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadCatalog(full)
	if err != nil {
		t.Fatal(err)
	}
	report := mappingsReport{Baseline: c.Baseline, Mappings: flattenMappings(c)}
	api := goInventory{Module: c.Baseline.GoModule, Symbols: map[string]goSymbol{}, Packages: []string{}}
	original := reconcile(report, before, api, true)
	extended := reconcile(report, after, api, true)
	if extended.InventoryDeclarations != 1 || extended.AssessedDeclarations != original.AssessedDeclarations || !reflect.DeepEqual(extended.Counts, original.Counts) || !reflect.DeepEqual(extended.Rows, original.Rows) {
		t.Fatal("optional test assembly declarations changed API reconciliation")
	}
	if !reflect.DeepEqual(original.Baseline, c.Baseline) || !reflect.DeepEqual(extended.Baseline, c.Baseline) {
		t.Fatal("optional test assembly declarations changed the original API baseline")
	}
}

func TestTestReconciliationOneToOne(t *testing.T) {
	args := testCommandArgs(t, sampleTestMappingsJSON, inventoryWithTests(sampleTestSectionJSON))
	output := commandOutput(t, append(slices.Clone(args), "-limit=0", "-check")...)
	var got testReconciliationReport
	decodeOutput(t, output, &got)
	if got.InventoryTests != 9 || got.MappedTests != 4 || got.Go.TestFunctions != 4 || len(got.Rows) != 9 {
		t.Fatalf("one-to-one test totals are incorrect: %+v", got.testReconciliationSummary)
	}
	if !reflect.DeepEqual(got.Counts, map[string]int{"linked": 4, "unreviewed": 5, "needs-reconciliation": 0, "invalid-go-target": 0}) {
		t.Fatalf("test pair states are incorrect: %v", got.Counts)
	}
	wantByAssembly := map[string]map[string]int{
		"Core.Tests":  {"linked": 4, "unreviewed": 4, "needs-reconciliation": 0, "invalid-go-target": 0},
		"Other.Tests": {"linked": 0, "unreviewed": 1, "needs-reconciliation": 0, "invalid-go-target": 0},
	}
	if !reflect.DeepEqual(got.ByAssembly, wantByAssembly) {
		t.Fatalf("test pair counts by assembly = %v, want %v", got.ByAssembly, wantByAssembly)
	}
	info := got.Inventory.Assemblies["Core.Tests"]
	if len(got.Inventory.Assemblies) != 2 || info.Commit != "3333333333333333333333333333333333333333" || info.TargetFramework != ".NETCoreApp,Version=v10.0" || len(info.SHA256) != 64 || got.Inventory.IdentityFormat != "test-name-v1" || len(got.Inventory.SHA256) != 64 {
		t.Fatalf("test assembly provenance was replaced by API package provenance: %+v", got.Inventory)
	}
	wantPairs := map[string]string{
		"Core.Tests/Example.Tests.AgentTests.RejectsNull":  "agent.TestCombined",
		"Core.Tests/Example.Tests.AgentTests.RunAsync":     "internal/check.TestAlternate",
		"Core.Tests/Example.Tests.Outer`1+Inner`1.Generic": "examples/demo.TestRetry",
		"Core.Tests/GlobalTests.All":                       "..TestRoot",
	}
	pairs := make(map[string]string)
	seen := make(map[string]bool)
	for _, row := range got.Rows {
		identity := row.Assembly + "/" + row.Dotnet
		if seen[identity] || row.Dotnet != row.Type+"."+row.Method {
			t.Fatalf("test declarations must appear exactly once with exact metadata identity: %+v", row)
		}
		seen[identity] = true
		if row.GoTest == "" {
			if row.State != "unreviewed" || row.Reason != "" || row.InvalidGoTest != "" {
				t.Fatalf("an unpaired declaration must remain unreviewed: %+v", row)
			}
		} else {
			if row.State != "linked" || row.Reason != "" || row.InvalidGoTest != "" {
				t.Fatalf("a valid pair must be linked: %+v", row)
			}
			pairs[identity] = row.GoTest
		}
		if row.Assembly == "Other.Tests" && row.State != "unreviewed" {
			t.Fatalf("same metadata names in another assembly inherited a mapping: %+v", row)
		}
	}
	if !reflect.DeepEqual(pairs, wantPairs) {
		t.Fatalf("test pairs = %v, want %v", pairs, wantPairs)
	}
	var raw struct {
		MappedTests int                          `json:"mapped_tests"`
		Rows        []map[string]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.MappedTests != 4 {
		t.Fatalf("the report must expose the pair count as mapped_tests: %s", output)
	}
	for _, obsolete := range []string{"reviews", "go_tests", "review"} {
		if strings.Contains(output, `"`+obsolete+`":`) {
			t.Fatalf("test report retained obsolete %q metadata: %s", obsolete, output)
		}
	}
	for _, row := range raw.Rows {
		if row["go"] != nil || row["go_tests"] != nil || row["invalid_go_tests"] != nil {
			t.Fatalf("test pairs must not contain API examples or target arrays: %v", row)
		}
		if row["attribute"] != nil || row["signature"] != nil || bytes.Contains(row["method"], []byte("(")) {
			t.Fatalf("test report retained redundant method metadata: %v", row)
		}
		if string(row["state"]) == `"unreviewed"` {
			if row["go_test"] != nil || row["suggested_go"] != nil {
				t.Fatalf("unreviewed rows must omit target placeholders and guesses: %v", row)
			}
		} else if row["go_test"] == nil {
			t.Fatalf("linked rows must expose a singular go_test target: %v", row)
		}
	}
	if again := commandOutput(t, append(slices.Clone(args), "-limit=0", "-check")...); again != output {
		t.Fatal("test report is not deterministic")
	}
	for _, brief := range []bool{false, true} {
		invocation := append(slices.Clone(args), "-json=false", "-check")
		if brief {
			invocation = append(invocation, "-summary")
		}
		text := commandOutput(t, invocation...)
		for _, want := range []string{
			"Assembly Core.Tests", "Inventory tests: 9 (unfiltered); selected test pairs: 4.",
			"not theory data rows", "No tests were run", "not coverage percentages",
			"Linked validates one-to-one references, not behavioral parity. Unreviewed does not imply a missing test.",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("test text report omitted %q: %s", want, text)
			}
		}
		if !brief && !strings.Contains(strings.Join(strings.Fields(text), " "), "ASSEMBLY .NET TEST GO TEST STATE REASON") {
			t.Errorf("test text report omitted the one-to-one row columns: %s", text)
		}
		for _, removed := range []string{"STATUS", "NOTE", "REVIEW", "GO TESTS", "Go test review queue", "Missing queued Go tests"} {
			if strings.Contains(text, removed) {
				t.Errorf("test text report retained %q metadata: %s", removed, text)
			}
		}
	}
}

func TestTestReconciliationNullLeaves(t *testing.T) {
	args := testCommandArgs(t, singleTestMapping(`null`), inventoryWithTests(sampleTestSectionJSON))
	args[4] = writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent_test.go": "package agent\nimport \"testing\"\nfunc TestRunAsync(*testing.T) {}\nfunc TestPending(*testing.T) {}\n",
	})
	before, err := os.ReadFile(args[2])
	if err != nil {
		t.Fatal(err)
	}
	c, err := symbolcatalog.Decode(before)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Core.Tests/Example.Tests.AgentTests.MoreCases",
		"Core.Tests/Example.Tests.AgentTests.NoGo",
		"Core.Tests/Example.Tests.AgentTests.Pending",
		"Core.Tests/Example.Tests.AgentTests.PlatformSpecific",
		"Core.Tests/Example.Tests.AgentTests.RejectsNull",
		"Core.Tests/Example.Tests.AgentTests.RunAsync",
		"Core.Tests/Example.Tests." + sampleTestNestedType + "." + sampleTestGenericMethod,
		"Core.Tests/GlobalTests.All",
		"Other.Tests/Example.Tests.AgentTests.RunAsync",
	}
	names := make([]string, 0)
	for assembly, types := range c.Tests {
		for owner, methods := range types {
			for method, target := range methods {
				if target != nil {
					t.Fatalf("extraction paired an unreviewed test: %s %s.%s", assembly, owner, method)
				}
				names = append(names, assembly+"/"+owner+"."+method)
			}
		}
	}
	slices.Sort(names)
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("single-file null declarations = %v, want %v", names, want)
	}
	output := commandOutput(t, append(args, "-limit=0", "-check")...)
	var got testReconciliationReport
	decodeOutput(t, output, &got)
	if got.InventoryTests != len(want) || got.MappedTests != 0 || got.Go.TestFunctions != 2 || got.Counts["unreviewed"] != len(want) || len(got.Rows) != len(want) {
		t.Fatalf("null declarations were omitted or paired by name: %+v", got)
	}
	names = names[:0]
	for _, row := range got.Rows {
		if row.State != "unreviewed" || row.GoTest != "" || row.InvalidGoTest != "" || row.Reason != "" {
			t.Fatalf("a null leaf became a pair or reconciliation failure: %+v", row)
		}
		names = append(names, row.Assembly+"/"+row.Dotnet)
	}
	if !reflect.DeepEqual(names, want) || strings.Contains(output, `"go_test":`) || strings.Contains(output, `"suggested_go":`) {
		t.Fatalf("single-file report omitted null identities or guessed counterparts: %s", output)
	}
	after, err := os.ReadFile(args[2])
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("test reporting changed the unified catalog: %v", err)
	}
}

func TestTestReportFiltersAndPages(t *testing.T) {
	args := testCommandArgs(t, sampleTestMappingsJSON, inventoryWithTests(sampleTestSectionJSON))
	read := func(extra ...string) testReconciliationReport {
		var report testReconciliationReport
		decodeOutput(t, commandOutput(t, append(slices.Clone(args), extra...)...), &report)
		return report
	}
	for _, test := range []struct {
		name         string
		args         []string
		want, mapped int
	}{
		{"assembly", []string{"-assembly", "OTHER"}, 1, 0},
		{"namespace", []string{"-namespace", "EXAMPLE.TESTS"}, 8, 3},
		{"namespace excludes enclosing types", []string{"-namespace", "Example.Tests.Outer`1"}, 0, 0},
		{"generic nested type", []string{"-type", "outer`1+inner`1"}, 1, 1},
		{"type does not search methods", []string{"-type", "RunAsync"}, 0, 0},
		{"global type", []string{"-type", "GlobalTests"}, 1, 1},
		{"test name", []string{"-symbol", "RunAsync"}, 2, 1},
		{"signatures are not indexed", []string{"-symbol", "RunAsync("}, 0, 0},
		{"reverse Go target", []string{"-symbol", "AGENT.TESTCOMBINED"}, 1, 1},
		{"reverse internal Go target", []string{"-symbol", "internal/check.TestAlternate"}, 1, 1},
		{"unreviewed", []string{"-state", "unreviewed"}, 5, 0},
		{"linked", []string{"-state", "linked"}, 4, 4},
		{"intersecting", []string{"-assembly", "core", "-state", "unreviewed"}, 4, 0},
		{"unknown target", []string{"-symbol", "agent.TestMissing"}, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := read(test.args...)
			total := 0
			for _, count := range got.Counts {
				total += count
			}
			if got.Page == nil || got.Rows == nil || got.Page.Total != test.want || len(got.Rows) != test.want || total != test.want || got.MappedTests != test.mapped || got.InventoryTests != 9 || got.Go.TestFunctions != 4 {
				t.Fatalf("filter changed unfiltered inventory counts or selected the wrong declarations: %+v", got)
			}
		})
	}
	all := read("-limit=0")
	page := read("-limit=2", "-offset=1")
	if page.Page.Total != 9 || page.Page.Returned != 2 || page.Page.NextOffset == nil || *page.Page.NextOffset != 3 || !reflect.DeepEqual(page.Rows, all.Rows[1:3]) || !reflect.DeepEqual(page.testReconciliationSummary, all.testReconciliationSummary) {
		t.Fatalf("pagination changed test counts or deterministic order: %+v", page)
	}
	last := read("-limit=2", "-offset=8")
	if last.Page.Returned != 1 || last.Page.NextOffset != nil || !reflect.DeepEqual(last.Rows, all.Rows[8:]) {
		t.Fatalf("incorrect final page: %+v", last)
	}
	selected := read("-assembly", "core", "-state", "unreviewed", "-limit=0")
	filtered := read("-assembly", "core", "-state", "unreviewed", "-limit=2", "-offset=1")
	if len(selected.Rows) != 4 || filtered.Page.Total != 4 || filtered.Page.Returned != 2 || filtered.Page.NextOffset == nil || *filtered.Page.NextOffset != 3 || !reflect.DeepEqual(filtered.Rows, selected.Rows[1:3]) || !reflect.DeepEqual(filtered.testReconciliationSummary, selected.testReconciliationSummary) {
		t.Fatalf("test filters must precede paging without changing selected counts: %+v", filtered)
	}
	empty := read("-offset=100")
	if empty.Rows == nil || len(empty.Rows) != 0 || empty.Page.Total != 9 || empty.Page.NextOffset != nil || !reflect.DeepEqual(empty.testReconciliationSummary, all.testReconciliationSummary) {
		t.Fatalf("empty page changed counts: %+v", empty)
	}
	for _, filters := range [][]string{nil, {"-assembly", "core", "-state", "unreviewed"}, {"-symbol", "not-present"}} {
		selected := read(append(slices.Clone(filters), "-limit=0")...)
		invocation := append(append(slices.Clone(args), filters...), "-summary")
		var summary testReconciliationSummary
		decodeOutput(t, commandOutput(t, invocation...), &summary)
		if !reflect.DeepEqual(summary, selected.testReconciliationSummary) {
			t.Fatalf("summary changed selected test counts or provenance: %+v", summary)
		}
	}
}

func TestTestReconciliationUsesExactNames(t *testing.T) {
	valid := singleTestMapping(validTestMappingJSON)
	nested := `{"Core.Tests":{"Example.Tests.` + sampleTestNestedType + `":{"` + sampleTestGenericMethod + `":` + validTestMappingJSON + `}}}`
	for _, test := range []struct {
		name, entries string
		linked        bool
	}{
		{"exact test name", valid, true},
		{"method case", strings.Replace(valid, "RunAsync", "runAsync", 1), false},
		{"wrong declaring namespace", strings.Replace(valid, "Example.Tests", "Other.Tests", 1), false},
		{"no suffix type resolution", strings.Replace(valid, "Example.Tests", "Example", 1), false},
		{"wrong assembly", strings.Replace(valid, "Core.Tests", "core.Tests", 1), false},
		{"no inherited copy", strings.Replace(valid, "AgentTests", "DerivedTests", 1), false},
		{"generic method by name", nested, true},
		{"different method name", strings.Replace(nested, "Generic", "Other", 1), false},
		{"generic declaring arity", strings.Replace(nested, "Outer`1", "Outer`2", 1), false},
		{"nested metadata separator", strings.Replace(nested, "Outer`1+Inner`1", "Outer`1.Inner`1", 1), false},
		{"global namespace", `{"Core.Tests":{"GlobalTests":{"All":` + validTestMappingJSON + `}}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "wrong assembly" {
				c, err := symbolcatalog.Decode([]byte(catalogWithTests(sampleCatalogJSON, test.entries)))
				if err != nil {
					t.Fatal(err)
				}
				inv, err := symbolcatalog.DecodeInventory([]byte(inventoryWithTests(sampleTestSectionJSON)))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := symbolcatalog.Merge(c, inv); err == nil || !strings.Contains(err.Error(), `metadata is required for test assembly "core.Tests"`) {
					t.Fatalf("case-mismatched assembly must fail validation without normalization: %v", err)
				}
				return
			}
			args := testCommandArgs(t, test.entries, inventoryWithTests(sampleTestSectionJSON))
			var got testReconciliationReport
			decodeOutput(t, commandOutput(t, append(args, "-limit=0")...), &got)
			linked, dangling, unreviewed, rows := 0, 1, 9, 10
			if test.linked {
				linked, dangling, unreviewed, rows = 1, 0, 8, 9
			}
			if got.Counts["linked"] != linked || got.Counts["needs-reconciliation"] != dangling || got.Counts["unreviewed"] != unreviewed || got.InventoryTests != 9 || got.MappedTests != 1 || len(got.Rows) != rows {
				t.Fatalf("metadata identity was normalized, guessed, or consumed more than once: %+v", got)
			}
		})
	}
	// Even a same-named Go test must not manufacture a pair.
	args := testCommandArgs(t, `{}`, inventoryWithTests(sampleTestSectionJSON))
	args[len(args)-1] = writeGoCheckout(t, "example.org/sdk", map[string]string{
		"agent/agent_test.go": "package agent\nimport \"testing\"\nfunc TestRunAsync(*testing.T) {}\nfunc TestPending(*testing.T) {}\n",
	})
	var queue testReconciliationReport
	decodeOutput(t, commandOutput(t, append(args, "-check", "-limit=0")...), &queue)
	if queue.Counts["unreviewed"] != 9 || queue.MappedTests != 0 || queue.Go.TestFunctions != 2 {
		t.Fatalf("unreviewed tests were auto-mapped: %+v", queue)
	}
}

func TestTestReconciliationCheckIgnoresFilters(t *testing.T) {
	for _, test := range []struct{ name, old, replacement, state string }{
		{"missing Go test", "internal/check.TestAlternate", "internal/check.TestMissing", "invalid-go-target"},
		{"dangling .NET test", "RunAsync", "Removed", "needs-reconciliation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := strings.Replace(sampleTestMappingsJSON, test.old, test.replacement, 1)
			args := testCommandArgs(t, entries, inventoryWithTests(sampleTestSectionJSON))
			var all testReconciliationReport
			decodeOutput(t, commandOutput(t, append(slices.Clone(args), "-limit=0")...), &all)
			if all.Counts[test.state] != 1 || all.MappedTests != 4 {
				t.Fatalf("positive control did not expose the failing reference: %+v", all)
			}
			var failing testReconciliationReport
			decodeOutput(t, commandOutput(t, append(slices.Clone(args), "-state", test.state)...), &failing)
			if len(failing.Rows) != 1 || failing.MappedTests != 1 || failing.Rows[0].State != test.state || failing.Rows[0].Reason == "" {
				t.Fatalf("state filter lost the failing test pair: %+v", failing)
			}
			if test.state == "invalid-go-target" && (failing.Rows[0].GoTest != "internal/check.TestMissing" || failing.Rows[0].InvalidGoTest != "internal/check.TestMissing") {
				t.Fatalf("missing Go target must be recorded as a single function: %+v", failing.Rows[0])
			}
			for _, filters := range [][]string{
				{"-symbol", "not-present", "-limit=1"},
				{"-assembly", "Other.Tests", "-limit=1"},
				{"-state", "unreviewed", "-summary"},
				{"-state", "linked", "-offset=100"},
				{"-limit=1"},
			} {
				var out, diagnostics bytes.Buffer
				invocation := append(append(slices.Clone(args), filters...), "-check")
				err := run(invocation, &out, &diagnostics)
				if err == nil || !strings.Contains(err.Error(), "test reconciliation check failed") || out.Len() == 0 {
					t.Fatalf("hidden reference failure must emit a report then fail: %v, %q", err, out.String())
				}
				var report struct {
					InventoryTests int `json:"inventory_tests"`
				}
				if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.InventoryTests != 9 {
					t.Fatalf("check failure discarded the report or input count: %v, %s", err, out.String())
				}
			}
		})
	}
	entries := strings.Replace(strings.Replace(sampleTestMappingsJSON, "RunAsync", "Removed", 1), "internal/check.TestAlternate", "internal/check.TestMissing", 1)
	args := testCommandArgs(t, entries, inventoryWithTests(sampleTestSectionJSON))
	var got testReconciliationReport
	decodeOutput(t, commandOutput(t, append(slices.Clone(args), "-state", "needs-reconciliation")...), &got)
	if len(got.Rows) != 1 || got.MappedTests != 1 || got.Rows[0].GoTest != "internal/check.TestMissing" || got.Rows[0].InvalidGoTest != "internal/check.TestMissing" || got.Rows[0].State != "needs-reconciliation" {
		t.Fatalf("dangling .NET state masked its missing Go target: %+v", got)
	}
	var out, diagnostics bytes.Buffer
	err := run(append(slices.Clone(args), "-state", "linked", "-summary", "-check"), &out, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "1 of 10 unfiltered rows") || out.Len() == 0 {
		t.Fatalf("a doubly invalid pair must count as one failing row even when hidden: %v, %q", err, out.String())
	}
}

func TestTestReconciliationRequiresInputs(t *testing.T) {
	args := testCommandArgs(t, `{}`, sampleAPIInventoryJSON)
	args[len(args)-1] = filepath.Join(t.TempDir(), "must-not-index")
	var out, diagnostics bytes.Buffer
	err := run(append(args, "-symbol", "not-present", "-summary", "-check"), &out, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "has no tests section") || out.Len() != 0 {
		t.Fatalf("absent test inventory was reported as zero tests or indexed Go first: %v, %q", err, out.String())
	}
	args = testCommandArgs(t, `{}`, inventoryWithTests(sampleTestSectionJSON))
	args[len(args)-1] = writeGoCheckout(t, "example.org/other", nil)
	out.Reset()
	err = run(append(args, "-symbol", "not-present", "-summary"), &out, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "does not match catalog go_module") || out.Len() != 0 {
		t.Fatalf("test target presence was attributed to a different module: %v, %q", err, out.String())
	}
}

func TestTestReconciliationPreservesInputs(t *testing.T) {
	args := testCommandArgs(t, sampleTestMappingsJSON, inventoryWithTests(sampleTestSectionJSON))
	c, err := loadCatalog(args[2])
	if err != nil {
		t.Fatal(err)
	}
	pairs := flattenTests(c)
	wantPairs := maps.Clone(pairs)
	inv, err := c.Declarations()
	if err != nil {
		t.Fatal(err)
	}
	index, err := indexGoTests(args[4])
	if err != nil {
		t.Fatal(err)
	}
	inventorySHA256 := inv.SHA256
	// Reconciliation must not consume the pair index when it is reused with
	// different or unknown source revisions.
	for _, commit := range []string{"3333333333333333333333333333333333333333", "5555555555555555555555555555555555555555", ""} {
		for name, assembly := range inv.Tests.Assemblies {
			assembly.Commit = commit
			inv.Tests.Assemblies[name] = assembly
		}
		index.Commit = "4444444444444444444444444444444444444444"
		index.Dirty = new(true)
		switch commit {
		case "":
			index.Commit = ""
		case "5555555555555555555555555555555555555555":
			index.Commit = "6666666666666666666666666666666666666666"
		}
		wantIndex := index
		wantIndex.Tests = slices.Clone(index.Tests)
		wantIndex.Dirty = new(true)
		before, err := json.Marshal(inv)
		if err != nil {
			t.Fatal(err)
		}
		report := reconcileTests(pairs, inv, index)
		after, err := json.Marshal(inv)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pairs, wantPairs) || !reflect.DeepEqual(index, wantIndex) || !bytes.Equal(before, after) || inv.SHA256 != inventorySHA256 {
			t.Fatal("test reconciliation mutated its pair, inventory, or Go source inputs")
		}
		for _, brief := range []bool{false, true} {
			var out bytes.Buffer
			if err := writeTestReconciliation(&out, report, testReportFilter{}, pageOptions{}, true, brief, true); err != nil {
				t.Fatalf("commit changes alone must not fail reference checks: %v", err)
			}
			var got testReconciliationReport
			if brief {
				decodeOutput(t, out.String(), &got.testReconciliationSummary)
			} else {
				decodeOutput(t, out.String(), &got)
			}
			if got.InventoryTests != 9 || got.MappedTests != 4 || got.Counts["linked"] != 4 || got.Counts["unreviewed"] != 5 {
				t.Fatalf("reusing test pairs changed reconciliation results: %+v", got.testReconciliationSummary)
			}
			if !reflect.DeepEqual(got.Go, wantIndex.goTestMetadata) || got.Inventory.IdentityFormat != inv.Tests.IdentityFormat || got.Inventory.SHA256 != inventorySHA256 || len(got.Inventory.Assemblies) != len(inv.Tests.Assemblies) {
				t.Fatalf("inventory or Go source metadata changed: %+v", got.testReconciliationSummary)
			}
			for name, assembly := range inv.Tests.Assemblies {
				if got.Inventory.Assemblies[name] != assembly.AssemblyMetadata || got.Inventory.Assemblies[name].Commit != commit {
					t.Fatalf("test source metadata was lost or inferred from API metadata: %+v", got.Inventory)
				}
			}
			for _, removed := range []string{"review", "reviews", "go_tests", "invalid_go_tests", "status", "note", "review_source_changed", "review_go_changed", "by_status", "assessed_tests", "go_test_targets"} {
				if strings.Contains(out.String(), `"`+removed+`":`) {
					t.Errorf("test JSON retained removed %q metadata: %s", removed, out.String())
				}
			}
		}
	}
}

func TestTestReportWriteErrors(t *testing.T) {
	args := testCommandArgs(t, sampleTestMappingsJSON, inventoryWithTests(sampleTestSectionJSON))
	want := errors.New("test report output failed")
	for _, invocation := range [][]string{
		append(slices.Clone(args), "-json=false"),
		append(slices.Clone(args), "-summary"),
		{"go-tests", "-go-root", args[4]},
		{"go-tests", "-go-root", args[4], "-summary", "-json=false"},
	} {
		var diagnostics bytes.Buffer
		if err := run(invocation, errorReportWriter{want}, &diagnostics); !errors.Is(err, want) {
			t.Fatalf("test output error = %v, want %v", err, want)
		}
	}
}
