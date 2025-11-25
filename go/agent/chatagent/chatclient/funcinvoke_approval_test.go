// Copyright (c) Microsoft. All rights reserved.

package chatclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework/go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

// approvalTestClient is a test client for approval tests that supports multi-round responses
type approvalTestClient struct {
	responses         [][]*message.Message // Queue of response messages for multiple rounds
	expectedInputs    [][]*message.Message // Queue of expected input messages for validation
	currentRound      int
	streamingCallback func(messages []*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error]
}

func (c *approvalTestClient) Response(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
	if c.currentRound >= len(c.responses) {
		return nil, fmt.Errorf("unexpected call to Response, round %d, expected %d rounds", c.currentRound, len(c.responses))
	}

	response := c.responses[c.currentRound]
	c.currentRound++

	return &chatclient.ChatResponse{Messages: response}, nil
}

func (c *approvalTestClient) StreamingResponse(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
	if c.streamingCallback != nil {
		return c.streamingCallback(messages)
	}

	// Default streaming implementation: convert Response to streaming
	return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
		resp, err := c.Response(ctx, opts, messages...)
		if err != nil {
			yield(nil, err)
			return
		}

		for _, msg := range resp.Messages {
			for _, content := range msg.Contents {
				update := &chatclient.ChatResponseUpdate{
					Role:     msg.Role,
					Contents: []message.Content{content},
				}
				if !yield(update, nil) {
					return
				}
			}
		}
	}
}

// invokeAndAssertApproval is the main helper for non-streaming approval tests
func invokeAndAssertApproval(t *testing.T, options *chatclient.ChatOptions, input []*message.Message,
	downstreamClientOutput []*message.Message, expectedOutput []*message.Message,
	expectedDownstreamClientInput []*message.Message, additionalTools []tool.Tool) {

	client := &approvalTestClient{
		responses: [][]*message.Message{downstreamClientOutput},
	}

	if expectedDownstreamClientInput != nil {
		client.expectedInputs = [][]*message.Message{expectedDownstreamClientInput}
	}

	invokeAndAssertApprovalWithClient(t, client, options, input, expectedOutput, additionalTools)
}

// invokeAndAssertApprovalWithClient performs the actual test execution
func invokeAndAssertApprovalWithClient(t *testing.T, innerClient chatclient.Client,
	options *chatclient.ChatOptions, input []*message.Message,
	expectedOutput []*message.Message, additionalTools []tool.Tool) {

	functionInvokingOptions := &chatclient.FunctionInvokingOptions{}
	if additionalTools != nil {
		functionInvokingOptions.AdditionalTools = additionalTools
	}

	client := chatclient.NewFunctionInvoking(innerClient, functionInvokingOptions)
	ctx := context.Background()

	response, err := client.Response(ctx, options, input...)
	if err != nil {
		t.Fatalf("Response failed: %v", err)
	}

	assertMessageListsEqual(t, expectedOutput, response.Messages)
}

// invokeAndAssertStreamingApproval is the helper for streaming approval tests
func invokeAndAssertStreamingApproval(t *testing.T, options *chatclient.ChatOptions, input []*message.Message,
	downstreamClientOutput []*message.Message, expectedOutput []*message.Message,
	expectedDownstreamClientInput []*message.Message, additionalTools []tool.Tool) {

	client := &approvalTestClient{
		responses: [][]*message.Message{downstreamClientOutput},
	}

	if expectedDownstreamClientInput != nil {
		client.expectedInputs = [][]*message.Message{expectedDownstreamClientInput}
	}

	invokeAndAssertStreamingApprovalWithClient(t, client, options, input, expectedOutput, additionalTools)
}

// invokeAndAssertStreamingApprovalWithClient performs streaming test execution
func invokeAndAssertStreamingApprovalWithClient(t *testing.T, innerClient chatclient.Client,
	options *chatclient.ChatOptions, input []*message.Message,
	expectedOutput []*message.Message, additionalTools []tool.Tool) {

	functionInvokingOptions := &chatclient.FunctionInvokingOptions{}
	if additionalTools != nil {
		functionInvokingOptions.AdditionalTools = additionalTools
	}

	client := chatclient.NewFunctionInvoking(innerClient, functionInvokingOptions)
	ctx := context.Background()

	// Collect all streaming updates into messages

	var updates []*chatclient.ChatResponseUpdate
	for update, err := range client.StreamingResponse(ctx, options, input...) {
		if err != nil {
			t.Fatalf("StreamingResponse failed: %v", err)
		}
		updates = append(updates, update)
	}
	resp := chatclient.NewChatResponseFromUpdates(updates)
	assertMessageListsEqual(t, expectedOutput, resp.Messages)
}

