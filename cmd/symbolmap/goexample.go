// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"go/version"
	"maps"
	"slices"
	"strconv"
	"strings"
)

var goExampleStandardImports = map[string]string{
	"context": "context",
	"fs":      "io/fs",
	"http":    "net/http",
	"iter":    "iter",
	"json":    "encoding/json",
	"reflect": "reflect",
	"slices":  "slices",
	"slog":    "log/slog",
}

var goExampleVariables = map[string]bool{
	"agentConfig": true,
	"builder":     true,
	"client":      true,
	"decoder":     true,
	"edge":        true,
	"env":         true,
	"evaluator":   true,
	"event":       true,
	"factory":     true,
	"format":      true,
	"frontmatter": true,
	"group":       true,
	"index":       true,
	"local":       true,
	"manager":     true,
	"msg":         true,
	"options":     true,
	"policy":      true,
	"provider":    true,
	"queue":       true,
	"request":     true,
	"resource":    true,
	"response":    true,
	"result":      true,
	"run":         true,
	"runner":      true,
	"script":      true,
	"session":     true,
	"skill":       true,
	"source":      true,
	"state":       true,
	"strategy":    true,
	"stream":      true,
	"streamRun":   true,
	"tag":         true,
	"value":       true,
	"workflow":    true,
}

// parseGoExample supplies only the boilerplate needed to parse declarations,
// expressions, or statement fragments. It does not supply example inputs.
func parseGoExample(code string) (*token.FileSet, *ast.File, error) {
	return parseGoExampleMode(code, false)
}

func parseGoExampleMode(code string, callStatement bool) (*token.FileSet, *ast.File, error) {
	if strings.TrimSpace(code) == "" {
		return nil, nil, fmt.Errorf("go example must not be empty")
	}
	parse := func(body string) (*token.FileSet, *ast.File, error) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "example.go", "package example\n"+body+"\n", parser.AllErrors)
		return fset, file, err
	}
	fset, file, err := parse(code)
	if err == nil {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				if decl.Tok == token.IMPORT {
					return nil, nil, fmt.Errorf("go examples must not declare imports")
				}
			case *ast.FuncDecl:
				if decl.Recv == nil && decl.Name.Name == "init" {
					return nil, nil, fmt.Errorf("go examples must not declare init functions")
				}
			}
		}
		if len(file.Decls) == 0 {
			return nil, nil, fmt.Errorf("go example must contain code, not only comments")
		}
		return fset, file, nil
	}
	if expression, err := parser.ParseExpr(code); err == nil {
		if _, ok := expression.(*ast.CallExpr); ok && callStatement {
			return parse("func example() {\n" + code + "\n}")
		}
		return parse("var _ = " + code)
	}
	return parse("func example() {\n" + code + "\n}")
}

// checkGoExample type-checks without evaluating source or loading packages.
// Imports must already be present in the indexed package graph.
func checkGoExample(code string, api goInventory) error {
	err := checkGoExampleMode(code, api, false)
	if err != nil && strings.Contains(err.Error(), "multiple-value") && strings.Contains(err.Error(), "in single-value context") {
		return checkGoExampleMode(code, api, true)
	}
	return err
}

func checkGoExampleMode(code string, api goInventory, callStatement bool) error {
	fset, file, err := parseGoExampleMode(code, callStatement)
	if err != nil {
		return err
	}

	// The parser leaves package qualifiers unresolved, but binds parameters
	// and locals that shadow catalog aliases. Bare identifiers are not imports.
	used := make(map[string]map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if base, ok := selector.X.(*ast.Ident); ok && base.Obj == nil {
			if used[base.Name] == nil {
				used[base.Name] = make(map[string]bool)
			}
			used[base.Name][selector.Sel.Name] = true
		}
		return true
	})
	imports, err := resolveGoExampleImports(used, api.packages)
	if err != nil {
		return err
	}

	if len(imports) != 0 {
		declaration := &ast.GenDecl{Tok: token.IMPORT}
		for _, alias := range slices.Sorted(maps.Keys(imports)) {
			spec := &ast.ImportSpec{
				Name: ast.NewIdent(alias),
				Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(imports[alias])},
			}
			declaration.Specs = append(declaration.Specs, spec)
			file.Imports = append(file.Imports, spec)
		}
		file.Decls = append([]ast.Decl{declaration}, file.Decls...)

		// Give synthesized imports real positions for example.go diagnostics.
		var source bytes.Buffer
		if err := format.Node(&source, fset, file); err != nil {
			return fmt.Errorf("format Go example: %w", err)
		}
		fset = token.NewFileSet()
		file, err = parser.ParseFile(fset, "example.go", source.Bytes(), parser.AllErrors)
		if err != nil {
			return err
		}
	}

	var checkErr error
	config := types.Config{
		Importer:  goExampleImporter(api.packages),
		GoVersion: version.Lang(api.GoVersion),
		Sizes:     types.SizesFor("gc", api.GOARCH),
		Error: func(err error) {
			var typeErr types.Error
			if errors.As(err, &typeErr) && strings.HasPrefix(typeErr.Msg, "undefined: ") {
				return
			}
			if checkErr == nil {
				checkErr = err
			}
		},
	}
	_, _ = config.Check("example", fset, []*ast.File{file}, nil)
	return checkErr
}

func resolveGoExampleImports(used map[string]map[string]bool, packages map[string]*types.Package) (map[string]string, error) {
	imports := make(map[string]string, len(used))
	for alias, names := range used {
		if importPath := goExampleStandardImports[alias]; importPath != "" {
			pkg := packages[importPath]
			if pkg == nil {
				return nil, fmt.Errorf("resolve Go example import %q: preferred package %q is not indexed", alias, importPath)
			}
			for name := range names {
				if object := pkg.Scope().Lookup(name); object == nil || !object.Exported() {
					return nil, fmt.Errorf("resolve Go example import %q: package %q has no exported %s", alias, importPath, name)
				}
			}
			imports[alias] = importPath
			continue
		}
		var namedPackages []string
		for importPath, pkg := range packages {
			if pkg.Name() == alias {
				namedPackages = append(namedPackages, importPath)
			}
		}
		if len(namedPackages) == 0 {
			// Selector bases that are not package names are example inputs.
			continue
		}
		var candidates []string
		for _, importPath := range namedPackages {
			pkg := packages[importPath]
			matches := true
			for name := range names {
				if object := pkg.Scope().Lookup(name); object == nil || !object.Exported() {
					matches = false
					break
				}
			}
			if matches {
				candidates = append(candidates, importPath)
			}
		}
		slices.Sort(candidates)
		if len(candidates) > 1 {
			public := slices.DeleteFunc(slices.Clone(candidates), func(importPath string) bool {
				return slices.Contains(strings.Split(importPath, "/"), "internal")
			})
			if len(public) == 1 {
				candidates = public
			}
		}
		if len(candidates) == 0 && goExampleVariables[alias] {
			continue
		}
		if len(candidates) != 1 {
			return nil, fmt.Errorf("resolve Go example import %q for %v: found %d matching packages %v", alias, slices.Sorted(maps.Keys(names)), len(candidates), candidates)
		}
		imports[alias] = candidates[0]
	}
	return imports, nil
}

type goExampleImporter map[string]*types.Package

func (packages goExampleImporter) Import(importPath string) (*types.Package, error) {
	if pkg := packages[importPath]; pkg != nil {
		return pkg, nil
	}
	return nil, fmt.Errorf("package %q is not present in the indexed Go package graph", importPath)
}
