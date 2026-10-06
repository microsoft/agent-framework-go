// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/mod/modfile"
)

const goTestDiscoveryScope = "static Test declarations in all *_test.go build variants; excludes nested modules, dot/underscore-prefixed names, vendor, testdata, and symlinks; no compilation or execution"

type goTestMetadata struct {
	Module        string `json:"module"`
	Commit        string `json:"commit"`
	Dirty         *bool  `json:"dirty"`
	Scope         string `json:"scope"`
	TestFunctions int    `json:"test_functions"`
}

type goTestInventory struct {
	goTestMetadata
	Tests []string  `json:"tests"` // Sorted, unique directory-qualified names.
	Page  *pageInfo `json:"page,omitempty"`
}

// indexGoTests parses source without invoking Go, loading dependencies, or
// evaluating build constraints. A key denotes presence in at least one source
// variant, not runtime uniqueness or runnability. Internal and external test
// packages intentionally share their directory identity. The root directory
// is ".", so a root test has a key such as "..TestRoot".
func indexGoTests(root string) (goTestInventory, error) {
	dir, err := filepath.Abs(root)
	if err != nil {
		return goTestInventory{}, fmt.Errorf("index Go tests root %q: %w", root, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return goTestInventory{}, fmt.Errorf("index Go tests root %q: %w", dir, err)
	}
	if !info.IsDir() {
		return goTestInventory{}, fmt.Errorf("index Go tests root %q is not a directory", dir)
	}
	moduleFile := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(moduleFile)
	if err != nil {
		return goTestInventory{}, fmt.Errorf("read Go test module: %w", err)
	}
	module, err := modfile.Parse(moduleFile, data, nil)
	if err != nil {
		return goTestInventory{}, fmt.Errorf("parse Go test module: %w", err)
	}
	if module.Module == nil || !modulePattern.MatchString(module.Module.Mod.Path) {
		return goTestInventory{}, errors.New("go test root must contain a valid module directive")
	}
	index := goTestInventory{
		goTestMetadata: goTestMetadata{Module: module.Module.Mod.Path, Scope: goTestDiscoveryScope},
		Tests:          make([]string, 0),
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(dir, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if file != dir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if file == dir {
				return nil
			}
			if name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			// A nested module is outside this index even if its go.mod cannot
			// be parsed. Do not attribute its declarations to the parent module.
			if _, err := os.Stat(filepath.Join(file, "go.mod")); err == nil {
				return filepath.SkipDir
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("test source %q is not a regular file", file)
		}
		source, err := parser.ParseFile(fset, file, nil, parser.AllErrors|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse test source %q: %w", file, err)
		}
		imports := make(map[string]bool)
		for _, spec := range source.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("read test import in %q: %w", file, err)
			}
			if importPath == "testing" {
				alias := "testing"
				if spec.Name != nil {
					alias = spec.Name.Name
				}
				if alias != "_" {
					imports[alias] = true
				}
			}
		}
		for _, declaration := range source.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || !goTestEntryPoint(fn, imports) {
				continue
			}
			relative, err := filepath.Rel(dir, filepath.Dir(file))
			if err != nil {
				return err
			}
			key := filepath.ToSlash(relative) + "." + fn.Name.Name
			if !validGoTestTarget(key) {
				return fmt.Errorf("test source %q has an invalid module-relative identity %q", file, key)
			}
			index.Tests = append(index.Tests, key)
		}
		return nil
	})
	if err != nil {
		return goTestInventory{}, fmt.Errorf("index Go test source: %w", err)
	}
	slices.Sort(index.Tests)
	index.Tests = slices.Compact(index.Tests)
	index.TestFunctions = len(index.Tests)
	index.Commit, index.Dirty, err = goIndexGit(dir, os.Environ())
	if err != nil {
		return goTestInventory{}, err
	}
	return index, nil
}

func goTestEntryPoint(fn *ast.FuncDecl, testingImports map[string]bool) bool {
	if !goTestName(fn.Name.Name) || fn.Recv != nil ||
		fn.Type.TypeParams != nil && fn.Type.TypeParams.NumFields() != 0 ||
		fn.Type.Results != nil && fn.Type.Results.NumFields() != 0 ||
		fn.Type.Params == nil || fn.Type.Params.NumFields() != 1 || len(fn.Type.Params.List) != 1 {
		return false
	}
	ptr, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch typ := ptr.X.(type) {
	case *ast.Ident:
		return typ.Name == "T" && testingImports["."]
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		return ok && typ.Sel.Name == "T" && testingImports[pkg.Name]
	default:
		return false
	}
}

func goTestName(name string) bool {
	if !token.IsIdentifier(name) || !strings.HasPrefix(name, "Test") {
		return false
	}
	if name == "Test" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return !unicode.IsLower(r)
}

func validGoTestTarget(target string) bool {
	separator := strings.LastIndexByte(target, '.')
	if separator < 1 || !goTestName(target[separator+1:]) {
		return false
	}
	dir := target[:separator]
	return fs.ValidPath(dir) && !strings.ContainsAny(dir, "\\:") &&
		!strings.ContainsFunc(dir, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func writeGoTests(out io.Writer, index goTestInventory, symbol string, paging pageOptions, asJSON, brief bool) error {
	total := index.TestFunctions
	selected := make([]string, 0)
	for _, name := range index.Tests {
		if strings.Contains(strings.ToLower(name), strings.ToLower(symbol)) {
			selected = append(selected, name)
		}
	}
	index.TestFunctions = len(selected)
	if asJSON && brief {
		return writeReportJSON(out, index.goTestMetadata)
	}
	visible, page := pageItems(selected, paging)
	index.Tests, index.Page = visible, &page
	if asJSON {
		return writeReportJSON(out, index)
	}
	var buffer bytes.Buffer
	writeGoTestContext(&buffer, index.goTestMetadata)
	fmt.Fprintf(&buffer, "Test functions: %d selected of %d discovered directory-qualified names. Counts are not test-case coverage.\n", len(selected), total)
	if !brief {
		if err := writePageText(&buffer, page); err != nil {
			return err
		}
		fmt.Fprintln(&buffer, "\nGO TEST")
		for _, name := range visible {
			fmt.Fprintln(&buffer, reportCell(name))
		}
	}
	_, err := buffer.WriteTo(out)
	return err
}

func writeGoTestContext(out *bytes.Buffer, metadata goTestMetadata) {
	dirty := "unknown"
	if metadata.Dirty != nil {
		dirty = fmt.Sprint(*metadata.Dirty)
	}
	fmt.Fprintf(out, "Go test source: module %s; commit %s; dirty %s.\n", reportCell(metadata.Module), reportCell(metadata.Commit), dirty)
	fmt.Fprintf(out, "Discovery scope: %s.\n", reportCell(metadata.Scope))
	fmt.Fprintln(out, "Presence means at least one source declaration, not buildability, runtime uniqueness, or a passing test.")
}
