// Copyright (c) Microsoft. All rights reserved.

package autocall_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware/autocall"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// TestAutocall_LogsSuccessfulFunctionCall tests that successful function calls are logged
func TestAutocall_LogsSuccessfulFunctionCall(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "TestFunc"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "Success", nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "TestFunc", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Success"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocall.Config{
		Logger: log,
	})

	// Assert logs contain expected entries
	output := buf.String()
	if !strings.Contains(output, "calling function") {
		t.Errorf("expected log to contain 'calling function', got: %s", output)
	}
	if !strings.Contains(output, "funcName=TestFunc") {
		t.Errorf("expected log to contain 'funcName=TestFunc', got: %s", output)
	}
	if !strings.Contains(output, "function call completed") {
		t.Errorf("expected log to contain 'function call completed', got: %s", output)
	}
	if !strings.Contains(output, "duration") {
		t.Errorf("expected log to contain 'duration', got: %s", output)
	}
}

// TestAutocall_LogsSensitiveData tests that sensitive data is logged when enabled
func TestAutocall_LogsSensitiveData(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	type TestArgs struct {
		Value string `json:"value"`
	}

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "TestFunc"},
			func(ctx context.Context, args TestArgs) (string, error) {
				return "Result: " + args.Value, nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "TestFunc", Arguments: `{"value":"secretdata"}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result: secretdata"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocall.Config{
		Logger:           log,
		LogSensitiveData: true,
	})

	// Assert logs contain sensitive data
	output := buf.String()
	if !strings.Contains(output, "arguments") {
		t.Errorf("expected log to contain 'arguments', got: %s", output)
	}
	if !strings.Contains(output, "secretdata") {
		t.Errorf("expected log to contain 'secretdata', got: %s", output)
	}
	if !strings.Contains(output, "result") {
		t.Errorf("expected log to contain 'result', got: %s", output)
	}
	if !strings.Contains(output, "Result: secretdata") {
		t.Errorf("expected log to contain result value, got: %s", output)
	}
}

// TestAutocall_DoesNotLogSensitiveDataByDefault tests that sensitive data is not logged by default
func TestAutocall_DoesNotLogSensitiveDataByDefault(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	type TestArgs struct {
		Value string `json:"value"`
	}

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "TestFunc"},
			func(ctx context.Context, args TestArgs) (string, error) {
				return "Result: " + args.Value, nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "TestFunc", Arguments: `{"value":"secretdata"}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result: secretdata"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocall.Config{
		Logger:           log,
		LogSensitiveData: false,
	})

	// Assert logs DO NOT contain sensitive data
	output := buf.String()
	if strings.Contains(output, "arguments") {
		t.Errorf("expected log to NOT contain 'arguments', got: %s", output)
	}
	if strings.Contains(output, "secretdata") {
		t.Errorf("expected log to NOT contain 'secretdata', got: %s", output)
	}
	if strings.Contains(output, "result") {
		t.Errorf("expected log to NOT contain 'result', got: %s", output)
	}
	// Function name should still be logged
	if !strings.Contains(output, "funcName=TestFunc") {
		t.Errorf("expected log to contain 'funcName=TestFunc', got: %s", output)
	}
}

// TestAutocall_LogsFailedFunctionCall tests that failed function calls are logged
func TestAutocall_LogsFailedFunctionCall(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "FailingFunc"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "", errors.New("something went wrong")
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "FailingFunc", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Function failed.", Error: fmt.Errorf("something went wrong")},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocall.Config{
		Logger:                             log,
		MaximumConsecutiveErrorsPerRequest: 3,
	})

	// Assert logs contain error information
	output := buf.String()
	if !strings.Contains(output, "call failed") {
		t.Errorf("expected log to contain 'call failed', got: %s", output)
	}
	if !strings.Contains(output, "funcName=FailingFunc") {
		t.Errorf("expected log to contain 'funcName=FailingFunc', got: %s", output)
	}
	if !strings.Contains(output, "error") {
		t.Errorf("expected log to contain 'error', got: %s", output)
	}
	if !strings.Contains(output, "something went wrong") {
		t.Errorf("expected log to contain error message, got: %s", output)
	}
}

// TestAutocall_LogsCanceledFunctionCall tests that canceled function calls are logged
func TestAutocall_LogsCanceledFunctionCall(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "CancelableFunc"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "", context.Canceled
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "CancelableFunc", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Function failed.", Error: context.Canceled},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocall.Config{
		Logger:                             log,
		MaximumConsecutiveErrorsPerRequest: 3,
	})

	// Assert logs contain cancellation information
	output := buf.String()
	if !strings.Contains(output, "call canceled") {
		t.Errorf("expected log to contain 'call canceled', got: %s", output)
	}
	if !strings.Contains(output, "funcName=CancelableFunc") {
		t.Errorf("expected log to contain 'funcName=CancelableFunc', got: %s", output)
	}
}

// TestAutocall_LogsMultipleFunctionCalls tests that multiple function calls are all logged
func TestAutocall_LogsMultipleFunctionCalls(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "Func1"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "Result1", nil
			}),
		functool.MustNew(&functool.Func{Name: "Func2"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "Result2", nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: `{}`},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result2"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocall.Config{
		Logger:                     log,
		AllowConcurrentInvocations: false,
	})

	// Assert logs contain both function calls
	output := buf.String()
	func1Calls := strings.Count(output, "funcName=Func1")
	func2Calls := strings.Count(output, "funcName=Func2")

	// Each function should appear twice: once in "calling function" and once in "function call completed"
	if func1Calls < 2 {
		t.Errorf("expected at least 2 log entries for Func1, got %d. Output: %s", func1Calls, output)
	}
	if func2Calls < 2 {
		t.Errorf("expected at least 2 log entries for Func2, got %d. Output: %s", func2Calls, output)
	}
}

// TestAutocall_LoggingWithConcurrentInvocations tests logging with concurrent function calls
func TestAutocall_LoggingWithConcurrentInvocations(t *testing.T) {
	// Create a logger that writes to a buffer
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "SlowFunc1"},
			func(ctx context.Context, args struct{}) (string, error) {
				time.Sleep(10 * time.Millisecond)
				return "Result1", nil
			}),
		functool.MustNew(&functool.Func{Name: "SlowFunc2"},
			func(ctx context.Context, args struct{}) (string, error) {
				time.Sleep(10 * time.Millisecond)
				return "Result2", nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "SlowFunc1", Arguments: `{}`},
			&message.FunctionCallContent{CallID: "callId2", Name: "SlowFunc2", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result2"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocall.Config{
		Logger:                     log,
		AllowConcurrentInvocations: true,
	})

	// Assert logs contain both function calls
	output := buf.String()
	if !strings.Contains(output, "funcName=SlowFunc1") {
		t.Errorf("expected log to contain SlowFunc1, got: %s", output)
	}
	if !strings.Contains(output, "funcName=SlowFunc2") {
		t.Errorf("expected log to contain SlowFunc2, got: %s", output)
	}
	// Verify completion logs
	completionLogs := strings.Count(output, "function call completed")
	if completionLogs < 2 {
		t.Errorf("expected at least 2 'function call completed' logs, got %d", completionLogs)
	}
}
