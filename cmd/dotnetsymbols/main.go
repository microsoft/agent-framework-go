// Copyright (c) Microsoft. All rights reserved.

// dotnetsymbols reads local or published .NET assembly metadata without executing code.
// It writes a deterministic public-symbol inventory to standard output.
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
	var patterns, namespaces, packages []string
	flags.Func("assembly", "local assembly file or filepath glob; repeat for each selected input", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("assembly pattern must not be empty")
		}
		patterns = append(patterns, value)
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
		return errors.New("unexpected positional arguments; select inputs with -assembly or -release")
	}
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
	files := make(map[string]bool)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("assembly pattern %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("assembly pattern %q matched no files", pattern)
		}
		for _, name := range matches {
			absolute, err := filepath.Abs(name)
			if err != nil {
				return err
			}
			files[absolute] = true
		}
	}
	slices.Sort(namespaces)
	namespaces = slices.Compact(namespaces)
	if namespaces == nil {
		namespaces = []string{}
	}
	selected := selection{Namespaces: namespaces, IncludeProtected: *protected}
	result := inventory{
		SchemaVersion: 1, IdentityFormat: "ecma335-v1", Selection: selected,
		Assemblies: make(map[string]assemblyInfo), Types: make(map[string]typeInfo),
	}
	if releaseMode {
		if err := loadRelease(&result, releaseOptions{Version: *release, Framework: *framework, Source: *source, Packages: packages}, diagnostics); err != nil {
			return err
		}
	}
	ordered := make([]string, 0, len(files))
	for name := range files {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	for _, name := range ordered {
		assemblyName, assembly, types, err := extractAssembly(name, selected)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := result.addAssembly(assemblyName, assembly, types); err != nil {
			return err
		}
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

func (result *inventory) addAssembly(name string, assembly assemblyInfo, types map[string]typeInfo) error {
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

func includesNamespace(namespace string, selection selection) bool {
	if len(selection.Namespaces) == 0 {
		return true
	}
	return slices.ContainsFunc(selection.Namespaces, func(prefix string) bool {
		return namespace == prefix || strings.HasPrefix(namespace, prefix+".")
	})
}
