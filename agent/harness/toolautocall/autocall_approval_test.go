// Copyright (c) Microsoft. All rights reserved.

package toolautocall_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/messagetest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func expectedMessages(t *testing.T, expected ...*message.Message) func(context.Context, []*message.Message, ...agent.Option) {
	return func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
		if err := messagetest.MessagesEqual(messages, expected); err != nil {
			t.Errorf("Messages not equal: %v", err)
		}
	}
}

// invokeAndAssertApproval is the helper for approval tests
func invokeAndAssertApproval(t *testing.T, tools []tool.Tool, input []*message.Message,
	downstreamAgentOutput []*agent.ResponseUpdate, expectedOutput []*agent.ResponseUpdate,
	expectedDownstreamAgentInput []*message.Message, additionalTools []tool.Tool,
) {
	var cb func(context.Context, []*message.Message, ...agent.Option)
	if expectedDownstreamAgentInput != nil {
		cb = expectedMessages(t, expectedDownstreamAgentInput...)
	}
	rb := agenttest.NewResponseBuilder(cb)
	for _, resp := range downstreamAgentOutput {
		rb.Add(resp)
	}

	runner := &agenttest.Runner{
		Responses: rb.Build(),
	}

	invokeAndAssertApprovalWithAgent(t, runner.Run, tools, input, expectedOutput, additionalTools)
}

// invokeAndAssertApprovalWithAgent performs streaming test execution
func invokeAndAssertApprovalWithAgent(t *testing.T, next agent.RunFunc,
	tools []tool.Tool, input []*message.Message,
	expectedOutput []*agent.ResponseUpdate, additionalTools []tool.Tool,
) {
	autocallOptions := toolautocall.Config{
		NewID: func() string { return "" },
	}
	if additionalTools != nil {
		autocallOptions.AdditionalTools = additionalTools
	}

	ctx := t.Context()

	// Build options
	var opts []agent.Option
	for _, tool := range tools {
		opts = append(opts, agent.WithTool(tool))
	}

	// Collect all streaming updates into messages
	var updates []*agent.ResponseUpdate
	for update, err := range toolautocall.New(autocallOptions).Run(next, ctx, input, opts...) {
		if err != nil {
			t.Fatalf("StreamingResponse failed: %v", err)
		}
		updates = append(updates, update)
	}
	if err := agenttest.ResponseUpdatesEqual(updates, expectedOutput); err != nil {
		t.Fatal(err)
	}
}

// expectApprovalError expects an error during streaming invocation
func expectApprovalError(t *testing.T, tools []tool.Tool, input []*message.Message, expectedErrorMsg string) {
	runner := &agenttest.Runner{}

	ctx := t.Context()

	// Build options
	var opts []agent.Option
	for _, tool := range tools {
		opts = append(opts, agent.WithTool(tool))
	}

	var lastErr error
	for _, err := range toolautocall.New(toolautocall.Config{}).Run(runner.Run, ctx, input, opts...) {
		if err != nil {
			lastErr = err
			break
		}
	}

	if lastErr == nil {
		t.Fatalf("Expected error with message %q, but got nil", expectedErrorMsg)
	}

	if lastErr.Error() != expectedErrorMsg {
		t.Fatalf("Expected error message %q, got %q", expectedErrorMsg, lastErr.Error())
	}
}

// TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAllRequireApproval tests that
// all function calls are replaced with approval requests when all functions require approval
func TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAllRequireApproval(t *testing.T) {
	tests := []struct {
		name               string
		useAdditionalTools bool
	}{
		{"with_agent_options_tools", false},
		{"with_additional_tools", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolList := []tool.Tool{
				tool.ApprovalRequiredFunc(createFunc1()),
				tool.ApprovalRequiredFunc(createFunc2()),
			}

			tools := toolList
			if tt.useAdditionalTools {
				tools = nil
			}

			input := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}

			downstreamAgentOutput := []*agent.ResponseUpdate{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
				}},
			}

			expectedOutput := []*agent.ResponseUpdate{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
					&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
				}},
			}

			additionalTools := []tool.Tool(nil)
			if tt.useAdditionalTools {
				additionalTools = toolList
			}

			invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, nil, additionalTools)
		})
	}
}

