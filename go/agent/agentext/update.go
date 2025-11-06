//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// This binary is used to copy agent/agentext/interfaces.go to agent/zext.go
// applying the following modifications:
//
// - Change package name from agentext to agent
// - Remove import path "github.com/microsoft/agent-framework/go/agent"
// - Lowercase agentext symbol names

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Get the directory of this file
	agentextDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Construct paths
	inputFile := filepath.Join(agentextDir, "interfaces.go")
	outputFile := filepath.Join(filepath.Dir(agentextDir), "zext.go")

	// Read input file
	content, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", inputFile, err)
	}

	// Parse the file
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, inputFile, content, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", inputFile, err)
	}

	// Transform the AST
	if err := transformAST(file); err != nil {
		return fmt.Errorf("failed to transform AST: %w", err)
	}

	// Format the output
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	// Prepend the auto-generated comment
	output := bytes.Buffer{}
	output.WriteString("// Code generated from agent/agentext/interfaces.go by update.go. DO NOT EDIT.\n\n")
	output.Write(buf.Bytes())

	// Write output file
	if err := os.WriteFile(outputFile, output.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputFile, err)
	}

	fmt.Printf("Successfully generated %s from %s\n", outputFile, inputFile)
	return nil
}

func transformAST(file *ast.File) error {
	// Change package name from agentext to agent
	file.Name.Name = "agent"

	// Remove the agent import
	var newImports []*ast.ImportSpec
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if importPath != "github.com/microsoft/agent-framework/go/agent" {
			newImports = append(newImports, imp)
		}
	}

	// Update the import declarations
	for _, decl := range file.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.IMPORT {
			var newSpecs []ast.Spec
			for _, spec := range genDecl.Specs {
				if impSpec, ok := spec.(*ast.ImportSpec); ok {
					importPath := strings.Trim(impSpec.Path.Value, `"`)
					if importPath != "github.com/microsoft/agent-framework/go/agent" {
						newSpecs = append(newSpecs, spec)
					}
				}
			}
			genDecl.Specs = newSpecs
		}
	}

	// Lowercase agentext type names only (not agent.* references)
	// Collect agentext type names first
	agentextTypes := make(map[string]bool)
	for _, decl := range file.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
			for _, spec := range genDecl.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					agentextTypes[typeSpec.Name.Name] = true
				}
			}
		}
	}

	// Now lowercase only agentext types
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.TypeSpec:
			// Lowercase type names defined in agentext
			node.Name.Name = lowercaseFirst(node.Name.Name)

		case *ast.FuncDecl:
			// Lowercase function names
			if node.Name != nil {
				node.Name.Name = lowercaseFirst(node.Name.Name)
			}

		case *ast.Ident:
			// Only lowercase if it's an agentext type, not an agent type
			if agentextTypes[node.Name] {
				node.Name = lowercaseFirst(node.Name)
			}
		}
		return true
	})

	// Additional pass to handle agent.Type references
	// We need to replace *ast.SelectorExpr with *ast.Ident when X is "agent"
	ast.Inspect(file, func(n ast.Node) bool {
		switch parent := n.(type) {
		case *ast.Field:
			// Replace agent.Type with Type in field types
			parent.Type = replaceAgentSelector(parent.Type)

		case *ast.FuncDecl:
			// Replace in function parameters and results
			if parent.Type != nil {
				for _, field := range parent.Type.Params.List {
					field.Type = replaceAgentSelector(field.Type)
				}
				if parent.Type.Results != nil {
					for _, field := range parent.Type.Results.List {
						field.Type = replaceAgentSelector(field.Type)
					}
				}
			}
		}
		return true
	})

	return nil
}

// replaceAgentSelector replaces agent.Type with just Type
func replaceAgentSelector(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if ident, ok := e.X.(*ast.Ident); ok && ident.Name == "agent" {
			// Replace with just the selector name
			return &ast.Ident{
				Name:    e.Sel.Name,
				NamePos: e.Sel.NamePos,
			}
		}
		return e

	case *ast.StarExpr:
		// Handle *agent.Type
		e.X = replaceAgentSelector(e.X)
		return e

	case *ast.ArrayType:
		// Handle []agent.Type
		e.Elt = replaceAgentSelector(e.Elt)
		return e

	case *ast.MapType:
		// Handle map[agent.Type]agent.Type
		e.Key = replaceAgentSelector(e.Key)
		e.Value = replaceAgentSelector(e.Value)
		return e

	case *ast.ChanType:
		// Handle chan agent.Type
		e.Value = replaceAgentSelector(e.Value)
		return e

	case *ast.FuncType:
		// Handle func(agent.Type) agent.Type
		if e.Params != nil {
			for _, field := range e.Params.List {
				field.Type = replaceAgentSelector(field.Type)
			}
		}
		if e.Results != nil {
			for _, field := range e.Results.List {
				field.Type = replaceAgentSelector(field.Type)
			}
		}
		return e

	case *ast.Ellipsis:
		// Handle ...agent.Type
		e.Elt = replaceAgentSelector(e.Elt)
		return e

	case *ast.IndexExpr:
		// Handle generic types like Generic[agent.Type]
		e.X = replaceAgentSelector(e.X)
		e.Index = replaceAgentSelector(e.Index)
		return e

	case *ast.IndexListExpr:
		// Handle generic types with multiple type parameters like Generic[agent.Type1, agent.Type2]
		e.X = replaceAgentSelector(e.X)
		for i, index := range e.Indices {
			e.Indices[i] = replaceAgentSelector(index)
		}
		return e

	default:
		return expr
	}
}

// lowercaseFirst converts the first letter of a string to lowercase
func lowercaseFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}
