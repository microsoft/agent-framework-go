// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Decode the command's public JSON rather than inspecting its internal catalog.
type reportedMapping struct {
	Area      string   `json:"area"`
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Dotnet    string   `json:"dotnet"`
	Go        string   `json:"go"`
	GoSymbols []string `json:"go_symbols"`
	Status    string   `json:"status"`
	Note      string   `json:"note"`
	Assembly  string   `json:"assembly,omitempty"`
	Review    string   `json:"review,omitempty"`
}

type reportedMappings struct {
	Baseline map[string]any    `json:"baseline"`
	Reviews  map[string]any    `json:"reviews,omitempty"`
	Mappings []reportedMapping `json:"mappings"`
	Page     *pageInfo         `json:"page,omitempty"`
}

type reportedSummary struct {
	Baseline      map[string]any `json:"baseline"`
	Reviews       map[string]any `json:"reviews,omitempty"`
	DotnetSymbols int            `json:"dotnet_symbols"`
	GoSymbols     int            `json:"go_symbols"`
	GoOnlySymbols int            `json:"go_only_symbols,omitempty"`
	ByStatus      map[string]int `json:"by_status"`
	ByKind        map[string]int `json:"by_kind"`
	ByArea        map[string]int `json:"by_area"`
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
	// Exercise the default path from the repository root, without Git or source
	// line assertions that would require maintaining a second symbol inventory.
	t.Chdir(filepath.Join("..", ".."))
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
	root, err := filepath.Abs(filepath.Join("..", ".."))
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

	t.Chdir(root)
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

	var out, diagnostics bytes.Buffer
	err = run([]string{"reconcile", "-file", writeCatalog(t, sampleCatalogJSON), "-summary"}, &out, &diagnostics)
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
		Baseline: baseline{DotnetCommit: sourceCommit, GoCommit: goCommit},
		Mappings: []mappingRow{
			row("Agent", "agent.Agent"),
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
	inv := declarationInventory{
		SchemaVersion: 1, IdentityFormat: "ecma335-v1", SHA256: "inventory",
		Assemblies: map[string]declarationAssembly{"Core": {InformationalVersion: "1.0.0+" + sourceCommit}},
		Types: map[string]declarationType{
			"Example.Agent":        {Assembly: "Core", Kind: "class"},
			"Example.BrokenTarget": {Assembly: "Core", Kind: "class"},
			"Example.OutsideGo":    {Assembly: "Core", Kind: "class"},
			"Example.Unreviewed":   {Assembly: "Core", Kind: "class"},
		},
	}
	api := goInventory{
		Module: "example.org/sdk", Commit: goCommit, Packages: []string{"agent"},
		Symbols: map[string]goSymbol{"agent.Agent": {Kind: "type", Signature: "type agent.Agent struct"}},
	}
	got := reconcile(report, inv, api, false)
	want := map[string]int{
		"linked": 1, "unreviewed": 1, "needs-reconciliation": 1,
		"invalid-go-target": 1, "outside-inventory-scope": 1, "go-outside-scope": 1,
	}
	if !reflect.DeepEqual(got.Counts, want) {
		t.Fatalf("reconciliation states = %v, want %v", got.Counts, want)
	}
	if got.InventoryDeclarations != 4 || got.AssessedDeclarations != 5 {
		t.Fatalf("reconciliation totals = %d inventory, %d assessed", got.InventoryDeclarations, got.AssessedDeclarations)
	}

	duplicate := row("Agent", "agent.Agent")
	conflict := reconcile(mappingsReport{Baseline: report.Baseline, Mappings: []mappingRow{report.Mappings[0], duplicate}}, inv, api, true)
	if conflict.Counts["needs-reconciliation"] != 2 || conflict.Counts["unreviewed"] != 4 {
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
		"agent.LocalOptions": {"note":"Go-specific configuration.","review":"go-specific"},
		"agent.OnlyInGo": {"note":"Go-specific helper.","review":"go-specific"},
		"provider/other.Extra": {"note":"Go-specific provider helper.","review":"go-specific"}
	}`)
	file := writeCatalog(t, data)
	read := func(args ...string) goOnlyReport {
		var report goOnlyReport
		decodeOutput(t, commandOutput(t, append([]string{"go-only", "-file", file}, args...)...), &report)
		return report
	}
	got := read("-symbol", "AGENT.", "-limit", "1")
	if got.Page.Total != 2 || got.Page.Returned != 1 || got.Page.NextOffset == nil || *got.Page.NextOffset != 1 || got.GoOnly["agent.LocalOptions"].Review != "go-specific" {
		t.Fatalf("unexpected Go-only first page: %+v", got)
	}
	if len(got.Reviews) != 1 || got.GoOnly["agent.LocalOptions"].Note == "" {
		t.Fatalf("missing review evidence: %+v", got)
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
	for _, want := range []string{"Go-only assessments", "agent.OnlyInGo", "go-specific", "Go-specific helper."} {
		if !strings.Contains(text, want) {
			t.Errorf("text output missing %q: %s", want, text)
		}
	}
	got = read("-file", writeCatalog(t, sampleCatalogJSON))
	if got.GoOnly == nil || len(got.GoOnly) != 0 || got.Page.Total != 0 {
		t.Fatalf("catalog without optional section must report zero Go-only entries: %+v", got)
	}
}

func TestInvalidGoOnlyAssessments(t *testing.T) {
	for _, test := range []struct{ name, entries, want string }{
		{"unqualified", `{"OnlyInGo":{"note":"Go helper.","review":"go-specific"}}`, "invalid qualified Go symbol"},
		{"unexported", `{"agent.onlyInGo":{"note":"Go helper.","review":"go-specific"}}`, "invalid qualified Go symbol"},
		{"empty note", `{"agent.OnlyInGo":{"note":" ","review":"go-specific"}}`, "note must not be empty"},
		{"missing review", `{"agent.OnlyInGo":{"note":"Go helper."}}`, "existing review batch"},
		{"unknown review", `{"agent.OnlyInGo":{"note":"Go helper.","review":"unknown"}}`, "existing review batch"},
		{"no implicit baseline", `{"agent.OnlyInGo":{"note":"Go helper.","review":"baseline"}}`, "existing review batch"},
		{"null entry", `{"agent.OnlyInGo":null}`, "note must not be empty"},
		{"no mapping status", `{"agent.OnlyInGo":{"note":"Go helper.","review":"go-specific","status":"unmapped"}}`, "unknown field"},
		{"mapped counterpart", `{"agent.Agent":{"note":"Go helper.","review":"go-specific"}}`, "also assessed as go_only"},
		{"duplicate key", `{"agent.OnlyInGo":{"note":"Go helper.","review":"go-specific"},"agent.OnlyInGo":{"note":"Another note.","review":"go-specific"}}`, "duplicate JSON key"},
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
	entries := `{"agent.OnlyInGo":{"note":"Go helper.","review":"go-specific"},"agent.Missing":{"note":"Removed Go helper.","review":"go-specific"},"provider/other.Extra":{"note":"Go provider helper.","review":"go-specific"}}`
	file := writeCatalog(t, catalogWithGoOnly(minimal, entries))
	inventory := writeCatalog(t, `{"schema_version":1,"identity_format":"ecma335-v1","assemblies":{"Core":{"informational_version":"1.0.0+1111111111111111111111111111111111111111"}},"types":{"Example.Placeholder":{"assembly":"Core","kind":"class"}}}`)
	args := []string{"reconcile", "-file", file, "-inventory", inventory, "-go-root", root}
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
	validFile := writeCatalog(t, catalogWithGoOnly(minimal, `{"agent.OnlyInGo":{"note":"Go helper.","review":"go-specific"},"provider/other.Extra":{"note":"Go provider helper.","review":"go-specific"}}`))
	validArgs := []string{"reconcile", "-file", validFile, "-inventory", inventory, "-go-root", root, "-check", "-summary"}
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
		{"child note required", `, "note": "Name accessor."`, ``, "note"},
		{"blank note", `"note": "Name accessor."`, `"note": " \t "`, "note"},
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
		{"empty input", ``, "EOF"},
		{"array root", `[]`, "cannot unmarshal array"},
		{"null root", `null`, "schema_version"},
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
		{"gaps", "-summary"},
		{"mappings", "-limit", "-1"},
		{"mappings", "-offset", "-1"},
		{"go", "-limit", "-1"},
		{"reconcile", "-offset", "-1"},
		{"mappings", "-summary", "-limit", "0"},
		{"go", "-summary", "-offset", "0"},
		{"reconcile", "-summary", "-limit", "10"},
		{"gaps", "-tags", "alternate"},
		{"mappings", "-go-root", "./other"},
		{"mappings", "-go-package", "./agent/..."},
		{"go", "-file", "ignored.json"},
		{"go", "-area", "agents"},
		{"go", "-inventory", "ignored.json"},
		{"go", "-check"},
		{"go", "-go-package", " "},
		{"go-only", "-summary"},
		{"go-only", "-go-root", "."},
		{"go-only", "-type", "Agent"},
		{"go-only", "-limit", "-1"},
		{"changes"},
		{"changes", "-old-root", " "},
		{"changes", "-old-root", ".", "-limit", "-1"},
		{"changes", "-old-root", ".", "-summary", "-limit", "0"},
		{"changes", "-old-root", ".", "-file", "ignored.json"},
		{"changes", "-old-root", ".", "-check"},
		{"reconcile", "-state", "ready"},
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
		{"mappings", []string{"-file", "-area", "-kind", "-status", "-type", "-symbol", "-json", "-summary", "-limit", "-offset"}, []string{"-go-root", "-inventory", "-check"}},
		{"gaps", []string{"-file", "-area", "-status", "-limit", "-offset"}, []string{"-go-root", "-summary"}},
		{"go-only", []string{"-file", "-symbol", "-json", "-limit", "-offset"}, []string{"-go-root", "-summary", "-type", "-status"}},
		{"go", []string{"-go-root", "-go-package", "-tags", "-summary", "-symbol", "-json", "-limit", "-offset"}, []string{"-file", "-area", "-inventory", "-check"}},
		{"changes", []string{"-old-root", "-go-root", "-go-package", "-tags", "-summary", "-symbol", "-json", "-limit", "-offset"}, []string{"-file", "-inventory", "-check"}},
		{"reconcile", []string{"-file", "-inventory", "-go-root", "-check", "-state", "-summary", "-limit", "-offset"}, nil},
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
				if strings.Contains(help.String(), text) {
					t.Errorf("%s help unexpectedly includes %q: %s", command.name, text, help.String())
				}
			}
		})
	}
	var out, help bytes.Buffer
	if err := run([]string{"-help"}, &out, &help); err != nil || out.Len() != 0 {
		t.Fatalf("root help must not load inputs: %v, %q", err, out.String())
	}
	for _, name := range []string{"symbolmap", "mappings", "gaps", "go-only", "go", "changes", "reconcile"} {
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
	for _, view := range []string{"mappings", "summary", "gaps"} {
		var out, diagnostics bytes.Buffer
		// No fixture symbols match this filter. Validation must still inspect
		// the entire catalog before any view applies its filters.
		err := run(mappingViewArgs(view, "-file", file, "-area", "hosting", "-json"), &out, &diagnostics)
		if err == nil || (want != "" && !strings.Contains(err.Error(), want)) {
			t.Fatalf("%s error = %v, want %q", view, err, want)
		}
		if out.Len() != 0 {
			t.Fatalf("invalid catalog produced a successful-looking report: %s", out.String())
		}
	}
}

func catalogWithGoOnly(data, entries string) string {
	const review = `{"checked_at":"2026-09-29","dotnet_commit":"1111111111111111111111111111111111111111","go_commit":"2222222222222222222222222222222222222222","inventory_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scope":"Source-inspected Go-specific APIs."}`
	return strings.Replace(data, `"namespaces":`, `"reviews":{"go-specific":`+review+`},"go_only":`+entries+`,"namespaces":`, 1)
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