// expectApprovalError expects an error to be thrown during invocation
func expectApprovalError(t *testing.T, options *chatclient.ChatOptions, input []*message.Message,
	downstreamClientOutput []*message.Message, expectedErrorMsg string, additionalTools []tool.Tool) {

	client := &approvalTestClient{
		responses: [][]*message.Message{downstreamClientOutput},
	}

	functionInvokingOptions := &chatclient.FunctionInvokingOptions{}
	if additionalTools != nil {
		functionInvokingOptions.AdditionalTools = additionalTools
	}

	fiClient := chatclient.NewFunctionInvoking(client, functionInvokingOptions)
	ctx := context.Background()

	_, err := fiClient.Response(ctx, options, input...)
	if err == nil {
		t.Fatalf("Expected error with message %q, but got nil", expectedErrorMsg)
	}

	if err.Error() != expectedErrorMsg {
		t.Fatalf("Expected error message %q, got %q", expectedErrorMsg, err.Error())
	}
}

// expectStreamingApprovalError expects an error during streaming invocation
func expectStreamingApprovalError(t *testing.T, options *chatclient.ChatOptions, input []*message.Message,
	downstreamClientOutput []*message.Message, expectedErrorMsg string, additionalTools []tool.Tool) {

	client := &approvalTestClient{
		responses: [][]*message.Message{downstreamClientOutput},
	}

	functionInvokingOptions := &chatclient.FunctionInvokingOptions{}
	if additionalTools != nil {
		functionInvokingOptions.AdditionalTools = additionalTools
	}

	fiClient := chatclient.NewFunctionInvoking(client, functionInvokingOptions)
	ctx := context.Background()

	var lastErr error
	for _, err := range fiClient.StreamingResponse(ctx, options, input...) {
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
		{"with_chat_options_tools", false},
		{"with_additional_tools", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := []tool.Tool{
				tool.ApprovalRequiredFunc(createFunc1()),
				tool.ApprovalRequiredFunc(createFunc2()),
			}

			options := &chatclient.ChatOptions{Tools: tools}
			if tt.useAdditionalTools {
				options.Tools = nil
			}

			input := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}

			downstreamClientOutput := []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
				}},
			}

			expectedOutput := []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
					&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
				}},
			}

			additionalTools := []tool.Tool(nil)
			if tt.useAdditionalTools {
				additionalTools = tools
			}

			invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, additionalTools)
			invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, additionalTools)
		})
	}
}

// TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAnyRequireApproval tests that
// all function calls are replaced with approval requests when any function requires approval
func TestFunctionInvoking_AllFunctionCallsReplacedWithApprovalsWhenAnyRequireApproval(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
	}

	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, nil)
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

			options := &chatclient.ChatOptions{
				Tools: chatOptionsTools,
			}

			input := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}

			downstreamClientOutput := []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
				}},
			}

			expectedOutput := []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
					&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
				}},
			}

			invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, additionalTools)
			invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, additionalTools)
		})
	}
}

// TestFunctionInvoking_ApprovedApprovalResponsesAreExecuted tests that approved approval responses are executed
func TestFunctionInvoking_ApprovedApprovalResponsesAreExecuted(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
	}

	// Input includes: user message, approval requests (from previous turn), and approval responses
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
	}

	// Downstream client receives: user message, function calls (not approval requests), and function results
	expectedDownstreamClientInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	// Downstream client returns a simple text response
	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	// Final output includes: function calls, function results, and the assistant response
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_ApprovedApprovalResponsesFromSeparateFCCMessagesAreExecuted tests that approved approval responses
// from separate assistant messages (each with their own MessageId) are properly aggregated and executed
func TestFunctionInvoking_ApprovedApprovalResponsesFromSeparateFCCMessagesAreExecuted(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
	}

	// Input has approval requests in separate assistant messages with different IDs,
	// and approval responses in separate user messages
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		}},
		{Role: message.RoleAssistant, ID: "resp2", Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		),
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
	}

	// Downstream client receives function calls with their original message IDs preserved
	expectedDownstreamClientInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
		}},
		{Role: message.RoleAssistant, ID: "resp2", Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "world"}}},
	}

	// Output includes the function calls, results, and downstream response
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
		}},
		{Role: message.RoleAssistant, ID: "resp2", Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "world"}}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_RejectedApprovalResponsesAreFailed tests that rejected approval responses fail with error messages