func TestFunctionInvoking_IgnoresInformationalOnlyApprovalContent(t *testing.T) {
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1", InformationalOnly: true}},
		}},
		message.New(&message.ToolApprovalResponseContent{
			RequestID: "ficc_callId1",
			Approved:  true,
			ToolCall:  &message.FunctionCallContent{CallID: "callId1", Name: "Func1", InformationalOnly: true},
		}),
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1", InformationalOnly: true}},
		}},
		message.New(&message.ToolApprovalResponseContent{
			RequestID: "ficc_callId1",
			Approved:  true,
			ToolCall:  &message.FunctionCallContent{CallID: "callId1", Name: "Func1", InformationalOnly: true},
		}),
	}

	invokeAndAssertApproval(t, nil, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

func TestFunctionInvoking_PreservesNilToolCallApprovalContentWhenProcessingOtherApproval(t *testing.T) {
	tools := []tool.Tool{createFunc1()}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		message.New(
			&message.ToolApprovalRequestContent{RequestID: "missing-request-tool-call"},
			&message.ToolApprovalResponseContent{RequestID: "missing-response-tool-call", Approved: true},
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		),
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		message.New(
			&message.ToolApprovalRequestContent{RequestID: "missing-request-tool-call"},
			&message.ToolApprovalResponseContent{RequestID: "missing-response-tool-call", Approved: true},
		),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

// TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAnyRequireApproval tests that
// all function calls are replaced with approval requests when any function requires approval
func TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAnyRequireApproval(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
		createFunc2(),
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
	}

	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, nil, nil)
}

// TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAnyRequestOrAdditionalRequireApproval tests that
// all function calls are replaced with approval requests when any tool (in ChatOptions.Tools or AdditionalTools) requires approval
func TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAnyRequestOrAdditionalRequireApproval(t *testing.T) {
	tests := []struct {
		name                           string
		additionalToolsRequireApproval bool
	}{
		{"additional_tools_require_approval", true},
		{"chat_options_tools_require_approval", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			func1 := createFunc1()
			func2 := createFunc2()

			var additionalTools []tool.Tool
			var chatOptionsTools []tool.Tool

			if tt.additionalToolsRequireApproval {
				// AdditionalTools has approval-required func1, ChatOptions has regular func2
				additionalTools = []tool.Tool{tool.ApprovalRequiredFunc(func1)}
				chatOptionsTools = []tool.Tool{func2}
			} else {
				// ChatOptions has approval-required func2, AdditionalTools has regular func1
				chatOptionsTools = []tool.Tool{tool.ApprovalRequiredFunc(func2)}
				additionalTools = []tool.Tool{func1}
			}

			input := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}

			downstreamAgentOutput := []*agent.ResponseUpdate{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
				}},
			}

			expectedOutput := []*agent.ResponseUpdate{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
					&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
				}},
			}

			invokeAndAssertApproval(t, chatOptionsTools, input, downstreamAgentOutput, expectedOutput, nil, additionalTools)
		})
	}
}

// TestFunctionInvoking_ApprovedApprovalResponsesAreExecuted tests that approved approval responses are executed
func TestFunctionInvoking_ApprovedApprovalResponsesAreExecuted(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
		createFunc2(),
	}

	// Input includes: user message, approval requests (from previous turn), and approval responses
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId2", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		),
	}

	// Downstream agent receives: user message and function results
	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	// Downstream agent returns a simple text response
	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	// Final output includes: function calls, function results, and the assistant response
	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

func TestFunctionInvoking_ApprovedApprovalResponsesMarkRequestInformationalOnly(t *testing.T) {
	tools := []tool.Tool{tool.ApprovalRequiredFunc(createFunc1())}
	requestCall := &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}
	responseCall := &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: requestCall},
		}},
		message.New(&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: true, ToolCall: responseCall}),
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "world"}}},
	}
	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{&message.FunctionCallContent{CallID: "callId1", Name: "Func1"}}},
		{Role: message.RoleTool, Contents: []message.Content{&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "world"}}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, nil, nil)

	if !requestCall.InformationalOnly {
		t.Fatal("expected approval request FunctionCallContent to be informational-only")
	}
	if !responseCall.InformationalOnly {
		t.Fatal("expected approval response FunctionCallContent to be informational-only")
	}
}

