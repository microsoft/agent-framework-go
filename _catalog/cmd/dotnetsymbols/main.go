// Copyright (c) Microsoft. All rights reserved.

// dotnetsymbols reads local or published .NET assembly metadata without executing code.
// It optionally adds test declarations from selected local test assemblies
// and writes a deterministic inventory to standard output, or explicitly
// refreshes an existing catalog while preserving its Go assessments.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/symbolcatalog"
	"github.com/microsoft/agent-framework-go/_catalog/cmd/internal/testinventory"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("dotnetsymbols", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	var patterns, testPatterns, namespaces, packages []string
	var input, updateMapping string
	flags.Func("input", "validated cached extraction JSON instead of assembly or release inputs", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("input path must not be empty")
		}
		input = value
		return nil
	})
	flags.Func("update-mapping", "refresh this existing catalog in place instead of writing an inventory to standard output", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("update-mapping path must not be empty")
		}
		updateMapping = value
		return nil
	})
	flags.Func("assembly", "local assembly file or filepath glob; repeat for each selected input", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("assembly pattern must not be empty")
		}
		patterns = append(patterns, value)
		return nil
	})
	flags.Func("test-assembly", "local implementation assembly containing xUnit tests; repeat for each file or filepath glob", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("test assembly pattern must not be empty")
		}
		testPatterns = append(testPatterns, value)
		return nil
	})
	flags.Func("namespace", "include this namespace and its children; repeat to select several (default: all)", func(value string) error {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.HasSuffix(value, ".") {
			return errors.New("namespace must be a nonempty namespace name without a trailing dot")
		}
		namespaces = append(namespaces, value)
		return nil
	})
	release := flags.String("release", "latest", "latest stable MAF release or an exact NuGet version, optionally prefixed with dotnet-")
	framework := flags.String("framework", "net8.0", "exact NuGet target framework for -release; no compatibility fallback")
	source := flags.String("nuget-source", defaultNuGetSource, "public NuGet v3 service index for -release")
	flags.Func("package", "package ID or ID@version for -release; repeat to replace the default core package set", func(value string) error {
		packages = append(packages, value)
		return nil
	})
	protected := flags.Bool("include-protected", false, "also include protected and protected-internal API, but not private-protected API")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments; select inputs with -assembly, -release, or -input")
	}
	var result symbolcatalog.Inventory
	if input != "" {
		var extractionFlag string
		flags.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "assembly", "test-assembly", "release", "package", "framework", "nuget-source", "namespace", "include-protected":
				extractionFlag = f.Name
			}
		})
		if extractionFlag != "" {
			return fmt.Errorf("-input cannot be combined with -%s", extractionFlag)
		}
		data, err := os.ReadFile(input)
		if err != nil {
			return fmt.Errorf("%s: %w", input, err)
		}
		decoded, err := symbolcatalog.DecodeInventory(data)
		if err != nil {
			return fmt.Errorf("%s: %w", input, err)
		}
		result = decoded
	} else {
		releaseMode := len(patterns) == 0
		if !releaseMode {
			var releaseFlag string
			flags.Visit(func(f *flag.Flag) {
				if f.Name == "release" || f.Name == "package" || f.Name == "framework" || f.Name == "nuget-source" {
					releaseFlag = f.Name
				}
			})
			if releaseFlag != "" {
				return fmt.Errorf("-assembly cannot be combined with -%s", releaseFlag)
			}
		}
		files, err := assemblyFiles(patterns)
		if err != nil {
			return err
		}
		testFiles, err := assemblyFiles(testPatterns)
		if err != nil {
			return fmt.Errorf("test assemblies: %w", err)
		}
		slices.Sort(namespaces)
		namespaces = slices.Compact(namespaces)
		if namespaces == nil {
			namespaces = []string{}
		}
		selected := symbolcatalog.Selection{Namespaces: namespaces, IncludeProtected: *protected}
		result = symbolcatalog.Inventory{
			SchemaVersion: 1, IdentityFormat: "ecma335-v1", Selection: selected,
			Assemblies: make(map[string]symbolcatalog.Assembly), Types: make(map[string]symbolcatalog.Declaration),
		}
		if releaseMode {
			if err := loadRelease(&result, releaseOptions{Version: *release, Framework: *framework, Source: *source, Packages: packages}, diagnostics); err != nil {
				return err
			}
		}
		for _, name := range files {
			assemblyName, assembly, types, err := extractAssembly(name, selected)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if err := addAssembly(&result, assemblyName, assembly, types); err != nil {
				return err
			}
		}
		if len(testFiles) != 0 {
			result.Tests = &testinventory.Inventory{
				IdentityFormat: testinventory.IdentityFormat,
				Assemblies:     make(map[string]testinventory.Assembly),
			}
			for _, file := range testFiles {
				name, assembly, err := extractTestAssembly(file)
				if err != nil {
					return fmt.Errorf("extract test declarations from %s: %w", file, err)
				}
				if previous, exists := result.Tests.Assemblies[name]; exists && previous.SHA256 != assembly.SHA256 {
					return fmt.Errorf("multiple different inputs for test assembly %q; select one build configuration", name)
				}
				result.Tests.Assemblies[name] = assembly
			}
		}
	}
	if updateMapping != "" {
		return symbolcatalog.Update(updateMapping, result)
	}
	// Buffer the full report so decoding/encoding errors cannot produce a
	// successful-looking partial inventory on standard output.
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return err
	}
	n, err := out.Write(buffer.Bytes())
	if err == nil && n != buffer.Len() {
		err = io.ErrShortWrite
	}
	return err
}

func assemblyFiles(patterns []string) ([]string, error) {
	files := make(map[string]bool)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("assembly pattern %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("assembly pattern %q matched no files", pattern)
		}
		for _, name := range matches {
			absolute, err := filepath.Abs(name)
			if err != nil {
				return nil, err
			}
			files[absolute] = true
		}
	}
	ordered := make([]string, 0, len(files))
	for name := range files {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	return ordered, nil
}

func addAssembly(result *symbolcatalog.Inventory, name string, assembly symbolcatalog.Assembly, types map[string]symbolcatalog.Declaration) error {
	if previous, exists := result.Assemblies[name]; exists {
		if previous.SHA256 == assembly.SHA256 {
			return nil
		}
		return fmt.Errorf("multiple different inputs for assembly %q; select one version and target framework", name)
	}
	result.Assemblies[name] = assembly
	for typeName, typ := range types {
		if existing, exists := result.Types[typeName]; exists {
			return fmt.Errorf("type %q is declared by both %q and %q", typeName, existing.Assembly, name)
		}
		result.Types[typeName] = typ
	}
	return nil
}

func includesNamespace(namespace string, selection symbolcatalog.Selection) bool {
	if len(selection.Namespaces) == 0 {
		return true
	}
	return slices.ContainsFunc(selection.Namespaces, func(prefix string) bool {
		return namespace == prefix || strings.HasPrefix(namespace, prefix+".")
	})
}
