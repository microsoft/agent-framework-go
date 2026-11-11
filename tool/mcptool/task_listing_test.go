// Copyright (c) Microsoft. All rights reserved.

package mcptool_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListToolsWithTasks_WrapsAllTools(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{}, map[string]mcp.ToolHandler{
		"first":  taskTextTool("first-result"),
		"second": taskTextTool("second-result"),
	})
	tools, err := mcptool.ListToolsWithTasks(t.Context(), server.client, mcptool.TaskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	names := make([]string, len(tools))
	for i, current := range tools {
		if _, ok := current.(tool.FuncTool); !ok {
			t.Errorf("tool %d is %T, want tool.FuncTool", i, current)
		}
		names[i] = current.Name()
	}
	if !reflect.DeepEqual(names, []string{"first", "second"}) {
		t.Fatalf("tool names = %v, want [first second]", names)
	}
}

func TestListToolsWithTasks_ThrowsOnNullClient(t *testing.T) {
	_, err := mcptool.ListToolsWithTasks(t.Context(), nil, mcptool.TaskOptions{})
	assertTaskErrorContains(t, err, "nil")
}

func TestListToolsWithTasks_NegativeStuckPollLimitThrows(t *testing.T) {
	options := mcptool.TaskOptions{MaxConsecutiveStuckPolls: -1}
	assertInvalidTaskOptions(t, options, "MaxConsecutiveStuckPolls")
}

func TestListToolsWithTasks_NegativeInputRequestLimitThrows(t *testing.T) {
	options := mcptool.TaskOptions{MaxTotalInputRequests: -1}
	assertInvalidTaskOptions(t, options, "MaxTotalInputRequests")
}

func TestListToolsWithTasks_NegativeCancellationTimeoutThrows(t *testing.T) {
	options := mcptool.TaskOptions{RemoteCancellationTimeout: -time.Second}
	assertInvalidTaskOptions(t, options, "RemoteCancellationTimeout")
}

func TestListToolsWithTasks_SubMillisecondCancellationTimeoutThrows(t *testing.T) {
	options := mcptool.TaskOptions{RemoteCancellationTimeout: time.Nanosecond}
	assertInvalidTaskOptions(t, options, "RemoteCancellationTimeout")
}

func TestListToolsWithTasks_InvalidPollingIntervalRangeThrows(t *testing.T) {
	options := mcptool.TaskOptions{}
	options.MinimumPollingInterval = 20 * time.Millisecond
	options.MaximumPollingInterval = 10 * time.Millisecond
	assertInvalidTaskOptions(t, options, "MaximumPollingInterval")
}

func TestListToolsWithTasks_PollingRangeWithoutWholeMillisecondThrows(t *testing.T) {
	options := mcptool.TaskOptions{}
	options.MinimumPollingInterval = time.Nanosecond
	options.MaximumPollingInterval = time.Millisecond - time.Nanosecond
	assertInvalidTaskOptions(t, options, "whole millisecond")
}

func TestListToolsWithTasks_PollingMaximumAboveRuntimeLimitThrows(t *testing.T) {
	maximum := time.Duration((1<<63 - 1) / int64(time.Millisecond) * int64(time.Millisecond))
	options := mcptool.TaskOptions{MaximumPollingInterval: maximum + 1}
	assertInvalidTaskOptions(t, options, "MaximumPollingInterval")
}

func TestListToolsWithTasks_NegativePollingIntervalsThrow(t *testing.T) {
	t.Run("minimum", func(t *testing.T) {
		assertInvalidTaskOptions(t, mcptool.TaskOptions{MinimumPollingInterval: -time.Second}, "MinimumPollingInterval")
	})
	t.Run("maximum", func(t *testing.T) {
		assertInvalidTaskOptions(t, mcptool.TaskOptions{MaximumPollingInterval: -time.Second}, "MaximumPollingInterval")
	})
}

func TestListToolsWithTasks_InputCapabilitiesRequireHandler(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options mcptool.TaskOptions
	}{
		{"handler only", mcptool.TaskOptions{InputHandler: acceptInputHandler}},
		{"elicitation only", mcptool.TaskOptions{ElicitationCapabilities: &mcp.ElicitationCapabilities{}}},
		{"sampling only", mcptool.TaskOptions{SamplingCapabilities: &mcp.SamplingCapabilities{}}}, //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
		{"both capabilities without handler", mcptool.TaskOptions{
			ElicitationCapabilities: &mcp.ElicitationCapabilities{},
			SamplingCapabilities:    &mcp.SamplingCapabilities{}, //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertInvalidTaskOptions(t, tt.options, "InputHandler")
		})
	}
}

func TestListToolsWithTasks_SnapshotsInputCapabilitiesBeforeListing(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		initialStatus: "input_required",
		inputRequests: []mcp.InputRequest{elicitationRequest("Confirm operation.")},
		inputCapabilities: map[string]any{
			"elicitation": map[string]any{"form": map[string]any{}},
			"sampling":    map[string]any{"context": map[string]any{}, "tools": map[string]any{}},
		},
	}, taskTestTool("task-tool", taskTextTool("unused")))
	started, resume := make(chan struct{}), make(chan struct{})
	server.server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				close(started)
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return next(ctx, method, request)
		}
	})
	elicitation := &mcp.ElicitationCapabilities{}
	sampling := &mcp.SamplingCapabilities{ //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
		Context: &mcp.SamplingContextCapabilities{}, //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
		Tools:   &mcp.SamplingToolsCapabilities{},   //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type listing struct {
		tools []tool.Tool
		err   error
	}
	listed := make(chan listing, 1)
	go func() {
		tools, err := mcptool.ListToolsWithTasks(ctx, server.client, mcptool.TaskOptions{
			InputHandler:            acceptInputHandler,
			ElicitationCapabilities: elicitation,
			SamplingCapabilities:    sampling,
		})
		listed <- listing{tools, err}
	}()
	select {
	case <-started:
	case result := <-listed:
		t.Fatalf("listing ended before the server received tools/list: %v", result.err)
	}
	if elicitation.Form != nil {
		t.Error("listing mutated the caller's elicitation capabilities")
	}
	elicitation.URL = &mcp.URLElicitationCapabilities{}
	sampling.Context = nil
	sampling.Tools = nil
	close(resume)
	list := <-listed
	if list.err != nil {
		t.Fatal(list.err)
	}
	result, err := findFuncTool(t, list.tools, "task-tool").Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustTextResult(t, result); got != "completed" {
		t.Fatalf("result = %q, want completed", got)
	}
}

func assertInvalidTaskOptions(t *testing.T, options mcptool.TaskOptions, want string) {
	t.Helper()
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("tool", taskTextTool("result")))
	_, err := mcptool.ListToolsWithTasks(t.Context(), server.client, options)
	assertTaskErrorContains(t, err, want)
}