func TestFunctionInvoking_RejectedApprovalResponsesAreFailed(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: false, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: false, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
	}

	expectedDownstreamClientInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Tool call invocation was rejected by user."},
			&message.FunctionResultContent{CallID: "callId2", Result: "Error: Tool call invocation was rejected by user."},
		}},
	}

	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Tool call invocation was rejected by user."},
			&message.FunctionResultContent{CallID: "callId2", Result: "Error: Tool call invocation was rejected by user."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_MixedApprovedAndRejectedApprovalResponsesAreExecutedAndFailed tests that
// mixed approved and rejected approval responses are handled correctly
func TestFunctionInvoking_MixedApprovedAndRejectedApprovalResponsesAreExecutedAndFailed(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: false, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
	}

	expectedDownstreamClientInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Tool call invocation was rejected by user."},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	// Non-streaming output: separate Tool messages for each function result
	expectedOutputNonStreaming := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Tool call invocation was rejected by user."},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	// Streaming output: combined Tool message with both function results
	expectedOutputStreaming := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Error: Tool call invocation was rejected by user."},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutputNonStreaming, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutputStreaming, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_ApprovedInputsAreExecutedAndFunctionResultsAreConverted tests that
// approved inputs are executed and function results are converted back to approval requests
func TestFunctionInvoking_ApprovedInputsAreExecutedAndFunctionResultsAreConverted(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			createFunc1(),
			tool.ApprovalRequiredFunc(createFunc2()),
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
	}

	expectedDownstreamClientInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	// Downstream client returns a new FunctionCallContent for Func2 with different arguments
	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":3}`)},
		}},
	}

	// Output includes executed functions and the new approval request for the new Func2 call
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":3}`)}},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_AlreadyExecutedApprovalsAreIgnored tests that already executed approvals
// (ones that have both FunctionCallContent and FunctionResultContent in history) are ignored
func TestFunctionInvoking_AlreadyExecutedApprovalsAreIgnored(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			createFunc1(),
			tool.ApprovalRequiredFunc(createFunc2()),
		},
	}

	// Input includes history with already-executed approvals and a new approval to execute
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		// Previous turn: approval requests
		{Role: message.RoleAssistant, ID: "resp1", Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
		// Previous turn: approval responses
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
		// Previous turn: already executed - function calls
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		// Previous turn: already executed - function results
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		// Current turn: new approval request
		{Role: message.RoleAssistant, ID: "resp2", Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId3", FunctionCall: &message.FunctionCallContent{CallID: "callId3", Name: "Func1"}},
		}},
		// Current turn: new approval response
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId3", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId3", Name: "Func1"}},
		),
	}

	// Downstream client should receive history with already-executed items and the new function call
	expectedDownstreamClientInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "Func1"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Result 1"},
		}},
	}

	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "World"},
		}},
	}

	// Output only includes the newly executed approval (not the already-executed ones from history)
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "Func1"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Result 1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "World"},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_MixedApprovalRequiredToolsWithNonApprovalRequiringFunctionCall tests that
// when only some tools require approval, non-approval-requiring function calls are executed immediately
// and don't trigger approval requests for all calls
func TestFunctionInvoking_MixedApprovalRequiredToolsWithNonApprovalRequiringFunctionCall(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()), // Func1 requires approval
			createFunc2(),                            // Func2 does NOT require approval
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	// Multi-round client:
	// Round 1: Downstream returns only Func2 call (no approval required)
	// Round 2: After executing Func2, downstream returns final response
	client := &approvalTestClient{
		responses: [][]*message.Message{
			// Round 1: Only Func2 is called (doesn't require approval, so it's executed immediately)
			{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
				}},
			},
			// Round 2: Final response after Func2 execution
			{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "World again"},
				}},
			},
		},
	}

	// Expected output: Func2 call, result, and final response
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "World again"},
		}},
	}

	invokeAndAssertApprovalWithClient(t, client, options, input, expectedOutput, nil)

	// Reset for streaming test
	client.currentRound = 0
	invokeAndAssertStreamingApprovalWithClient(t, client, options, input, expectedOutput, nil)
}

