// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"
)

func TestLocalAssemblyInventory(t *testing.T) {
	data := testAssembly(t)
	file := filepath.Join(t.TempDir(), "Sample.dll")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if err := run([]string{"-assembly", file, "-namespace", "Example.Child"}, &out, &diagnostics); err != nil {
		t.Fatalf("extract local assembly: %v", err)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %s", diagnostics.String())
	}
	var got symbolcatalog.Inventory
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	if got.SchemaVersion != 1 || got.IdentityFormat != "ecma335-v1" || !slices.Equal(got.Selection.Namespaces, []string{"Example.Child"}) {
		t.Fatalf("unexpected inventory metadata: %+v", got)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &shape); err != nil {
		t.Fatal(err)
	}
	if _, present := shape["tests"]; present || got.Tests != nil {
		t.Fatalf("API-only output unexpectedly contains tests: %s", out.String())
	}
	assembly, ok := got.Assemblies["Sample"]
	if !ok || assembly.Version != "1.2.3.4" || assembly.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("unexpected assembly: %+v", got.Assemblies)
	}
	if len(got.Types) != 1 || got.Types["Example.Child.Gadget"].Assembly != "Sample" {
		t.Fatalf("namespace filter returned types: %+v", got.Types)
	}
	wantOutput := fmt.Sprintf(strings.ReplaceAll(`{
	"schema_version": 1,
	"identity_format": "ecma335-v1",
	"selection": {
		"namespaces": [
			"Example.Child"
		],
		"include_protected": false
	},
	"assemblies": {
		"Sample": {
			"version": "1.2.3.4",
			"sha256": "%x",
			"reference_assembly": false
		}
	},
	"types": {
		"Example.Child.Gadget": {
			"assembly": "Sample",
			"kind": "class"
		}
	}
}
`, "\t", "  "), sha256.Sum256(data))
	if out.String() != wantOutput {
		t.Fatalf("schema-1 stdout changed: got %s, want %s", out.String(), wantOutput)
	}

	short := testWriter(func(p []byte) (int, error) { return len(p) / 2, nil })
	if err := run([]string{"-assembly", file}, short, &diagnostics); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short report write: got %v, want io.ErrShortWrite", err)
	}
	writeErr := errors.New("output failed")
	failing := testWriter(func([]byte) (int, error) { return 0, writeErr })
	if err := run([]string{"-assembly", file}, failing, &diagnostics); !errors.Is(err, writeErr) {
		t.Fatalf("failed report write: got %v, want %v", err, writeErr)
	}
}

func TestInvalidCommandInput(t *testing.T) {
	badPE := filepath.Join(t.TempDir(), "bad.dll")
	if err := os.WriteFile(badPE, []byte("not a managed assembly"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"invalid metadata", []string{"-assembly", badPE}, "PE:"},
		{"conflicting modes", []string{"-assembly", badPE, "-release", "1.2.3"}, "cannot be combined"},
		{"missing assembly", []string{"-assembly", filepath.Join(t.TempDir(), "missing.dll")}, "matched no files"},
		{"invalid namespace", []string{"-assembly", badPE, "-namespace", "Example."}, "namespace must be"},
		{"invalid release", []string{"-release", "broken"}, "expected an exact NuGet version"},
		{"invalid package", []string{"-package", "invalid@version"}, "expected an exact NuGet version"},
		{"invalid feed", []string{"-nuget-source", "http://example.test/v3/index.json"}, "expected an absolute HTTPS URL"},
		{"invalid framework", []string{"-framework", "net8.0/other"}, "invalid exact target framework"},
		{"empty test pattern", []string{"-test-assembly", "", "-release", "broken"}, "test assembly pattern must not be empty"},
		{"blank test pattern", []string{"-test-assembly", " "}, "test assembly pattern must not be empty"},
		{"missing test assembly", []string{"-assembly", badPE, "-test-assembly", filepath.Join(t.TempDir(), "missing.dll")}, "matched no files"},
		{"empty update path", []string{"-assembly", badPE, "-update-mapping", ""}, "update-mapping path must not be empty"},
		{"blank update path", []string{"-assembly", badPE, "-update-mapping", " \t "}, "update-mapping path must not be empty"},
		{"empty input path", []string{"-input", ""}, "input path must not be empty"},
		{"blank input path", []string{"-input", " \t "}, "input path must not be empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			err := run(test.args, &out, &diagnostics)
			if err == nil || !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
				t.Fatalf("run(%v) = %v, output %q; want error containing %q and no report", test.args, err, out.String(), test.want)
			}
		})
	}
	var out, help bytes.Buffer
	if err := run([]string{"-help"}, &out, &help); err != nil || out.Len() != 0 || !strings.Contains(help.String(), "-assembly") {
		t.Fatalf("help: %v, output %q, diagnostics %q", err, out.String(), help.String())
	}
}

