// Copyright (c) Microsoft. All rights reserved.

package tool

import (
	"context"

	"github.com/microsoft/agent-framework/golang/pkg/types"
)

// Tool represents a tool or function that an agent can use.
type Tool interface {
	types.Identifiable
	types.Nameable

	// Description returns a description of what the tool does.
	Description() string

	// Schema returns the JSON schema for the tool's parameters.
	Schema() map[string]interface{}

	// Execute runs the tool with the given arguments.
	Execute(ctx context.Context, arguments string) (string, error)
}

// Function is a basic implementation of Tool for custom functions.
type Function struct {
	id          string
	name        string
	description string
	schema      map[string]interface{}
	handler     func(ctx context.Context, arguments string) (string, error)
}

// FunctionConfig contains configuration for creating a Function.
type FunctionConfig struct {
	Name        string
	Description string
	Schema      map[string]interface{}
	Handler     func(ctx context.Context, arguments string) (string, error)
}

// NewFunction creates a new Function tool.
func NewFunction(config FunctionConfig) *Function {
	return &Function{
		id:          config.Name, // Use name as ID for simplicity
		name:        config.Name,
		description: config.Description,
		schema:      config.Schema,
		handler:     config.Handler,
	}
}

// ID returns the function's unique identifier.
func (f *Function) ID() string {
	return f.id
}

// Name returns the function's name.
func (f *Function) Name() string {
	return f.name
}

// Description returns the function's description.
func (f *Function) Description() string {
	return f.description
}

// Schema returns the JSON schema for the function's parameters.
func (f *Function) Schema() map[string]interface{} {
	return f.schema
}

// Execute runs the function with the given arguments.
func (f *Function) Execute(ctx context.Context, arguments string) (string, error) {
	return f.handler(ctx, arguments)
}

// HostedTool represents a tool hosted by the LLM provider (e.g., code interpreter).
type HostedTool interface {
	Tool
	// ToolType returns the provider-specific tool type identifier.
	ToolType() string
}

// CodeInterpreter is a hosted tool for executing code.
type CodeInterpreter struct {
	id string
}

// NewCodeInterpreter creates a new CodeInterpreter tool.
func NewCodeInterpreter() *CodeInterpreter {
	return &CodeInterpreter{id: "code_interpreter"}
}

func (c *CodeInterpreter) ID() string                                              { return c.id }
func (c *CodeInterpreter) Name() string                                            { return "code_interpreter" }
func (c *CodeInterpreter) Description() string                                     { return "Execute Python code" }
func (c *CodeInterpreter) Schema() map[string]interface{}                          { return nil }
func (c *CodeInterpreter) Execute(ctx context.Context, arguments string) (string, error) {
	return "", nil
}
func (c *CodeInterpreter) ToolType() string { return "code_interpreter" }

// FileSearch is a hosted tool for searching files.
type FileSearch struct {
	id string
}

// NewFileSearch creates a new FileSearch tool.
func NewFileSearch() *FileSearch {
	return &FileSearch{id: "file_search"}
}

func (f *FileSearch) ID() string                                              { return f.id }
func (f *FileSearch) Name() string                                            { return "file_search" }
func (f *FileSearch) Description() string                                     { return "Search files" }
func (f *FileSearch) Schema() map[string]interface{}                          { return nil }
func (f *FileSearch) Execute(ctx context.Context, arguments string) (string, error) {
	return "", nil
}
func (f *FileSearch) ToolType() string { return "file_search" }

// WebSearch is a hosted tool for web searching.
type WebSearch struct {
	id string
}

// NewWebSearch creates a new WebSearch tool.
func NewWebSearch() *WebSearch {
	return &WebSearch{id: "web_search"}
}

func (w *WebSearch) ID() string                                              { return w.id }
func (w *WebSearch) Name() string                                            { return "web_search" }
func (w *WebSearch) Description() string                                     { return "Search the web" }
func (w *WebSearch) Schema() map[string]interface{}                          { return nil }
func (w *WebSearch) Execute(ctx context.Context, arguments string) (string, error) {
	return "", nil
}
func (w *WebSearch) ToolType() string { return "web_search" }