// TestFunctionInvoking_ApprovedApprovalResponsesFromSeparateFCCMessagesAreExecuted tests that approved approval responses
// from separate assistant messages (each with their own MessageId) are properly aggregated and executed
func TestFunctionInvoking_ApprovedApprovalResponsesFromSeparateFCCMessagesAreExecuted(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
		createFunc2(),
	}

	// Input has approval requests in separate assistant messages with different IDs,
	// and approval responses in separate user messages
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		}},
		{Role: message.RoleAssistant, ID: "resp2", Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		),
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId2", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		),
	}

	// Downstream agent receives function calls with their original message IDs preserved
	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "world"}}},
	}

	// Output includes the function calls, results, and downstream response
	expectedOutput := []*agent.ResponseUpdate{
		{MessageID: "resp1", ResponseID: "resp1", Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
		}},
		{MessageID: "resp2", ResponseID: "resp2", Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "world"}}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

// TestFunctionInvoking_RejectedApprovalResponsesAreFailed tests that rejected approval responses fail with error messages
func TestFunctionInvoking_RejectedApprovalResponsesAreFailed(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
		createFunc2(),
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: false, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId2", Approved: false, ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		),
	}

	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Tool call invocation rejected."},
			&message.FunctionResultContent{CallID: "callId2", Result: "Tool call invocation rejected."},
		}},
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Tool call invocation rejected."},
			&message.FunctionResultContent{CallID: "callId2", Result: "Tool call invocation rejected."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

func TestFunctionInvoking_RejectedApprovalResponseIncludesReason(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		}},
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: false, Reason: "user declined", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		),
	}

	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Tool call invocation rejected. user declined"},
		}},
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Tool call invocation rejected. user declined"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

// TestFunctionInvoking_MixedApprovedAndRejectedApprovalResponsesAreExecutedAndFailed tests that
// mixed approved and rejected approval responses are handled correctly
func TestFunctionInvoking_MixedApprovedAndRejectedApprovalResponsesAreExecutedAndFailed(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
		createFunc2(),
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: false, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId2", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		),
	}

	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Tool call invocation rejected."},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*agent.ResponseUpdate{
		{MessageID: "resp1", ResponseID: "resp1", Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Tool call invocation rejected."},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

// TestFunctionInvoking_ApprovedInputsAreExecutedAndFunctionResultsAreConverted tests that
// approved inputs are executed and function results are converted back to approval requests
func TestFunctionInvoking_ApprovedInputsAreExecutedAndFunctionResultsAreConverted(t *testing.T) {
	tools := []tool.Tool{
		createFunc1(),
		tool.ApprovalRequiredFunc(createFunc2()),
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId2", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		),
	}

	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	// Downstream client returns a new FunctionCallContent for Func2 with different arguments
	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":3}`},
		}},
	}

	// Output includes executed functions and the new approval request for the new Func2 call
	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":3}`}},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

// TestFunctionInvoking_AlreadyExecutedApprovalsAreIgnored tests that already executed approvals
// (ones that have both FunctionCallContent and FunctionResultContent in history) are ignored
func TestFunctionInvoking_AlreadyExecutedApprovalsAreIgnored(t *testing.T) {
	tools := []tool.Tool{
		createFunc1(),
		tool.ApprovalRequiredFunc(createFunc2()),
	}

	// Input includes history with already-executed approvals and a new approval to execute
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		// Previous turn: approval requests
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
		// Previous turn: approval responses
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId2", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		),
		// Previous turn: already executed - function calls
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		// Previous turn: already executed - function results
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		// Current turn: new approval request
		{Role: message.RoleAssistant, ID: "resp2", Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId3", ToolCall: &message.FunctionCallContent{CallID: "callId3", Name: "Func1"}},
		}},
		// Current turn: new approval response
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId3", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId3", Name: "Func1"}},
		),
	}

	// Downstream client should receive history with already-executed items and the new function call
	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Result 1"},
		}},
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "World"},
		}},
	}

	// Output only includes the newly executed approval (not the already-executed ones from history)
	expectedOutput := []*agent.ResponseUpdate{
		{MessageID: "resp2", ResponseID: "resp2", Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "Func1"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Result 1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "World"},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

// TestFunctionInvoking_MixedApprovalRequiredToolsWithNonApprovalRequiringFunctionCall tests that
// when only some tools require approval, non-approval-requiring function calls are executed immediately
// and don't trigger approval requests for all calls
func TestFunctionInvoking_MixedApprovalRequiredToolsWithNonApprovalRequiringFunctionCall(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()), // Func1 requires approval
		createFunc2(),                            // Func2 does NOT require approval
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	// Multi-round agent:
	// Round 1: Downstream returns only Func2 call (no approval required)
	// Round 2: After executing Func2, downstream returns final response

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder(expectedMessages(t, input[0])).
			AddFunctionCall("callId2", "Func2", `{"i":42}`).
			NewTurn(expectedMessages(t,
				input[0],
				&message.Message{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
				}},
				&message.Message{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
				}},
			)).
			AddText("World again").
			Build(),
	}

	// Expected output: Func2 call, result, and final response
	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "World again"},
		}},
	}

	invokeAndAssertApprovalWithAgent(t, runner.Run, tools, input, expectedOutput, nil)
}

