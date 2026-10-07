// Copyright (c) Microsoft. All rights reserved.

package symbolcatalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"
)

const legacyCatalog = `{
	"schema_version":0,
	"baseline":{"checked_at":"2026-09-23","dotnet_repository":"https://example.org/dotnet","dotnet_commit":"1111111111111111111111111111111111111111","go_repository":"https://example.org/go","go_commit":"2222222222222222222222222222222222222222","go_module":"example.org/sdk","inventory_complete":false,"scope":"Selected declarations."},
	"go_only":{"agent.Unique":{"note":"Go-specific API."}},
	"tests":{"Core.Tests":{"Example.Tests.AgentTests":{"Run":"agent.TestRun"}}},
	"namespaces":{"Example":{"Agent":{"area":"agents","assembly":"Core","mapping":{"go":"agent.Agent{}","go_symbols":["agent.Agent"],"status":"adapted","note":"Configured agent."},"methods":{"Run(string)":{"go":"a.Run(ctx, text)","go_symbols":["agent.Agent.Run"],"status":"partial","note":"Reviewed limitation."}}}}}
}`

func extraction() symbolcatalog.Inventory {
	return symbolcatalog.Inventory{
		SchemaVersion: 1, IdentityFormat: "ecma335-v1",
		Selection: symbolcatalog.Selection{Namespaces: []string{}},
		Packages: map[string]symbolcatalog.Package{
			"Example.Core": {Version: "1.0.0", Assemblies: []string{"Core"}},
		},
		Assemblies: map[string]symbolcatalog.Assembly{
			"Core": {Version: "1.0.0.0", SHA256: strings.Repeat("b", 64)},
		},
		Types: map[string]symbolcatalog.Declaration{
			"Example.Agent": {
				Assembly: "Core", Kind: "class", BaseType: "System.Object",
				Constructors: map[string]symbolcatalog.Method{".ctor()": {ReturnType: "System.Void"}},
				Methods: map[string]symbolcatalog.Method{
					"Run(System.String) -> System.String": {
						ReturnType: "System.String", Parameters: []symbolcatalog.Parameter{{Type: "System.String"}},
					},
					"Run(System.Int32) -> System.String": {
						ReturnType: "System.String", Parameters: []symbolcatalog.Parameter{{Type: "System.Int32"}},
					},
					"Create``1(!!0) -> !!0": {
						ReturnType: "!!0", Parameters: []symbolcatalog.Parameter{{Type: "!!0"}},
						GenericParameters: []symbolcatalog.Generic{{Name: "T", Number: 0, Flags: 4}},
						Attributes:        symbolcatalog.Attributes{Experimental: "EX001", CompilerGenerated: true},
					},
					"Convert() -> System.Int32":  {ReturnType: "System.Int32"},
					"Convert() -> System.String": {ReturnType: "System.String"},
					"Try(System.Int32&) -> System.Void": {
						ReturnType: "System.Void", Parameters: []symbolcatalog.Parameter{{Type: "System.Int32&", Modifier: "out"}},
					},
					"Trace(System.Int32,...) -> System.Void": {
						ReturnType: "System.Void", VarArgs: true, Parameters: []symbolcatalog.Parameter{{Type: "System.Int32"}},
					},
					"Project(modreq(Example.Required) System.Int32) -> System.Void": {
						ReturnType: "System.Void", Parameters: []symbolcatalog.Parameter{{Type: "modreq(Example.Required) System.Int32"}},
					},
				},
				Properties: map[string]symbolcatalog.Property{
					"Name -> System.String": {Type: "System.String", Attributes: symbolcatalog.Attributes{NullableFlags: []int{2}}},
				},
				Events:    map[string]symbolcatalog.Event{"Changed -> System.Action": {Type: "System.Action"}},
				Fields:    map[string]symbolcatalog.Field{"Enabled": {Type: "System.Boolean"}},
				Constants: map[string]symbolcatalog.Field{"DefaultName": {Type: "System.String"}},
			},
			"Example.Box`1+Item": {
				Assembly: "Core", Kind: "class", GenericParameters: []symbolcatalog.Generic{{Name: "T", Number: 0}},
				Methods: map[string]symbolcatalog.Method{
					"Get() -> !0": {ReturnType: "!0"},
				},
			},
			"Example.ValueBox`1": {
				Assembly: "Core", Kind: "class", GenericParameters: []symbolcatalog.Generic{{Name: "T", Number: 0, Flags: 8}},
				Methods: map[string]symbolcatalog.Method{
					"Accept(System.Nullable`1<!0>) -> System.Void": {
						ReturnType: "System.Void", Parameters: []symbolcatalog.Parameter{{Type: "System.Nullable`1<!0>"}},
					},
				},
			},
			"Example.Mode": {
				Assembly: "Core", Kind: "enum", Constants: map[string]symbolcatalog.Field{"Default": {Type: "Example.Mode"}},
			},
		},
		Tests: &testinventory.Inventory{
			IdentityFormat: testinventory.IdentityFormat,
			Assemblies: map[string]testinventory.Assembly{
				"Core.Tests": {
					AssemblyMetadata: testinventory.AssemblyMetadata{Commit: strings.Repeat("c", 40), SHA256: strings.Repeat("d", 64)},
					Types:            map[string][]string{"Example.Tests.AgentTests": {"NewCase", "Run"}},
				},
			},
		},
	}
}

