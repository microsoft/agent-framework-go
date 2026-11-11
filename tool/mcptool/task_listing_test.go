// Copyright (c) Microsoft. All rights reserved.

package mcptool_test

import (
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

func assertInvalidTaskOptions(t *testing.T, options mcptool.TaskOptions, want string) {
	t.Helper()
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("tool", taskTextTool("result")))
	_, err := mcptool.ListToolsWithTasks(t.Context(), server.client, options)
	assertTaskErrorContains(t, err, want)
}