func TestLocalAssemblyWithTestMetadata(t *testing.T) {
	file := filepath.Join(t.TempDir(), "Sample.dll")
	if err := os.WriteFile(file, testAssembly(t), 0o600); err != nil {
		t.Fatal(err)
	}
	data := testAssemblyWithTests(t, testAssemblyOptions{})
	tests := filepath.Join(t.TempDir(), "Tests.dll")
	duplicate := filepath.Join(t.TempDir(), "DifferentFileName.dll")
	for _, path := range []string{tests, duplicate} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Metadata extraction must not invoke Git, a .NET SDK, or a test runner.
	t.Setenv("PATH", t.TempDir())
	var out, diagnostics bytes.Buffer
	args := []string{"-assembly", file, "-namespace", "Example.Child", "-test-assembly", tests, "-test-assembly", duplicate}
	if err := run(args, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var got symbolcatalog.Inventory
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tests == nil || got.Tests.IdentityFormat != testinventory.IdentityFormat || len(got.Tests.Assemblies) != 1 {
		t.Fatalf("unexpected test provenance: %+v", got.Tests)
	}
	assembly := got.Tests.Assemblies["Sample.Tests"]
	if assembly.Commit != "3333333333333333333333333333333333333333" || assembly.TargetFramework != ".NETCoreApp,Version=v10.0" || assembly.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("missing test build metadata: %+v", assembly.AssemblyMetadata)
	}
	want := map[string][]string{
		"Example.Tests.Cases":            {"Run", "Theory"},
		"Example.Tests.Outer`1+Nested`1": {"Generic", "Retry"},
	}
	if !reflect.DeepEqual(assembly.Types, want) || len(got.Types) != 1 || got.Types["Example.Child.Gadget"].Assembly != "Sample" {
		t.Fatalf("API selection changed test discovery or method names: %+v", got)
	}
	encoded, err := json.Marshal(got.Tests)
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{`"attribute"`, `"version"`, `"informational_version"`, "FactAttribute", "System.Void", "System.Boolean", " -> ", "Generic``1"} {
		if bytes.Contains(encoded, []byte(omitted)) {
			t.Fatalf("name-only test inventory retained %q: %s", omitted, encoded)
		}
	}
	var again bytes.Buffer
	if err := run(args, &again, &diagnostics); err != nil || !bytes.Equal(out.Bytes(), again.Bytes()) {
		t.Fatalf("repeated metadata inventory differs: %v", err)
	}
	// Failure after assembly extraction must still leave stdout empty.
	out.Reset()
	args[len(args)-1] = file // An API-only assembly is not a test assembly.
	if err := run(args, &out, &diagnostics); err == nil || !strings.Contains(err.Error(), "no supported test declarations") || out.Len() != 0 {
		t.Fatalf("failed test extraction = %v, output %q", err, out.String())
	}
	if err := os.WriteFile(duplicate, append(data, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = duplicate
	if err := run(args, &out, &diagnostics); err == nil || !strings.Contains(err.Error(), "multiple different inputs for test assembly") || out.Len() != 0 {
		t.Fatalf("conflicting test build = %v, output %q", err, out.String())
	}
}

func TestUpdateMapping(t *testing.T) {
	assembly := writeCommandFile(t, "Sample.dll", testAssembly(t))
	tests := writeCommandFile(t, "Tests.dll", testAssemblyWithTests(t, testAssemblyOptions{}))
	mapping := writeCommandFile(t, "mapping.json", []byte(testLegacyMapping))
	original, err := symbolcatalog.Decode([]byte(testLegacyMapping))
	if err != nil {
		t.Fatalf("invalid legacy baseline: %v", err)
	}
	args := []string{"-assembly", assembly, "-test-assembly", tests}
	var out, diagnostics bytes.Buffer
	if err := run(args, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	inv, err := symbolcatalog.DecodeInventory(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(mapping)
	if err != nil || string(unchanged) != testLegacyMapping {
		t.Fatalf("ordinary stdout extraction changed the catalog: %v", err)
	}
	args = append(args, "-update-mapping", mapping)
	out.Reset()
	if err := run(args, &out, &diagnostics); err != nil || out.Len() != 0 {
		t.Fatalf("update = %v, stdout %q; want a silent update", err, out.String())
	}
	data, err := os.ReadFile(mapping)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := symbolcatalog.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.Baseline, original.Baseline) || !reflect.DeepEqual(updated.GoOnly, original.GoOnly) {
		t.Fatal("update changed the baseline or Go-only assessments")
	}
	entry := updated.Namespaces["Example"]["Widget"]
	entry.Methods = maps.Clone(entry.Methods)
	missing := entry.Methods["Run(string)"]
	if !missing.Unavailable {
		t.Fatal("assessed member absent from the extraction was treated as current")
	}
	missing.Unavailable = false
	entry.Methods["Run(string)"] = missing
	if !reflect.DeepEqual(entry, original.Namespaces["Example"]["Widget"]) {
		t.Fatalf("update changed an existing assessment: %+v", entry)
	}
	fresh := updated.Namespaces["Example.Child"]["Gadget"]
	if fresh.Assembly != "Sample" || fresh.Identity != "" || fresh.Kind != "" || fresh.Mapping == nil {
		t.Fatalf("new API declaration is missing: %+v", fresh)
	}
	m := fresh.Mapping
	if !m.Unreviewed || m.Status != "unmapped" || m.Go == nil || *m.Go != "" || m.GoSymbols == nil || len(m.GoSymbols) != 0 || m.Note != "" {
		t.Fatalf("new API was assessed instead of queued: %+v", m)
	}
	if !reflect.DeepEqual(updated.Tests["Sample.Tests"]["Example.Tests.Cases"]["Run"], original.Tests["Sample.Tests"]["Example.Tests.Cases"]["Run"]) {
		t.Fatal("update changed an existing test pair")
	}
	for owner, names := range map[string][]string{
		"Example.Tests.Cases":            {"Theory"},
		"Example.Tests.Outer`1+Nested`1": {"Generic", "Retry"},
	} {
		for _, name := range names {
			target, present := updated.Tests["Sample.Tests"][owner][name]
			if !present || target != nil || !bytes.Contains(data, []byte(fmt.Sprintf("%q: null", name))) {
				t.Fatalf("new test %s.%s must be an explicit null entry", owner, name)
			}
		}
	}
	projected, err := updated.Declarations()
	if err != nil || !reflect.DeepEqual(projected.Types, inv.Types) || !reflect.DeepEqual(projected.Assemblies, inv.Assemblies) || !reflect.DeepEqual(projected.Tests, inv.Tests) || projected.SHA256 != inv.SHA256 {
		t.Fatalf("updated declaration identities or provenance differ from extraction: %v, got %+v, want %+v", err, projected, inv)
	}
	if err := run(args, &out, &diagnostics); err != nil || out.Len() != 0 {
		t.Fatalf("repeated update = %v, stdout %q", err, out.String())
	}
	again, err := os.ReadFile(mapping)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("repeated update was not byte-idempotent: %v", err)
	}
}

func TestInputInventory(t *testing.T) {
	assembly := writeCommandFile(t, "Sample.dll", testAssembly(t))
	tests := writeCommandFile(t, "Tests.dll", testAssemblyWithTests(t, testAssemblyOptions{}))
	var extracted bytes.Buffer
	if err := run([]string{"-assembly", assembly, "-test-assembly", tests}, &extracted, io.Discard); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, extracted.Bytes()); err != nil {
		t.Fatal(err)
	}
	cached := []byte(" \n" + compact.String() + "\n")
	input := writeCommandFile(t, "input.json", cached)
	mapping := writeCommandFile(t, "mapping.json", []byte(testLegacyMapping))
	// Cached input must not invoke external commands or resolve/download latest.
	t.Setenv("PATH", t.TempDir())
	transport := http.DefaultTransport
	http.DefaultTransport = testTransport(func(request *http.Request) (*http.Response, error) {
		t.Errorf("cached input attempted an HTTP request: %s", request.URL)
		return nil, errors.New("network is unavailable")
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	var out, diagnostics bytes.Buffer
	args := []string{"-input", input}
	if err := run(args, &out, &diagnostics); err != nil || !bytes.Equal(out.Bytes(), extracted.Bytes()) || diagnostics.Len() != 0 {
		t.Fatalf("cached stdout = %v, output %q, diagnostics %q; want normalized local extraction", err, out.String(), diagnostics.String())
	}
	unchanged, err := os.ReadFile(mapping)
	if err != nil || string(unchanged) != testLegacyMapping {
		t.Fatalf("cached stdout mode changed the catalog: %v", err)
	}
	args = append(args, "-update-mapping", mapping)
	out.Reset()
	if err := run(args, &out, &diagnostics); err != nil || out.Len() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("cached update = %v, stdout %q, diagnostics %q", err, out.String(), diagnostics.String())
	}
	data, err := os.ReadFile(mapping)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := symbolcatalog.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Dotnet == nil || updated.Dotnet.SHA256 != fmt.Sprintf("%x", sha256.Sum256(cached)) {
		t.Fatalf("cached update did not retain raw extraction provenance: %+v", updated.Dotnet)
	}
	if err := run(args, &out, &diagnostics); err != nil || out.Len() != 0 {
		t.Fatalf("repeated cached update = %v, stdout %q", err, out.String())
	}
	again, err := os.ReadFile(mapping)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("repeated cached update was not byte-idempotent: %v", err)
	}
	source, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(source, cached) {
		t.Fatalf("cached input was modified: %v", err)
	}
}

func TestInputFlagExclusions(t *testing.T) {
	mapping := writeCommandFile(t, "mapping.json", []byte(testLegacyMapping))
	input := filepath.Join(t.TempDir(), "missing.json")
	for _, test := range []struct {
		name string
		args []string
	}{
		{"assembly", []string{"-assembly", "missing.dll"}},
		{"test-assembly", []string{"-test-assembly", "missing.dll"}},
		{"release", []string{"-release", "latest"}},
		{"package", []string{"-package", "Sample"}},
		{"framework", []string{"-framework", "net8.0"}},
		{"nuget-source", []string{"-nuget-source", defaultNuGetSource}},
		{"namespace", []string{"-namespace", "Example"}},
		{"include-protected", []string{"-include-protected"}},
		{"include-protected=false", []string{"-include-protected=false"}},
	} {
		for _, mode := range []struct {
			name string
			args []string
		}{{"stdout", nil}, {"update", []string{"-update-mapping", mapping}}} {
			t.Run(mode.name+"/"+test.name, func(t *testing.T) {
				args := append([]string{"-input", input}, test.args...)
				args = append(args, mode.args...)
				var out, diagnostics bytes.Buffer
				err := run(args, &out, &diagnostics)
				if err == nil || !strings.Contains(err.Error(), "-input cannot be combined with -") || out.Len() != 0 {
					t.Fatalf("explicit extraction flag was not rejected before reading input: %v, stdout %q", err, out.String())
				}
				data, err := os.ReadFile(mapping)
				if err != nil || string(data) != testLegacyMapping {
					t.Fatalf("conflicting flags changed the catalog: %v", err)
				}
			})
		}
	}
}

func TestInvalidCachedInput(t *testing.T) {
	assembly := writeCommandFile(t, "Sample.dll", testAssembly(t))
	tests := writeCommandFile(t, "Tests.dll", testAssemblyWithTests(t, testAssemblyOptions{}))
	var valid bytes.Buffer
	if err := run([]string{"-assembly", assembly, "-test-assembly", tests}, &valid, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := symbolcatalog.DecodeInventory(valid.Bytes()); err != nil {
		t.Fatalf("invalid positive control: %v", err)
	}
	for _, test := range []struct {
		name, input, want string
	}{
		{"empty", "", "single JSON object"},
		{"malformed", "{", ""},
		{"duplicate key", strings.Replace(valid.String(), `"schema_version": 1`, `"schema_version": 1, "schema_version": 1`, 1), "duplicate JSON key"},
		{"schema", strings.Replace(valid.String(), `"schema_version": 1`, `"schema_version": 2`, 1), "unsupported inventory schema_version"},
		{"identity", strings.Replace(valid.String(), "ecma335-v1", "unsupported", 1), "unsupported inventory identity_format"},
		{"unknown assembly", strings.Replace(valid.String(), `"assembly": "Sample"`, `"assembly": "Missing"`, 1), "unknown assembly"},
		{"missing API assembly hash", strings.Replace(valid.String(), fmt.Sprintf(`"sha256": "%x",`, sha256.Sum256(testAssembly(t))), "", 1), "API assembly SHA256"},
		{"invalid API assembly hash", strings.Replace(valid.String(), fmt.Sprintf("%x", sha256.Sum256(testAssembly(t))), strings.Repeat("g", 64), 1), "API assembly SHA256"},
		{"duplicate test name", strings.Replace(valid.String(), `"Theory"`, `"Run"`, 1), "duplicate test name"},
		{"test signature", strings.Replace(valid.String(), `"Theory"`, `"Theory()"`, 1), "invalid test name"},
		{"unknown test field", strings.Replace(valid.String(), `"identity_format": "test-name-v1"`, `"identity_format": "test-name-v1", "unknown": true`, 1), "unknown field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := writeCommandFile(t, "input.json", []byte(test.input))
			mapping := writeCommandFile(t, "mapping.json", []byte(testLegacyMapping))
			for _, update := range []bool{false, true} {
				args := []string{"-input", input}
				if update {
					args = append(args, "-update-mapping", mapping)
				}
				var out, diagnostics bytes.Buffer
				err := run(args, &out, &diagnostics)
				if err == nil || test.want != "" && !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
					t.Fatalf("invalid cached input (update=%v) = %v, stdout %q; want %q", update, err, out.String(), test.want)
				}
				data, err := os.ReadFile(mapping)
				if err != nil || string(data) != testLegacyMapping {
					t.Fatalf("invalid cached input changed the catalog: %v", err)
				}
			}
		})
	}
}