// TestFunctionInvoking_ApprovedApprovalResponsesWithoutApprovalRequestAreExecuted tests that
// approval responses without preceding approval requests are still executed
func TestFunctionInvoking_ApprovedApprovalResponsesWithoutApprovalRequestAreExecuted(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
	}

	// Input has approval responses but NO approval requests in history
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
	}

	expectedDownstreamClientInput := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_FunctionCallContentIsNotPassedToDownstreamServiceWithServiceThreads tests that
// when using ConversationId (service threads), FunctionCallContent is not passed to downstream service
func TestFunctionInvoking_FunctionCallContentIsNotPassedToDownstreamServiceWithServiceThreads(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
		ConversationID: "test-conversation",
	}

	// Input has only approval responses (service thread scenario)
	input := []*message.Message{
		message.New(
			&message.FunctionApprovalResponseContent{ID: "callId1", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalResponseContent{ID: "callId2", Approved: true, FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		),
	}

	// With ConversationId, FunctionCallContent should NOT be sent to downstream
	expectedDownstreamClientInput := []*message.Message{
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
	}

	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	// Output includes FunctionCallContent, function results, and assistant response
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, expectedDownstreamClientInput, nil)
}

// TestFunctionInvoking_FunctionCallContentIsYieldedImmediatelyIfNoApprovalRequiredWhenStreaming tests that
// function call content is yielded immediately when no approval is required (no approval-required functions)
func TestFunctionInvoking_FunctionCallContentIsYieldedImmediatelyIfNoApprovalRequiredWhenStreaming(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			createFunc1(), // No approval required
			createFunc2(), // No approval required
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	// Multi-round client: first returns function calls, second returns final response
	client := &approvalTestClient{
		responses: [][]*message.Message{
			// Round 1: Downstream returns function calls
			{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
				}},
			},
			// Round 2: After executing functions, downstream returns final response
			{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "world"},
				}},
			},
		},
	}

	// Expected output includes function calls, their results, and final response
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssertApprovalWithClient(t, client, options, input, expectedOutput, nil)

	// Reset for streaming test
	client.currentRound = 0
	invokeAndAssertStreamingApprovalWithClient(t, client, options, input, expectedOutput, nil)
}

// TestFunctionInvoking_FunctionCallsAreBufferedUntilApprovalRequirementEncounteredWhenStreaming tests that
// when some functions require approval, function calls are buffered and converted to approval requests
func TestFunctionInvoking_FunctionCallsAreBufferedUntilApprovalRequirementEncounteredWhenStreaming(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			createFunc1(),                            // No approval required
			tool.ApprovalRequiredFunc(createFunc2()), // Approval required
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	// Downstream returns function calls
	downstreamClientOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
	}

	// Since approval is required for at least one function, ALL are converted to approval requests
	expectedOutput := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			&message.FunctionApprovalRequestContent{ID: "callId2", FunctionCall: &message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)}},
		}},
	}

	invokeAndAssertApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, nil)
	invokeAndAssertStreamingApproval(t, options, input, downstreamClientOutput, expectedOutput, nil, nil)
}

// TestFunctionInvoking_ApprovalRequestWithoutApprovalResponseThrows tests that an approval request
// without a matching approval response throws an error
func TestFunctionInvoking_ApprovalRequestWithoutApprovalResponseThrows(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			tool.ApprovalRequiredFunc(createFunc1()),
			createFunc2(),
		},
	}

	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionApprovalRequestContent{ID: "callId1", FunctionCall: &message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
		}},
	}

	expectedErrorMsg := "FunctionApprovalRequestContent found with FunctionCall.CallId(s) 'callId1' that have no matching FunctionApprovalResponseContent"

	// Note: We don't pass any downstream client output since the error should occur during approval processing
	expectApprovalError(t, options, input, nil, expectedErrorMsg, nil)
	expectStreamingApprovalError(t, options, input, nil, expectedErrorMsg, nil)
}

// Helper functions to create test tools
func createFunc1() *functool.Tool {
	return functool.MustNew(&functool.Func{Name: "Func1"},
		func(ctx context.Context, args struct{}) (string, error) {
			return "Result 1", nil
		})
}

func createFunc2() *functool.Tool {
	type Func2Args struct {
		I int `json:"i"`
	}
	return functool.MustNew(&functool.Func{Name: "Func2"},
		func(ctx context.Context, args Func2Args) (string, error) {
			return fmt.Sprintf("Result 2: %d", args.I), nil
		})
}
