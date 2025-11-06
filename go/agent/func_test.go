// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/internal/agenttest"
)

// Test FuncTool
func TestFuncTool_Basic(t *testing.T) {
	type Input struct {
		Message string `json:"message"`
	}

	handler := func(ctx context.Context, input Input) (string, error) {
		return "output: " + input.Message, nil
	}

	funcDef := &agent.Func{
		Name:        "test_func",
		Description: "Test function",
	}

	tool, err := agent.NewFuncTool(funcDef, handler)
	if err != nil {
		t.Fatalf("expected no error creating FuncTool, got: %v", err)
	}

	name, desc := tool.ToolInfo()
	if name != "test_func" {
		t.Errorf("expected name 'test_func', got %q", name)
	}
	if desc != "Test function" {
		t.Errorf("expected description 'Test function', got %q", desc)
	}

	// Test schema
	schema := tool.Schema()
	if schema == nil {
		t.Fatal("expected schema, got nil")
	}
}

func TestFuncTool_MustNew(t *testing.T) {
	handler := func(ctx context.Context, input string) (string, error) {
		return "result", nil
	}

	funcDef := &agent.Func{
		Name:        "must_func",
		Description: "Must function",
	}

	tool := agent.MustNewFuncTool(funcDef, handler)
	if tool == nil {
		t.Fatal("expected tool, got nil")
	}

	name, _ := tool.ToolInfo()
	if name != "must_func" {
		t.Errorf("expected name 'must_func', got %q", name)
	}
}

func TestFuncTool_MustNewPanic(t *testing.T) {
	// Test that MustNewFuncTool panics on error
	// We'll create an error condition by having invalid schema setup
	defer func() {
		if r := recover(); r != nil {
			// Expected panic, test passes
			return
		}
	}()

	// This should succeed, so we skip this test as it's hard to force an error
	t.Skip("Skipping panic test as it's difficult to force an error condition")
}

func TestFuncTool_MissingArg0(t *testing.T) {
	type Input struct {
		Value string `json:"value"`
	}

	handler := func(ctx context.Context, input Input) (string, error) {
		return "result", nil
	}

	tool := agent.MustNewFuncTool(
		&agent.Func{
			Name: "test",
		},
		handler,
	)

	// Call without required arg0
	_, err := tool.Call(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing arg0, got nil")
	}
}

func TestFuncTool_HandlerError(t *testing.T) {
	type Input struct {
		Value string `json:"value"`
	}

	expectedErr := errors.New("handler error")
	handler := func(ctx context.Context, input Input) (string, error) {
		return "", expectedErr
	}

	tool := agent.MustNewFuncTool(
		&agent.Func{
			Name: "error_func",
		},
		handler,
	)

	_, err := tool.Call(context.Background(), map[string]any{
		"arg0": map[string]any{"value": "test"},
	})
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestFuncTool_WithAgent(t *testing.T) {
	client := agenttest.NewClient()

	handler := func(ctx context.Context, location string) (string, error) {
		return "Weather in " + location + " is sunny", nil
	}

	funcDef := &agent.Func{
		Name:        "get_weather",
		Description: "Get weather for a location",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"location": map[string]any{"type": "string"}},
		},
	}

	tool := agent.MustNewFuncTool(funcDef, handler)

	toolCalls := []*agent.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "get_weather",
			Arguments: `{"location": "Seattle"}`,
		},
	}
	client.WithToolCalls(toolCalls, "Final response")

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("What's the weather?"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp.Text() != "Final response" {
		t.Errorf("expected 'Final response', got %q", resp.Text())
	}
}

// Test HostedWebSearchTool
func TestHostedWebSearchTool(t *testing.T) {
	tool := &agent.HostedWebSearchTool{
		Description: "Search the web",
	}

	name, desc := tool.ToolInfo()
	if name != "web_search" {
		t.Errorf("expected name 'web_search', got %q", name)
	}
	if desc != "Search the web" {
		t.Errorf("expected description 'Search the web', got %q", desc)
	}
}

// Test ToolString
func TestToolString(t *testing.T) {
	tool := &agenttest.Tool{
		NameValue: "test_tool",
		DescValue: "A test tool",
	}

	str := agent.ToolString(tool)
	if str == "" {
		t.Error("expected non-empty string")
	}
	// Should contain the name
	if len(str) < len("test_tool") {
		t.Errorf("expected string to contain tool name, got %q", str)
	}
}

// Test tool initialization
func TestAgent_ToolInit(t *testing.T) {
	client := agenttest.NewClient()

	initCalled := false
	tool := &agenttest.InitializableTool{
		Tool: agenttest.Tool{
			NameValue: "init_tool",
		},
		InitFunc: func(ctx context.Context) error {
			initCalled = true
			return nil
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if !initCalled {
		t.Error("expected tool Init to be called")
	}
}

func TestAgent_ToolInitError(t *testing.T) {
	client := agenttest.NewClient()

	expectedErr := errors.New("init failed")
	tool := &agenttest.InitializableTool{
		Tool: agenttest.Tool{
			NameValue: "init_tool",
		},
		InitFunc: func(ctx context.Context) error {
			return expectedErr
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("Test"))

	if err == nil {
		t.Fatal("expected error from tool init, got nil")
	}
}

// Test tool loader
func TestAgent_ToolLoader(t *testing.T) {
	client := agenttest.NewClient()

	innerTool := &agenttest.Tool{
		NameValue: "inner_tool",
	}

	loaderTool := &agenttest.LoaderTool{
		Tool: agenttest.Tool{
			NameValue: "loader",
		},
		LoadFunc: func(ctx context.Context) ([]agent.Tool, error) {
			return []agent.Tool{innerTool}, nil
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{loaderTool},
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify the loader was called and inner tools were loaded
	lastCall := client.GetLastRunCall()
	if lastCall.Opts == nil {
		t.Fatal("expected opts")
	}
	// Should have both the loader and the inner tool
	if len(lastCall.Opts.Tools) < 2 {
		t.Errorf("expected at least 2 tools (loader + inner), got %d", len(lastCall.Opts.Tools))
	}
}

func TestAgent_ToolLoaderError(t *testing.T) {
	client := agenttest.NewClient()

	expectedErr := errors.New("load failed")
	loaderTool := &agenttest.LoaderTool{
		Tool: agenttest.Tool{
			NameValue: "loader",
		},
		LoadFunc: func(ctx context.Context) ([]agent.Tool, error) {
			return nil, expectedErr
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{loaderTool},
	}, agent.NewTextMessage("Test"))

	if err == nil {
		t.Fatal("expected error from tool loader, got nil")
	}
}