func TestUpdateMappingFailures(t *testing.T) {
	assembly := writeCommandFile(t, "Sample.dll", testAssembly(t))
	badAssembly := writeCommandFile(t, "bad.dll", []byte("not a managed assembly"))
	badTests := writeCommandFile(t, "Tests.dll", testAssemblyWithTests(t, testAssemblyOptions{fact: "Other.FactAttribute"}))
	for _, test := range []struct {
		name    string
		args    []string
		catalog string
		want    string
	}{
		{"corrupt assembly", []string{"-assembly", assembly, "-assembly", badAssembly}, testLegacyMapping, "PE:"},
		{"invalid test extraction", []string{"-assembly", assembly, "-test-assembly", badTests}, testLegacyMapping, "unsupported test attribute"},
		{"empty API selection", []string{"-assembly", assembly, "-namespace", "Not.Selected"}, testLegacyMapping, "assemblies and types must not be empty"},
		{"missing cached input", []string{"-input", filepath.Join(t.TempDir(), "missing.json")}, testLegacyMapping, "missing.json"},
		{"malformed catalog", []string{"-assembly", assembly}, "{", ""},
		{"duplicate catalog key", []string{"-assembly", assembly}, strings.Replace(testLegacyMapping, `"schema_version":0`, `"schema_version":0,"schema_version":0`, 1), "duplicate JSON key"},
		{"invalid assessment", []string{"-assembly", assembly}, strings.Replace(testLegacyMapping, `"status":"adapted"`, `"status":"invalid"`, 1), "invalid status"},
		{"duplicate test target", []string{"-assembly", assembly}, strings.Replace(testLegacyMapping, `"Run":"agent.TestRun"`, `"Run":"agent.TestRun","Other":"agent.TestRun"`, 1), "already mapped"},
		{"obsolete reviews", []string{"-assembly", assembly}, strings.Replace(testLegacyMapping, `"namespaces":`, `"reviews":{},"namespaces":`, 1), `unknown field "reviews"`},
		{"obsolete Go queue", []string{"-assembly", assembly}, strings.Replace(testLegacyMapping, `"namespaces":`, `"go_tests":{"unreviewed":[]},"namespaces":`, 1), `unknown field "go_tests"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			mapping := writeCommandFile(t, "mapping.json", []byte(test.catalog))
			args := append(slices.Clone(test.args), "-update-mapping", mapping)
			var out, diagnostics bytes.Buffer
			err := run(args, &out, &diagnostics)
			if err == nil || test.want != "" && !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
				t.Fatalf("failed update = %v, stdout %q; want %q", err, out.String(), test.want)
			}
			data, err := os.ReadFile(mapping)
			if err != nil || string(data) != test.catalog {
				t.Fatalf("failed update changed the destination: %v", err)
			}
		})
	}
	t.Run("requires existing catalog", func(t *testing.T) {
		mapping := filepath.Join(t.TempDir(), "missing.json")
		var out, diagnostics bytes.Buffer
		err := run([]string{"-assembly", assembly, "-update-mapping", mapping}, &out, &diagnostics)
		if !errors.Is(err, os.ErrNotExist) || out.Len() != 0 {
			t.Fatalf("missing destination = %v, stdout %q", err, out.String())
		}
		if _, err := os.Stat(mapping); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("update created a missing destination: %v", err)
		}
	})
}

func TestTestAssemblySourceCommit(t *testing.T) {
	for _, test := range []struct{ name, version, want string }{
		{"SHA1", "1.0.0+" + strings.Repeat("a", 40), strings.Repeat("a", 40)},
		{"SHA256", "1.0.0+" + strings.Repeat("b", 64), strings.Repeat("b", 64)},
		{"uppercase", "1.0.0+" + strings.Repeat("A", 40), strings.Repeat("a", 40)},
		{"no attribute", "", ""},
		{"version only", "1.0.0", ""},
		{"bare commit", strings.Repeat("a", 40), ""},
		{"abbreviated commit", "1.0.0+0c9944cc", ""},
		{"nonhex metadata", "1.0.0+" + strings.Repeat("g", 40), ""},
		{"extra metadata", "1.0.0+" + strings.Repeat("a", 40) + ".dirty", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := testAssemblyWithTests(t, testAssemblyOptions{informationalVersion: new(test.version)})
			_, assembly, err := extractTestAssemblyBytes(data)
			if err != nil {
				t.Fatal(err)
			}
			if assembly.Commit != test.want {
				t.Fatalf("source commit = %q, want %q", assembly.Commit, test.want)
			}
			encoded, err := json.Marshal(assembly)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(`"commit"`)) != (test.want != "") {
				t.Fatalf("unknown source commit must be omitted: %s", encoded)
			}
		})
	}
}

func TestInvalidTestAssemblyMetadata(t *testing.T) {
	// Establish that the fixture is readable before testing malformed variants.
	if _, _, err := extractTestAssemblyBytes(testAssemblyWithTests(t, testAssemblyOptions{})); err != nil {
		t.Fatal(err)
	}
	// Signatures are irrelevant to discovery by name, even if the metadata
	// reader cannot decode a selected method's signature.
	if _, _, err := extractTestAssemblyBytes(testAssemblyWithTests(t, testAssemblyOptions{badSignature: true})); err != nil {
		t.Fatalf("name discovery decoded an unnecessary test signature: %v", err)
	}
	for _, test := range []struct {
		name    string
		options testAssemblyOptions
		want    string
	}{
		{"reference assembly", testAssemblyOptions{reference: true}, "reference assembly"},
		{"unknown marker", testAssemblyOptions{fact: "Xunit.ConditionalFactAttribute"}, "unsupported test attribute"},
		{"lookalike marker", testAssemblyOptions{fact: "Unrelated.FactAttribute"}, "unsupported test attribute"},
		{"multiple markers", testAssemblyOptions{multiple: true}, "multiple test attributes"},
		{"duplicate identity", testAssemblyOptions{duplicate: true}, "duplicate test name"},
		{"overloaded name", testAssemblyOptions{overload: true}, "cannot distinguish overloads"},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, got, err := extractTestAssemblyBytes(testAssemblyWithTests(t, test.options))
			if err == nil || !strings.Contains(err.Error(), test.want) || name != "" || got.Types != nil {
				t.Fatalf("extractTestAssemblyBytes() = %q, %+v, %v; want %q and no partial result", name, got, err, test.want)
			}
		})
	}
}

func TestLatestStableRelease(t *testing.T) {
	for _, test := range []struct {
		name     string
		versions string
		want     string
		wantErr  string
	}{
		{"numeric order", `{"versions":["1.9.0","1.10.0-rc.1","1.10.0","1.9.9"]}`, "1.10.0", ""},
		{"no stable release", `{"versions":["1.10.0-preview.1"]}`, "", "no stable version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: testTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != "https://example.test/flat/microsoft.agents.ai/index.json" {
					t.Errorf("unexpected request: %s", request.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(test.versions)),
					ContentLength: int64(len(test.versions)), Header: make(http.Header),
				}, nil
			})}
			got, err := latestStableRelease(client, "https://example.test/flat/")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("latestStableRelease() = %q, %v; want %q", got, err, test.wantErr)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("latestStableRelease() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestDownloadPackageDataLimit(t *testing.T) {
	for _, test := range []struct {
		name          string
		contentLength int64
		body          string
		wantErr       string
	}{
		{"at limit", 5, "abcde", ""},
		{"declared oversize", 6, "abcdef", "content exceeds"},
		{"streamed oversize", -1, "abcdef", "content exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(test.body)),
					ContentLength: test.contentLength, Header: make(http.Header),
				}, nil
			})}
			got, err := downloadPackageData(client, "https://example.test/package.nupkg", 5)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("download = %q, %v; want %q", got, err, test.wantErr)
				}
			} else if err != nil || string(got) != test.body {
				t.Fatalf("download = %q, %v; want %q", got, err, test.body)
			}
		})
	}
}

func TestDownloadCloseError(t *testing.T) {
	want := errors.New("response body close failed")
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Body: testBody{Reader: strings.NewReader("ok"), closeErr: want},
			ContentLength: 2, Header: make(http.Header),
		}, nil
	})}
	data, err := downloadPackageData(client, "https://example.test/package.nupkg", 5)
	if !errors.Is(err, want) || len(data) != 0 {
		t.Fatalf("close response = %q, %v; want close error and no data", data, err)
	}
}

func TestPackageArchive(t *testing.T) {
	manifest := []byte(`<package><metadata><id>Sample.Package</id><version>1.2.3</version></metadata></package>`)
	assembly := testAssembly(t)
	valid := []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net8.0/Sample.dll", assembly}, {"lib/net8.0/Bad.dll", []byte("bad")}}
	for _, test := range []struct {
		name    string
		entries []testEntry
		wantErr string
	}{
		{"missing manifest", nil, "no root package manifest"},
		{"duplicate manifests", []testEntry{{"first.nuspec", manifest}, {"second.nuspec", manifest}}, "multiple root package manifests"},
		{"wrong identity", []testEntry{{"Sample.Package.nuspec", []byte(`<package><metadata><id>Other</id><version>1.2.3</version></metadata></package>`)}}, "does not match requested"},
		{"no exact framework", []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net9.0/Sample.dll", assembly}}, "no exact assets"},
		{"no managed DLL", []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net8.0/readme.txt", []byte("text")}}, "contains no managed DLLs"},
		{"invalid assembly", []testEntry{{"Sample.Package.nuspec", manifest}, {"ref/net8.0/Bad.dll", []byte("bad")}}, "PE:"},
		{"reference asset preferred", valid, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := testArchive(t, test.entries...)
			result := symbolcatalog.Inventory{
				Selection: symbolcatalog.Selection{}, Packages: make(map[string]symbolcatalog.Package),
				Assemblies: make(map[string]symbolcatalog.Assembly), Types: make(map[string]symbolcatalog.Declaration),
			}
			err := addPackage(&result, packageRequest{ID: "sample.package", Version: "1.2.3"}, "net8.0", "https://example.test/index.json", "https://example.test/Sample.Package.nupkg", archive)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("addPackage = %v; want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info, ok := result.Packages["Sample.Package"]
			if !ok || info.AssetGroup != "ref/net8.0" || !slices.Equal(info.Assemblies, []string{"Sample"}) || len(result.Types) != 2 {
				t.Fatalf("unexpected package inventory: %+v, types %+v", result.Packages, result.Types)
			}
		})
	}

	archive := testArchive(t, testEntry{"data.txt", []byte("abcdef")})
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readPackageEntry(zipReader.File[0], 5); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized ZIP entry: got %v, want limit error", err)
	}
	if data, err := readPackageEntry(zipReader.File[0], 6); err != nil || string(data) != "abcdef" {
		t.Fatalf("ZIP entry at limit: got %q, %v", data, err)
	}
}

const testLegacyMapping = `{
	"schema_version":0,
	"baseline":{"checked_at":"2026-09-23","dotnet_repository":"https://example.org/dotnet","dotnet_commit":"1111111111111111111111111111111111111111","go_repository":"https://example.org/go","go_commit":"2222222222222222222222222222222222222222","go_module":"example.org/sdk","inventory_complete":false,"scope":"Selected declarations."},
	"go_only":{"agent.WithSession":{"note":"Go-specific option."}},
	"tests":{"Sample.Tests":{"Example.Tests.Cases":{"Run":"agent.TestRun"}}},
	"namespaces":{"Example":{"Widget":{"area":"agents","assembly":"Sample","mapping":{"go":"agent.Config{}","go_symbols":["agent.Config"],"status":"adapted","note":"Existing type assessment."},"methods":{"Run(string)":{"go":"a.Run(ctx, text)","go_symbols":["agent.Agent.Run"],"status":"partial","note":"Existing method assessment."}}}}}
}`

func writeCommandFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

type testWriter func([]byte) (int, error)

func (w testWriter) Write(p []byte) (int, error) { return w(p) }

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type testBody struct {
	io.Reader
	closeErr error
}

func (body testBody) Close() error { return body.closeErr }

type testEntry struct {
	name string
	data []byte
}

func testArchive(t *testing.T, entries ...testEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, item := range entries {
		entry, err := archive.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// testAssembly builds a small managed PE with two public types.
func testAssembly(t *testing.T) []byte {
	t.Helper()
	stringsHeap := []byte{0}
	addString := func(text string) uint16 {
		index := uint16(len(stringsHeap))
		stringsHeap = append(stringsHeap, text...)
		stringsHeap = append(stringsHeap, 0)
		return index
	}
	module := addString("Sample.dll")
	moduleType := addString("<Module>")
	widget := addString("Widget")
	gadget := addString("Gadget")
	hidden := addString("Hidden")
	parent := addString("Example")
	child := addString("Example.Child")
	assembly := addString("Sample")

	var rows bytes.Buffer
	writeRow := func(fields ...any) {
		for _, field := range fields {
			if err := binary.Write(&rows, binary.LittleEndian, field); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeRow(uint16(0), module, uint16(1), uint16(0), uint16(0)) // Module.
	writeType := func(flags uint32, name, namespace uint16) {
		writeRow(flags, name, namespace, uint16(0), uint16(1), uint16(1))
	}
	writeType(0, moduleType, 0)
	writeType(1, widget, parent)
	writeType(1, gadget, child)
	writeType(0, hidden, parent)
	writeRow(uint32(0), uint16(1), uint16(2), uint16(3), uint16(4), uint32(0), uint16(0), assembly, uint16(0)) // Assembly.

	tables := make([]byte, 24)
	tables[4], tables[7] = 2, 1
	binary.LittleEndian.PutUint64(tables[8:], 1<<0|1<<2|1<<32)
	for _, count := range []uint32{1, 4, 1} {
		tables = binary.LittleEndian.AppendUint32(tables, count)
	}
	tables = append(tables, rows.Bytes()...)
	streams := []testEntry{
		{"#~", tables}, {"#Strings", stringsHeap}, {"#GUID", make([]byte, 16)}, {"#Blob", []byte{0}},
	}
	return testManagedPE(t, streams)
}

type testAssemblyOptions struct {
	fact                 string
	reference            bool
	multiple             bool
	duplicate            bool
	overload             bool
	badSignature         bool
	informationalVersion *string
}

// testAssemblyWithTests constructs metadata only. No C# compiler, executable
// test fixture, or committed DLL is needed to exercise go-winmd extraction.
func testAssemblyWithTests(t *testing.T, options testAssemblyOptions) []byte {
	t.Helper()
	stringsHeap, blobHeap := []byte{0}, []byte{0}
	addString := func(value string) uint16 {
		index := uint16(len(stringsHeap))
		stringsHeap = append(stringsHeap, value...)
		stringsHeap = append(stringsHeap, 0)
		return index
	}
	addBlob := func(value []byte) uint16 {
		if len(value) >= 128 {
			t.Fatal("fixture blob requires a multibyte length")
		}
		index := uint16(len(blobHeap))
		blobHeap = append(blobHeap, byte(len(value)))
		blobHeap = append(blobHeap, value...)
		return index
	}
	rows := make(map[int]*bytes.Buffer)
	counts := make(map[int]uint32)
	row := func(table int, fields ...any) {
		if rows[table] == nil {
			rows[table] = new(bytes.Buffer)
		}
		counts[table]++
		for _, field := range fields {
			if err := binary.Write(rows[table], binary.LittleEndian, field); err != nil {
				t.Fatal(err)
			}
		}
	}
	row(0, uint16(0), addString("Sample.Tests.dll"), uint16(1), uint16(0), uint16(0))
	if options.fact == "" {
		options.fact = "Xunit.FactAttribute"
	}
	for _, name := range []string{
		options.fact, "Xunit.TheoryAttribute", "xRetry.v3.RetryFactAttribute", "System.Threading.Tasks.Task",
		informationalVersionAttributeName, targetFrameworkAttributeName, referenceAssemblyAttributeName,
	} {
		separator := strings.LastIndexByte(name, '.')
		row(1, uint16(6), addString(name[separator+1:]), addString(name[:separator])) // AssemblyRef[1].
	}
	typ := func(flags uint32, name, namespace string, firstMethod uint16) {
		row(2, flags, addString(name), addString(namespace), uint16(0), uint16(1), firstMethod)
	}
	typ(0, "<Module>", "", 1)
	typ(0, "Cases", "Example.Tests", 1) // Nonpublic types must not be lost to API visibility filters.
	typ(0, "Outer`1", "Example.Tests", 4)
	typ(3, "Nested`1", "", 4)
	typ(1, "RetryTheoryAttribute", "xRetry.v3", 6)
	voidSig := []byte{0x20, 0, 1}
	firstSig := voidSig
	if options.badSignature {
		firstSig = []byte{0xff}
	}
	method := func(name string, signature []byte) {
		row(6, uint32(0), uint16(0), uint16(0x86), addString(name), addBlob(signature), uint16(1))
	}
	method("Run", firstSig)
	theoryName := "Theory"
	if options.overload {
		theoryName = "Run"
	}
	method(theoryName, []byte{0x20, 2, 0x12, 17, 2, 0x0e}) // Task (TypeRef[4]), bool, string.
	if options.duplicate {
		method("Run", voidSig)
	} else {
		method("Helper", []byte{0xff}) // Unattributed helper signatures need not be decoded.
	}
	method("Generic", []byte{0x30, 1, 3, 1, 0x13, 0, 0x13, 1, 0x1e, 0})
	method("Retry", voidSig)
	method(".ctor", voidSig)
	for _, reference := range []struct {
		typeRow        uint16
		stringArgument bool
	}{{1, false}, {2, false}, {3, false}, {5, true}, {6, true}, {7, false}} {
		signature := voidSig
		if reference.stringArgument {
			signature = []byte{0x20, 1, 1, 0x0e}
		}
		row(10, reference.typeRow<<3|1, addString(".ctor"), addBlob(signature))
	}
	emptyAttribute := addBlob([]byte{1, 0, 0, 0})
	attribute := func(methodRow, constructor uint16) {
		row(12, methodRow<<5, constructor, emptyAttribute)
	}
	attribute(1, 1<<3|3)
	attribute(2, 2<<3|3)
	attribute(4, 6<<3|2) // Local MethodDef constructor, not an external MemberRef.
	attribute(5, 3<<3|3)
	if options.multiple {
		attribute(1, 2<<3|3)
	}
	if options.duplicate {
		attribute(3, 1<<3|3)
	}
	version := "1.0.0+3333333333333333333333333333333333333333"
	if options.informationalVersion != nil {
		version = *options.informationalVersion
	}
	for i, value := range []string{version, ".NETCoreApp,Version=v10.0"} {
		if value == "" {
			continue
		}
		blob := append([]byte{1, 0, byte(len(value))}, value...)
		blob = append(blob, 0, 0)
		row(12, uint16(1<<5|14), uint16((i+4)<<3|3), addBlob(blob))
	}
	if options.reference {
		row(12, uint16(1<<5|14), uint16(6<<3|3), emptyAttribute)
	}
	row(32, uint32(0), uint16(1), uint16(2), uint16(3), uint16(4), uint32(0), uint16(0), addString("Sample.Tests"), uint16(0))
	row(35, uint16(1), uint16(0), uint16(0), uint16(0), uint32(0), uint16(0), addString("Fixture.Attributes"), uint16(0), uint16(0))
	row(41, uint16(4), uint16(3))
	for _, generic := range []struct {
		owner, number uint16
		name          string
	}{{6, 0, "T"}, {8, 0, "T"}, {8, 1, "U"}, {9, 0, "V"}} {
		row(42, generic.number, uint16(0), generic.owner, addString(generic.name))
	}
	tables := make([]byte, 24)
	tables[4], tables[7] = 2, 1
	var valid uint64
	for table := range 64 {
		if count := counts[table]; count != 0 {
			valid |= uint64(1) << table
			tables = binary.LittleEndian.AppendUint32(tables, count)
		}
	}
	binary.LittleEndian.PutUint64(tables[8:], valid)
	for table := range 64 {
		if rows[table] != nil {
			tables = append(tables, rows[table].Bytes()...)
		}
	}
	return testManagedPE(t, []testEntry{
		{"#~", tables}, {"#Strings", stringsHeap}, {"#GUID", make([]byte, 16)}, {"#Blob", blobHeap},
	})
}

func testManagedPE(t *testing.T, streams []testEntry) []byte {
	t.Helper()
	root := make([]byte, 16)
	binary.LittleEndian.PutUint32(root, 0x424a5342) // BSJB metadata signature.
	binary.LittleEndian.PutUint16(root[4:], 1)
	binary.LittleEndian.PutUint16(root[6:], 1)
	const version = "v4.0.30319\x00\x00"
	binary.LittleEndian.PutUint32(root[12:], uint32(len(version)))
	root = append(root, version...)
	root = binary.LittleEndian.AppendUint16(root, 0)
	root = binary.LittleEndian.AppendUint16(root, uint16(len(streams)))
	offset := len(root)
	for _, stream := range streams {
		offset += 8 + (len(stream.name)+4)&^3
	}
	for _, stream := range streams {
		root = binary.LittleEndian.AppendUint32(root, uint32(offset))
		root = binary.LittleEndian.AppendUint32(root, uint32(len(stream.data)))
		root = append(root, stream.name...)
		root = append(root, make([]byte, 4-len(stream.name)%4)...)
		offset += (len(stream.data) + 3) &^ 3
	}
	for _, stream := range streams {
		root = append(root, stream.data...)
		root = append(root, make([]byte, (4-len(stream.data)%4)%4)...)
	}

	const sectionRVA, sectionOffset, cliSize = 0x2000, 0x200, 72
	section := make([]byte, cliSize)
	binary.LittleEndian.PutUint32(section, cliSize)
	binary.LittleEndian.PutUint16(section[4:], 2)
	binary.LittleEndian.PutUint16(section[6:], 5)
	binary.LittleEndian.PutUint32(section[8:], sectionRVA+cliSize)
	binary.LittleEndian.PutUint32(section[12:], uint32(len(root)))
	section = append(section, root...)
	optional := pe.OptionalHeader32{Magic: 0x10b, NumberOfRvaAndSizes: 16}
	optional.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_COM_DESCRIPTOR] = pe.DataDirectory{VirtualAddress: sectionRVA, Size: cliSize}
	var image bytes.Buffer
	for _, header := range []any{
		pe.FileHeader{Machine: pe.IMAGE_FILE_MACHINE_I386, NumberOfSections: 1, SizeOfOptionalHeader: uint16(binary.Size(optional))},
		optional,
		pe.SectionHeader32{
			Name: [8]byte{'.', 't', 'e', 'x', 't'}, VirtualSize: uint32(len(section)), VirtualAddress: sectionRVA,
			SizeOfRawData: uint32(len(section)), PointerToRawData: sectionOffset,
		},
	} {
		if err := binary.Write(&image, binary.LittleEndian, header); err != nil {
			t.Fatal(err)
		}
	}
	if image.Len() > sectionOffset {
		t.Fatal("PE headers exceed section offset")
	}
	image.Write(make([]byte, sectionOffset-image.Len()))
	image.Write(section)
	return image.Bytes()
}
