// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/internal/agenttest"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

func TestAgentCallTool(t *testing.T) {
	client, a := agenttest.NewAgent()

	handler := func(ctx context.Context, location string) (string, error) {
		return "Weather in " + location + " is sunny", nil
	}

	funcDef := &functool.Func{
		Name:        "get_weather",
		Description: "Get weather for a location",
	}

	tl := functool.MustNew(funcDef, handler)

	toolCalls := []*message.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "get_weather",
			Arguments: `{"location": "Seattle"}`,
		},
	}
	client.WithToolCalls(toolCalls, "Final response")

	ctx := &agent.RunContext{Options: &agent.RunOptions{
		Tools: []tool.Tool{tl},
	}}
	resp, err := a.Run(ctx, message.NewText("What's the weather?"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp.String() != "Final response" {
		t.Errorf("expected 'Final response', got %q", resp.String())
	}
}

// Test tool initialization
func TestAgent_ToolInit(t *testing.T) {
	_, a := agenttest.NewAgent()

	initCalled := false
	tl := &agenttest.InitializableTool{
		Tool: agenttest.Tool{
			Name: "init_tool",
		},
		InitFunc: func(ctx context.Context) error {
			initCalled = true
			return nil
		},
	}
	ctx := &agent.RunContext{Options: &agent.RunOptions{
		Tools: []tool.Tool{tl},
	}}
	_, err := a.Run(ctx, message.NewText("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if !initCalled {
		t.Error("expected tool Init to be called")
	}
}

func TestAgent_ToolInitError(t *testing.T) {
	_, a := agenttest.NewAgent()

	expectedErr := errors.New("init failed")
	tl := &agenttest.InitializableTool{
		Tool: agenttest.Tool{
			Name: "init_tool",
		},
		InitFunc: func(ctx context.Context) error {
			return expectedErr
		},
	}

	ctx := &agent.RunContext{Options: &agent.RunOptions{
		Tools: []tool.Tool{tl},
	}}
	_, err := a.Run(ctx, message.NewText("Test"))

	if err == nil {
		t.Fatal("expected error from tool init, got nil")
	}
}

// Test tool loader
func TestAgent_ToolLoader(t *testing.T) {
	client, a := agenttest.NewAgent()

	innerTool := &agenttest.Tool{
		Name: "inner_tool",
	}

	loaderTool := &agenttest.LoaderTool{
		Tool: agenttest.Tool{
			Name: "loader",
		},
		LoadFunc: func(ctx context.Context) ([]tool.Tool, error) {
			return []tool.Tool{innerTool}, nil
		},
	}

	ctx := &agent.RunContext{Options: &agent.RunOptions{
		Tools: []tool.Tool{loaderTool},
	}}
	_, err := a.Run(ctx, message.NewText("Test"))

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
	_, a := agenttest.NewAgent()

	expectedErr := errors.New("load failed")
	loaderTool := &agenttest.LoaderTool{
		Tool: agenttest.Tool{
			Name: "loader",
		},
		LoadFunc: func(ctx context.Context) ([]tool.Tool, error) {
			return nil, expectedErr
		},
	}

	ctx := &agent.RunContext{Options: &agent.RunOptions{
		Tools: []tool.Tool{loaderTool},
	}}
	_, err := a.Run(ctx, message.NewText("Test"))
	if err == nil {
		t.Fatal("expected error from tool loader, got nil")
	}
}