// TestFunctionInvoking_ApprovedApprovalResponsesWithoutApprovalRequestAreExecuted tests that
// approval responses without preceding approval requests are still executed
func TestFunctionInvoking_ApprovedApprovalResponsesWithoutApprovalRequestAreExecuted(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
		createFunc2(),
	}

	// Input has approval responses but NO approval requests in history
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		message.New(
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalResponseContent{RequestID: "ficc_callId2", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		),
	}

	expectedDownstreamAgentInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

// TestFunctionInvoking_FunctionCallContentIsYieldedImmediatelyIfNoApprovalRequiredWhenStreaming tests that
// function call content is yielded immediately when no approval is required (no approval-required functions)
func TestFunctionInvoking_FunctionCallContentIsYieldedImmediatelyIfNoApprovalRequiredWhenStreaming(t *testing.T) {
	tools := []tool.Tool{
		createFunc1(), // No approval required
		createFunc2(), // No approval required
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	// Multi-round agent: first returns function calls, second returns final response

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder(expectedMessages(t, input[0])).
			AddFunctionCall("callId1", "Func1", "").
			AddFunctionCall("callId2", "Func2", `{"i":42}`).
			NewTurn(expectedMessages(t,
				input[0],
				&message.Message{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
				}},
				&message.Message{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
					&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
				}},
			)).
			AddText("world").
			Build(),
	}

	// Expected output includes function calls, their results, and final response
	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApprovalWithAgent(t, runner.Run, tools, input, expectedOutput, nil)
}

// TestFunctionInvoking_FunctionCallsAreBufferedUntilApprovalRequirementEncounteredWhenStreaming tests that
// when some functions require approval, function calls are buffered and converted to approval requests
func TestFunctionInvoking_FunctionCallsAreBufferedUntilApprovalRequirementEncounteredWhenStreaming(t *testing.T) {
	tools := []tool.Tool{
		createFunc1(),                            // No approval required
		tool.ApprovalRequiredFunc(createFunc2()), // Approval required
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	// Downstream returns function calls
	downstreamAgentOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
	}

	// Since approval is required for at least one function, ALL are converted to approval requests
	expectedOutput := []*agent.ResponseUpdate{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId2", ToolCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`}},
		}},
	}

	invokeAndAssertApproval(t, tools, input, downstreamAgentOutput, expectedOutput, nil, nil)
}

// TestFunctionInvoking_ApprovalRequestWithoutApprovalResponseThrows tests that an approval request
// without a matching approval response throws an error
func TestFunctionInvoking_ApprovalRequestWithoutApprovalResponseThrows(t *testing.T) {
	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(createFunc1()),
		createFunc2(),
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "ficc_callId1", ToolCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		}},
	}

	expectedErrorMsg := "ToolApprovalRequestContent found with ToolCall.CallID(s) 'callId1' that have no matching ToolApprovalResponseContent"

	// Note: We don't pass any downstream client output since the error should occur during approval processing
	expectApprovalError(t, tools, input, expectedErrorMsg)
}

// Helper functions to create test tools
func createFunc1() tool.FuncTool {
	return functool.MustNew(functool.Config{
		Name: "Func1",
	}, func(ctx context.Context, args struct{}) (string, error) {
		return "Result 1", nil
	})
}

func createFunc2() tool.FuncTool {
	type Func2Args struct {
		I int `json:"i"`
	}
	return functool.MustNew(functool.Config{
		Name: "Func2",
	}, func(ctx context.Context, args Func2Args) (string, error) {
		return fmt.Sprintf("Result 2: %d", args.I), nil
	})
}
