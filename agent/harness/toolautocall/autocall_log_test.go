// Copyright (c) Microsoft. All rights reserved.

package toolautocall_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
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
		functool.MustNew(functool.Config{Name: "TestFunc"},
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

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
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
		functool.MustNew(functool.Config{Name: "TestFunc"},
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

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
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
		functool.MustNew(functool.Config{Name: "TestFunc"},
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

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
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
		functool.MustNew(functool.Config{Name: "FailingFunc"},
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

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
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
		functool.MustNew(functool.Config{Name: "CancelableFunc"},
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

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
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

func TestAutocall_LogsFunctionNotFound(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "MissingFunc", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Requested function \"MissingFunc\" not found."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, nil, plan, nil, toolautocall.Config{
		Logger: log,
	})

	output := buf.String()
	if !strings.Contains(output, "function not found") {
		t.Errorf("expected log to contain 'function not found', got: %s", output)
	}
	if !strings.Contains(output, "funcName=MissingFunc") {
		t.Errorf("expected log to contain 'funcName=MissingFunc', got: %s", output)
	}
}

func TestAutocall_LogsNonInvocableFunction(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tools := []tool.Tool{
		schemaOnlyTool{Tool: agenttest.NewTool("DeclaredFunc", "declared function")},
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "DeclaredFunc", Arguments: `{}`},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
		Logger: log,
	})

	output := buf.String()
	if !strings.Contains(output, "function is not invocable") {
		t.Errorf("expected log to contain 'function is not invocable', got: %s", output)
	}
	if !strings.Contains(output, "funcName=DeclaredFunc") {
		t.Errorf("expected log to contain 'funcName=DeclaredFunc', got: %s", output)
	}
}

func TestAutocall_LogsMaximumIterationReached(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "Result", nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
		Logger:                      log,
		MaximumIterationsPerRequest: 1,
	})

	output := buf.String()
	if !strings.Contains(output, "maximum iteration count") {
		t.Errorf("expected log to contain 'maximum iteration count', got: %s", output)
	}
	if !strings.Contains(output, "maximumIterationsPerRequest=1") {
		t.Errorf("expected log to contain 'maximumIterationsPerRequest=1', got: %s", output)
	}
}

func TestAutocall_LogsFunctionRequiresApproval(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	testTool := tool.ApprovalRequiredFunc(functool.MustNew(functool.Config{Name: "NeedsApproval"},
		func(ctx context.Context, args struct{}) (string, error) {
			return "approved", nil
		}))

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{
				&message.FunctionCallContent{CallID: "callId1", Name: "NeedsApproval", Arguments: `{}`},
			}}).
			Build(),
	}

	for _, err := range toolautocall.New(toolautocall.Config{Logger: log}).Run(
		runner.Run,
		t.Context(),
		[]*message.Message{message.NewText("hello")},
		agent.WithTool(testTool),
	) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	output := buf.String()
	if !strings.Contains(output, "function requires approval") {
		t.Errorf("expected log to contain 'function requires approval', got: %s", output)
	}
	if !strings.Contains(output, "funcName=NeedsApproval") {
		t.Errorf("expected log to contain 'funcName=NeedsApproval', got: %s", output)
	}
}

func TestAutocall_LogsApprovalResponseAndRejection(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	requestCall := &message.FunctionCallContent{CallID: "callId1", Name: "NeedsApproval", Arguments: `{}`}
	responseCall := &message.FunctionCallContent{CallID: "callId1", Name: "NeedsApproval", Arguments: `{}`}
	input := []*message.Message{
		message.New(&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: requestCall}),
		message.New(&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: false, Reason: "not now", ToolCall: responseCall}),
	}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().AddText("done").Build(),
	}

	for _, err := range toolautocall.New(toolautocall.Config{Logger: log, NewID: func() string { return "" }}).Run(
		runner.Run,
		t.Context(),
		input,
	) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	output := buf.String()
	if !strings.Contains(output, "processing approval response") {
		t.Errorf("expected log to contain 'processing approval response', got: %s", output)
	}
	if !strings.Contains(output, "approved=false") {
		t.Errorf("expected log to contain 'approved=false', got: %s", output)
	}
	if !strings.Contains(output, "function was rejected") {
		t.Errorf("expected log to contain 'function was rejected', got: %s", output)
	}
	if !strings.Contains(output, "not now") {
		t.Errorf("expected log to contain rejection reason, got: %s", output)
	}
	if !requestCall.InformationalOnly {
		t.Fatal("expected rejected request FunctionCallContent to be informational-only")
	}
	if !responseCall.InformationalOnly {
		t.Fatal("expected rejected response FunctionCallContent to be informational-only")
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
		functool.MustNew(functool.Config{Name: "Func1"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "Result1", nil
			}),
		functool.MustNew(functool.Config{Name: "Func2"},
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

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
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
		functool.MustNew(functool.Config{Name: "SlowFunc1"},
			func(ctx context.Context, args struct{}) (string, error) {
				time.Sleep(10 * time.Millisecond)
				return "Result1", nil
			}),
		functool.MustNew(functool.Config{Name: "SlowFunc2"},
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

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{
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