func TestMergePreservesMappingsAndDeclarations(t *testing.T) {
	c, err := symbolcatalog.Decode([]byte(legacyCatalog))
	if err != nil {
		t.Fatal(err)
	}
	inv := extraction()
	beforeCatalog, _ := json.Marshal(c)
	beforeInventory, _ := json.Marshal(inv)
	merged, err := symbolcatalog.Merge(c, inv)
	if err != nil {
		t.Fatal(err)
	}
	afterCatalog, _ := json.Marshal(c)
	afterInventory, _ := json.Marshal(inv)
	if !bytes.Equal(beforeCatalog, afterCatalog) || !bytes.Equal(beforeInventory, afterInventory) {
		t.Fatal("merge mutated an input")
	}
	if !reflect.DeepEqual(c.Baseline, merged.Baseline) || !reflect.DeepEqual(c.GoOnly, merged.GoOnly) {
		t.Fatal("merge changed the baseline or Go-only assessments")
	}
	old := c.Namespaces["Example"]["Agent"]
	current := merged.Namespaces["Example"]["Agent"]
	if !reflect.DeepEqual(old.Mapping, current.Mapping) {
		t.Fatal("type assessment changed during extraction")
	}
	m := current.Methods["Run(string) -> string"]
	if !reflect.DeepEqual(m, old.Methods["Run(string)"]) {
		t.Fatal("member assessment changed during extraction")
	}
	if m := current.Methods["Run(int) -> string"]; !m.Unreviewed || m.Status != "unmapped" || len(m.GoSymbols) != 0 || m.Note != "" {
		t.Fatalf("new overload inherited a counterpart or assessment note: %+v", m)
	}
	if current.Kind != "" || current.Identity != "" || merged.Namespaces["Example"]["Mode"].Kind != "enum" {
		t.Fatal("ordinary declaring identities were duplicated or a non-class kind was lost")
	}
	for _, label := range []string{"Run(string) -> string", "Run(int) -> string", "Create<T>(T) -> T", "Try(out int) -> void", "Trace(int, ...) -> void"} {
		if _, present := current.Methods[label]; !present {
			t.Errorf("method signature omitted its return type: %s", label)
		}
	}
	if _, present := merged.Namespaces["Example"]["Box<T>.Item"].Methods["Get() -> T"]; !present {
		t.Error("nested generic method omitted its return type")
	}
	if generated := current.Methods["Create<T>(T) -> T"]; generated.Experimental != "EX001" || !generated.Generated {
		t.Fatal("experimental or generated markers were lost")
	}
	for _, label := range []string{"Convert() -> int", "Convert() -> string", "Try(out int) -> void", "Trace(int, ...) -> void", "Project(modreq(Example.Required) System.Int32) -> System.Void"} {
		if _, present := current.Methods[label]; !present {
			t.Errorf("compact declaration lost a distinguishing signature: %s", label)
		}
	}
	if merged.Tests["Core.Tests"]["Example.Tests.AgentTests"]["NewCase"] != nil || *merged.Tests["Core.Tests"]["Example.Tests.AgentTests"]["Run"] != "agent.TestRun" {
		t.Fatal("new test was paired or existing pair changed")
	}
	projected, err := merged.Declarations()
	if err != nil {
		t.Fatal(err)
	}
	if projected.SchemaVersion != inv.SchemaVersion || projected.IdentityFormat != inv.IdentityFormat || !reflect.DeepEqual(projected.Selection, inv.Selection) || !reflect.DeepEqual(projected.Assemblies, inv.Assemblies) || !reflect.DeepEqual(projected.Tests, inv.Tests) {
		t.Fatal("catalog compaction changed extraction provenance or test declarations")
	}
	if _, err := symbolcatalog.Merge(merged, projected); err == nil {
		t.Fatal("reporting labels were accepted as a new raw extraction snapshot")
	}
	rawNames, labels := symbolcatalog.NewNames(inv), symbolcatalog.NewNames(projected)
	for owner := range inv.Types {
		namespace, label := rawNames.TypeLabel(owner)
		if ns, got := labels.TypeLabel(owner); ns != namespace || got != label {
			t.Fatalf("declaring label changed: got %s.%s, want %s.%s", ns, got, namespace, label)
		}
		entry := merged.Namespaces[namespace][label]
		for kind, members := range map[string]map[string]symbolcatalog.Mapping{
			"constructor": entry.Constructors, "method": entry.Methods, "property": entry.Properties,
			"event": entry.Events, "field": entry.Fields, "constant": entry.Constants,
		} {
			for name := range members {
				if matches := rawNames.ResolveMember(owner, kind, namespace+"."+label, name); len(matches) != 1 {
					t.Fatalf("compact declaration no longer identifies one extracted member: %s.%s: %v", owner, name, matches)
				}
				if matches := labels.ResolveMember(owner, kind, namespace+"."+label, name); len(matches) != 1 || matches[0] != name {
					t.Fatalf("reporting guessed instead of using the stored label: %s.%s: %v", owner, name, matches)
				}
			}
		}
	}
	encoded, err := symbolcatalog.Encode(merged)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"NewCase": null`)) || bytes.Contains(encoded, []byte(`"types":`)) {
		t.Fatal("merged document did not inline declarations or retained a duplicate inventory")
	}
	for _, redundant := range []string{`"declaration":`, `"base_type":`, `"return_type":`, `"parameters":`, `"nullable_flags":`} {
		if bytes.Contains(encoded, []byte(redundant)) {
			t.Errorf("catalog retained redundant extraction metadata %s", redundant)
		}
	}
	decoded, err := symbolcatalog.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	again, err := symbolcatalog.Merge(decoded, inv)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := symbolcatalog.Encode(again)
	if err != nil || !bytes.Equal(encoded, repeated) {
		t.Fatalf("identical extraction is not deterministic and idempotent: %v", err)
	}
	t.Run("mapped notes stay omitted during refresh", func(t *testing.T) {
		data := strings.Replace(legacyCatalog, `"status":"partial","note":"Reviewed limitation."`, `"status":"mapped"`, 1)
		c, err := symbolcatalog.Decode([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		merged, err := symbolcatalog.Merge(c, extraction())
		if err != nil {
			t.Fatal(err)
		}
		if got := merged.Namespaces["Example"]["Agent"].Methods["Run(string) -> string"]; !reflect.DeepEqual(got, c.Namespaces["Example"]["Agent"].Methods["Run(string)"]) {
			t.Fatalf("refresh added a note or changed a mapped assessment: %+v", got)
		}
		encoded, err := symbolcatalog.Encode(merged)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := symbolcatalog.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Namespaces["Example"]["Agent"].Methods["Run(string) -> string"].Note != "" {
			t.Fatal("encoding supplied a note for a mapped assessment")
		}
		if again, err := symbolcatalog.Merge(decoded, extraction()); err != nil || !reflect.DeepEqual(merged, again) {
			t.Fatalf("mapped assessment without a note was not idempotent: %v", err)
		}
	})
	t.Run("ambiguous refresh requires an identity override", func(t *testing.T) {
		ambiguous := extraction()
		ambiguous.Types["Example.Agent"].Methods["Run(System.String) -> System.Int32"] = symbolcatalog.Method{
			ReturnType: "System.Int32", Parameters: []symbolcatalog.Parameter{{Type: "System.String"}},
		}
		complete, err := symbolcatalog.Merge(decoded, ambiguous)
		if err != nil || !reflect.DeepEqual(complete.Namespaces["Example"]["Agent"].Methods["Run(string) -> string"], decoded.Namespaces["Example"]["Agent"].Methods["Run(string) -> string"]) {
			t.Fatalf("complete return signature lost its assessment when a return-only overload appeared: %v", err)
		}
		legacy, err := symbolcatalog.Decode([]byte(legacyCatalog))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := symbolcatalog.Merge(legacy, ambiguous); err == nil || !strings.Contains(err.Error(), "ambiguous member") {
			t.Fatalf("refresh arbitrarily selected a return-only overload: %v", err)
		}
		entry := legacy.Namespaces["Example"]["Agent"]
		m := entry.Methods["Run(string)"]
		m.Identity = "Run(System.String) -> System.String"
		entry.Methods["Run(string)"] = m
		resolved, err := symbolcatalog.Merge(legacy, ambiguous)
		if err != nil {
			t.Fatal(err)
		}
		m.Identity = "" // The completed return signature now supplies the identity.
		if got := resolved.Namespaces["Example"]["Agent"].Methods["Run(string) -> string"]; !reflect.DeepEqual(got, m) {
			t.Fatalf("completed identity changed the assessment or retained redundant metadata: %+v", got)
		}
		if !resolved.Namespaces["Example"]["Agent"].Methods["Run(string) -> int"].Unreviewed {
			t.Fatal("return-only overload was collapsed into the existing assessment")
		}
		if again, err := symbolcatalog.Merge(resolved, ambiguous); err != nil || !reflect.DeepEqual(resolved, again) {
			t.Fatalf("ambiguous explicit identities did not survive repeated refresh: %v", err)
		}
	})
	t.Run("nullable reference label uses raw metadata only during refresh", func(t *testing.T) {
		c, err := symbolcatalog.Decode([]byte(legacyCatalog))
		if err != nil {
			t.Fatal(err)
		}
		c.Namespaces["Example"]["Agent"].Methods["Accept(Reference?)"] = symbolcatalog.Mapping{
			Go: new(""), GoSymbols: []string{}, Status: "unmapped", Note: "Reviewed reference overload.",
		}
		inv := extraction()
		inv.Types["Example.Agent"].Methods["Accept(Other.Reference) -> System.Void"] = symbolcatalog.Method{
			ReturnType: "System.Void", Parameters: []symbolcatalog.Parameter{{Type: "Other.Reference", Attributes: symbolcatalog.Attributes{NullableFlags: []int{2}}}},
		}
		merged, err := symbolcatalog.Merge(c, inv)
		if err != nil {
			t.Fatal(err)
		}
		m := merged.Namespaces["Example"]["Agent"].Methods["Accept(Reference?) -> void"]
		if m.Identity != "" || m.Unavailable {
			t.Fatalf("nullable-reference spelling required redundant metadata or failed to resolve: %+v", m)
		}
		if again, err := symbolcatalog.Merge(merged, inv); err != nil || !reflect.DeepEqual(merged, again) {
			t.Fatalf("nullable-reference assessment did not survive repeated refresh: %v", err)
		}
	})
	t.Run("return binding follows existing generic names", func(t *testing.T) {
		c, err := symbolcatalog.Decode([]byte(legacyCatalog))
		if err != nil {
			t.Fatal(err)
		}
		c.Namespaces["Example"]["Agent"].Methods["Create<TResult>(TResult)"] = symbolcatalog.Mapping{
			Go: new(""), GoSymbols: []string{}, Status: "unmapped", Note: "Reviewed generic method.",
		}
		merged, err := symbolcatalog.Merge(c, extraction())
		if err != nil {
			t.Fatal(err)
		}
		if m, present := merged.Namespaces["Example"]["Agent"].Methods["Create<TResult>(TResult) -> TResult"]; !present || m.Unavailable {
			t.Fatal("return signature used extraction generic names instead of existing positional labels")
		}
		if again, err := symbolcatalog.Merge(merged, extraction()); err != nil || !reflect.DeepEqual(merged, again) {
			t.Fatalf("positional return binding was not idempotent: %v", err)
		}
	})
	t.Run("unrefreshed package remains unchanged", func(t *testing.T) {
		partial := extraction()
		partial.Packages = map[string]symbolcatalog.Package{"Example.Other": {Assemblies: []string{"Other"}}}
		partial.Assemblies = map[string]symbolcatalog.Assembly{"Other": {SHA256: strings.Repeat("e", 64)}}
		partial.Types = map[string]symbolcatalog.Declaration{"Other.Widget": {Assembly: "Other", Kind: "class"}}
		partial.Tests = nil
		refreshed, err := symbolcatalog.Merge(decoded, partial)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(refreshed.Namespaces["Example"], decoded.Namespaces["Example"]) ||
			!reflect.DeepEqual(refreshed.Dotnet.Packages["Example.Core"], decoded.Dotnet.Packages["Example.Core"]) ||
			!reflect.DeepEqual(refreshed.Dotnet.Assemblies["Core"], decoded.Dotnet.Assemblies["Core"]) {
			t.Fatal("refreshing another package changed the unselected package's declarations or provenance")
		}
	})
	t.Run("owning package refresh updates provenance", func(t *testing.T) {
		fresh := extraction()
		pkg := fresh.Packages["Example.Core"]
		pkg.Version, pkg.SHA256 = "2.0.0", strings.Repeat("e", 64)
		fresh.Packages["Example.Core"] = pkg
		assembly := fresh.Assemblies["Core"]
		assembly.Version, assembly.SHA256 = "2.0.0.0", strings.Repeat("f", 64)
		fresh.Assemblies["Core"] = assembly
		refreshed, err := symbolcatalog.Merge(decoded, fresh)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(refreshed.Dotnet.Packages["Example.Core"], pkg) || !reflect.DeepEqual(refreshed.Dotnet.Assemblies["Core"], assembly) {
			t.Fatal("package and assembly provenance were not refreshed together")
		}
		if !reflect.DeepEqual(refreshed.Namespaces, decoded.Namespaces) || !reflect.DeepEqual(refreshed.Tests, decoded.Tests) {
			t.Fatal("provenance refresh changed declarations, assessments, or test pairs")
		}
	})
	t.Run("unowned local assembly refresh remains supported", func(t *testing.T) {
		c, err := symbolcatalog.Decode([]byte(legacyCatalog))
		if err != nil {
			t.Fatal(err)
		}
		fresh := extraction()
		fresh.Packages = nil
		local, err := symbolcatalog.Merge(c, fresh)
		if err != nil {
			t.Fatal(err)
		}
		assembly := fresh.Assemblies["Core"]
		assembly.Version, assembly.SHA256 = "2.0.0.0", strings.Repeat("f", 64)
		fresh.Assemblies["Core"] = assembly
		refreshed, err := symbolcatalog.Merge(local, fresh)
		if err != nil {
			t.Fatal(err)
		}
		if len(refreshed.Dotnet.Packages) != 0 || !reflect.DeepEqual(refreshed.Dotnet.Assemblies["Core"], assembly) {
			t.Fatal("local refresh lost assembly provenance or invented package provenance")
		}
		if !reflect.DeepEqual(refreshed.Namespaces, local.Namespaces) || !reflect.DeepEqual(refreshed.Tests, local.Tests) {
			t.Fatal("local provenance refresh changed declarations, assessments, or test pairs")
		}
	})
}

func TestRefreshRetainsRemovedAssessments(t *testing.T) {
	c, err := symbolcatalog.Decode([]byte(legacyCatalog))
	if err != nil {
		t.Fatal(err)
	}
	inv := extraction()
	c, err = symbolcatalog.Merge(c, inv)
	if err != nil {
		t.Fatal(err)
	}
	typ := inv.Types["Example.Agent"]
	delete(typ.Methods, "Run(System.String) -> System.String")
	delete(typ.Methods, "Run(System.Int32) -> System.String")
	inv.Types["Example.Agent"] = typ
	assembly := inv.Tests.Assemblies["Core.Tests"]
	assembly.Types["Example.Tests.AgentTests"] = []string{"Replacement"}
	inv.Tests.Assemblies["Core.Tests"] = assembly
	refreshed, err := symbolcatalog.Merge(c, inv)
	if err != nil {
		t.Fatal(err)
	}
	entry := refreshed.Namespaces["Example"]["Agent"]
	if m := entry.Methods["Run(string) -> string"]; !m.Unavailable || m.Status != "partial" || m.Note != "Reviewed limitation." {
		t.Fatalf("removed assessment was discarded or still claims current metadata: %+v", m)
	}
	if _, exists := entry.Methods["Run(int) -> string"]; exists {
		t.Fatal("removed unreviewed overload was retained as a current declaration")
	}
	methods := refreshed.Tests["Core.Tests"]["Example.Tests.AgentTests"]
	if methods["Run"] == nil || *methods["Run"] != "agent.TestRun" {
		t.Fatal("removed test lost its existing pair")
	}
	if _, exists := methods["NewCase"]; exists {
		t.Fatal("removed unpaired test was retained")
	}
	projected, err := refreshed.Declarations()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projected.Tests.Assemblies["Core.Tests"].Types["Example.Tests.AgentTests"], []string{"Replacement"}) {
		t.Fatal("removed paired test was misreported as a current declaration")
	}
	inv.Tests = nil
	apiOnly, err := symbolcatalog.Merge(refreshed, inv)
	if err != nil || !reflect.DeepEqual(apiOnly.Tests, refreshed.Tests) || !reflect.DeepEqual(apiOnly.Dotnet.Tests, refreshed.Dotnet.Tests) {
		t.Fatalf("API-only refresh changed test metadata or pairs: %v", err)
	}
}

func TestUpdateFailurePreservesFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mapping.json")
	if err := os.WriteFile(file, []byte(legacyCatalog), 0o600); err != nil {
		t.Fatal(err)
	}
	inv := extraction()
	if err := symbolcatalog.Update(file, inv); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{
		"scope", "duplicate target", "unknown assembly", "missing API hash", "invalid API hash",
		"dropped package assembly", "replaced package assembly",
		"local package-owned assembly", "foreign package-owned assembly",
	} {
		t.Run(failure, func(t *testing.T) {
			broken := extraction()
			want := ""
			switch failure {
			case "scope":
				broken.Selection.IncludeProtected = true
			case "duplicate target":
				c, err := symbolcatalog.Decode(before)
				if err != nil {
					t.Fatal(err)
				}
				c.Tests["Core.Tests"]["Example.Tests.AgentTests"]["NewCase"] = new("agent.TestRun")
				if _, err := symbolcatalog.Merge(c, broken); err == nil {
					t.Fatal("merge accepted a reused test target")
				}
				broken.IdentityFormat = "unsupported"
			case "unknown assembly":
				delete(broken.Assemblies, "Core")
			case "missing API hash", "invalid API hash":
				assembly := broken.Assemblies["Core"]
				assembly.SHA256 = ""
				if failure == "invalid API hash" {
					assembly.SHA256 = strings.Repeat("g", 64)
				}
				broken.Assemblies["Core"] = assembly
				want = "API assembly SHA256"
			case "dropped package assembly", "replaced package assembly":
				broken.Assemblies = map[string]symbolcatalog.Assembly{"Other": {SHA256: strings.Repeat("e", 64)}}
				broken.Types = map[string]symbolcatalog.Declaration{"Other.Widget": {Assembly: "Other", Kind: "class"}}
				assemblies := []string{"Other"}
				if failure == "replaced package assembly" {
					broken.Assemblies["Replacement"] = symbolcatalog.Assembly{SHA256: strings.Repeat("f", 64)}
					broken.Types["Other.Replacement"] = symbolcatalog.Declaration{Assembly: "Replacement", Kind: "class"}
					assemblies = append(assemblies, "Replacement")
				}
				broken.Packages["Example.Core"] = symbolcatalog.Package{Version: "2.0.0", Assemblies: assemblies}
				want = `package "Example.Core" no longer supplies assembly "Core"`
			case "local package-owned assembly", "foreign package-owned assembly":
				assembly := broken.Assemblies["Core"]
				assembly.Version, assembly.SHA256 = "2.0.0.0", strings.Repeat("f", 64)
				broken.Assemblies["Core"] = assembly
				broken.Packages = nil
				if failure == "foreign package-owned assembly" {
					broken.Packages = map[string]symbolcatalog.Package{"Example.Other": {Version: "2.0.0", Assemblies: []string{"Core"}}}
				}
				want = `assembly "Core" belongs to package "Example.Core"`
			}
			if err := symbolcatalog.Update(file, broken); err == nil || want != "" && !strings.Contains(err.Error(), want) {
				t.Fatalf("invalid refresh error = %v; want rejection containing %q", err, want)
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed update changed the original file: %v", err)
			}
			files, err := os.ReadDir(filepath.Dir(file))
			if err != nil || len(files) != 1 {
				t.Fatalf("failed update left temporary files: %v", err)
			}
		})
	}
}
