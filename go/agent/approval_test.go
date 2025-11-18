// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/internal/agenttest"
	"github.com/microsoft/agent-framework/go/memory/inmemory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
)

// TestAgent_ToolApprovalRequest tests that tools requiring approval generate approval requests.
func TestAgent_ToolApprovalRequest(t *testing.T) {
	client, a := agenttest.NewAgent()

	toolCalls := []*message.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "delete_file",
			Arguments: `{"path": "/important/file.txt"}`,
		},
	}
	client.WithToolCalls(toolCalls, "File deleted")

	// Create a tool that requires approval
	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "deleted", nil
		},
	})

	resp, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete the file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Should have a user input request for approval
	if len(resp.UserInputRequest) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(resp.UserInputRequest))
	}

	// Verify it's a function approval request
	approvalReq, ok := resp.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	if !ok {
		t.Fatalf("expected FunctionApprovalRequestContent, got %T", resp.UserInputRequest[0])
	}

	if approvalReq.FunctionCall.Name != "delete_file" {
		t.Errorf("expected tool name 'delete_file', got %q", approvalReq.FunctionCall.Name)
	}

	if approvalReq.FunctionCall.CallID != "call-1" {
		t.Errorf("expected call ID 'call-1', got %q", approvalReq.FunctionCall.CallID)
	}

	// ID should be in the format "approval-<callID>"
	expectedID := "approval-call-1"
	if approvalReq.ID != expectedID {
		t.Errorf("expected approval ID %q, got %q", expectedID, approvalReq.ID)
	}
}

// TestAgent_ToolApprovalApproved tests that approved tool calls are executed.
func TestAgent_ToolApprovalApproved(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return tool call
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "delete_file",
							Arguments: `{"path": "/important/file.txt"}`,
						},
					}},
				},
			}, nil
		}
		// Second call: return final response after tool execution
		return &agent.RunResponse{
			AgentID:    a.ID(),
			ResponseID: "resp-2",
			Messages: []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "File deleted successfully"},
				}},
			},
		}, nil
	}

	deleteCalled := false
	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			deleteCalled = true
			return "deleted", nil
		},
	})

	// First run: get approval request
	resp1, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete the file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(resp1.UserInputRequest) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(resp1.UserInputRequest))
	}

	approvalReq, ok := resp1.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	if !ok {
		t.Fatalf("expected FunctionApprovalRequestContent, got %T", resp1.UserInputRequest[0])
	}

	// Create approval response
	approvalResp := approvalReq.Response(true) // Approve the call

	// Second run: with approval response
	resp2, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{approvalResp},
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Tool should have been called
	if !deleteCalled {
		t.Error("expected delete tool to be called after approval")
	}

	if resp2.String() != "File deleted successfully" {
		t.Errorf("expected 'File deleted successfully', got %q", resp2.String())
	}
}

// TestAgent_ToolApprovalRejected tests that rejected tool calls are not executed.
func TestAgent_ToolApprovalRejected(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return tool call
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "delete_file",
							Arguments: `{"path": "/important/file.txt"}`,
						},
					}},
				},
			}, nil
		}
		// Second call: return final response after rejection
		return &agent.RunResponse{
			AgentID:    a.ID(),
			ResponseID: "resp-2",
			Messages: []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "Operation cancelled"},
				}},
			},
		}, nil
	}

	deleteCalled := false
	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			deleteCalled = true
			return "deleted", nil
		},
	})

	// First run: get approval request
	resp1, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete the file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(resp1.UserInputRequest) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(resp1.UserInputRequest))
	}

	approvalReq, ok := resp1.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	if !ok {
		t.Fatalf("expected FunctionApprovalRequestContent, got %T", resp1.UserInputRequest[0])
	}

	// Create rejection response
	approvalResp := approvalReq.Response(false) // Reject the call

	// Second run: with rejection response
	resp2, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{approvalResp},
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Tool should NOT have been called
	if deleteCalled {
		t.Error("expected delete tool NOT to be called after rejection")
	}

	if resp2.String() != "Operation cancelled" {
		t.Errorf("expected 'Operation cancelled', got %q", resp2.String())
	}
}

// TestAgent_MultipleToolApprovals tests handling of multiple tool calls requiring approval.
func TestAgent_MultipleToolApprovals(t *testing.T) {
	client, a := agenttest.NewAgent()

	toolCalls := []*message.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "delete_file",
			Arguments: `{"path": "/file1.txt"}`,
		},
		{
			CallID:    "call-2",
			Name:      "delete_file",
			Arguments: `{"path": "/file2.txt"}`,
		},
	}
	client.WithToolCalls(toolCalls, "Files processed")

	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "deleted", nil
		},
	})

	resp, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete files"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Should have 2 approval requests
	if len(resp.UserInputRequest) != 2 {
		t.Fatalf("expected 2 approval requests, got %d", len(resp.UserInputRequest))
	}

	// Verify both are function approval requests
	for i, req := range resp.UserInputRequest {
		approvalReq, ok := req.(*message.FunctionApprovalRequestContent)
		if !ok {
			t.Fatalf("request %d: expected FunctionApprovalRequestContent, got %T", i, req)
		}
		if approvalReq.FunctionCall.Name != "delete_file" {
			t.Errorf("request %d: expected tool name 'delete_file', got %q", i, approvalReq.FunctionCall.Name)
		}
	}
}

// TestAgent_MixedToolApprovals tests handling of tools with and without approval requirements.
// When multiple tools are called together, non-approval tools execute immediately while
// approval-required tools generate requests. The agent continues processing until it
// encounters only approval-required tools.
func TestAgent_MixedToolApprovals(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return both approved and non-approved tool calls
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "read_file",
							Arguments: `{"path": "/file.txt"}`,
						},
						&message.FunctionCallContent{
							CallID:    "call-2",
							Name:      "delete_file",
							Arguments: `{"path": "/file.txt"}`,
						},
					}},
				},
			}, nil
		}
		// Second call: after read_file executes, return a tool call that needs approval
		// This is the call where the agent will stop and ask for approval
		if callCount == 2 {
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-2",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-3",
							Name:      "delete_backup",
							Arguments: `{"path": "/backup.txt"}`,
						},
					}},
				},
			}, nil
		}
		// Third call: return final response after approval
		return &agent.RunResponse{
			AgentID:    a.ID(),
			ResponseID: "resp-3",
			Messages: []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "Operations completed"},
				}},
			},
		}, nil
	}

	readCalled := false
	readTool := &agenttest.Tool{
		Name: "read_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			readCalled = true
			return "file content", nil
		},
	}

	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "deleted", nil
		},
	})

	deleteBackupTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_backup",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "backup deleted", nil
		},
	})

	resp, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{readTool, deleteTool, deleteBackupTool},
	}}, message.NewText("Process file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// read_file should have been called immediately (no approval needed)
	// delete_file should NOT have been called (waiting for approval in first batch)
	if !readCalled {
		t.Error("expected read_file to be called without approval")
	}

	// Should have 1 approval request (for delete_file from first call)
	// The delete_file was in the first batch with read_file. read_file executed
	// immediately (no approval), and delete_file generated an approval request.
	// With mixed tool results and approval requests, the agent stops and returns both.
	if len(resp.UserInputRequest) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(resp.UserInputRequest))
	}

	approvalReq, ok := resp.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	if !ok {
		t.Fatalf("expected FunctionApprovalRequestContent, got %T", resp.UserInputRequest[0])
	}

	if approvalReq.FunctionCall.Name != "delete_file" {
		t.Errorf("expected approval for 'delete_file', got %q", approvalReq.FunctionCall.Name)
	}
}

// TestAgent_MixedToolResultsAndApprovalRequests tests that when both tool results and
// approval requests exist, both are properly handled: tool results are added to thread,
// approval requests are stored in thread history, and agent stops to wait for approval.
func TestAgent_MixedToolResultsAndApprovalRequests(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return multiple tool calls - some need approval, some don't
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "get_weather",
							Arguments: `{"city": "Seattle"}`,
						},
						&message.FunctionCallContent{
							CallID:    "call-2",
							Name:      "send_email",
							Arguments: `{"to": "user@example.com", "subject": "Weather"}`,
						},
						&message.FunctionCallContent{
							CallID:    "call-3",
							Name:      "read_sensor",
							Arguments: `{"sensor_id": "temp1"}`,
						},
					}},
				},
			}, nil
		}
		t.Fatalf("unexpected call count: %d", callCount)
		return nil, nil
	}

	weatherCalled := false
	weatherTool := &agenttest.Tool{
		Name: "get_weather",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			weatherCalled = true
			return "Sunny, 72°F", nil
		},
	}

	emailTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "send_email",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "email sent", nil
		},
	})

	sensorCalled := false
	sensorTool := &agenttest.Tool{
		Name: "read_sensor",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			sensorCalled = true
			return "23.5°C", nil
		},
	}

	thread := a.NewThread()
	resp, err := a.Run(&agent.RunContext{
		Context: context.Background(),
		Thread:  thread,
		Options: &agent.RunOptions{
			Tools: []tool.Tool{weatherTool, emailTool, sensorTool},
		},
	}, message.NewText("Get weather and send email"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Non-approval tools should have been called
	if !weatherCalled {
		t.Error("expected get_weather to be called")
	}
	if !sensorCalled {
		t.Error("expected read_sensor to be called")
	}

	// Should have 1 approval request for send_email
	if len(resp.UserInputRequest) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(resp.UserInputRequest))
	}

	approvalReq, ok := resp.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	if !ok {
		t.Fatalf("expected FunctionApprovalRequestContent, got %T", resp.UserInputRequest[0])
	}

	if approvalReq.FunctionCall.Name != "send_email" {
		t.Errorf("expected approval for 'send_email', got %q", approvalReq.FunctionCall.Name)
	}

	// Verify thread history contains FunctionApprovalRequestContent, not FunctionCallContent
	threadMessages := thread.(*inmemory.Thread).Messages

	// Find the assistant message with the tool calls
	var assistantMsg *message.Message
	for _, msg := range threadMessages {
		if msg.Role == message.RoleAssistant {
			// Check if this message has function calls/approvals
			for _, c := range msg.Contents {
				if _, ok := c.(*message.FunctionCallContent); ok {
					assistantMsg = msg
					break
				}
				if _, ok := c.(*message.FunctionApprovalRequestContent); ok {
					assistantMsg = msg
					break
				}
			}
			if assistantMsg != nil {
				break
			}
		}
	}

	if assistantMsg == nil {
		t.Fatal("expected to find assistant message with tool calls in thread")
	}

	// Count FunctionCallContent vs FunctionApprovalRequestContent
	var funcCallCount, approvalReqCount int
	for _, c := range assistantMsg.Contents {
		if _, ok := c.(*message.FunctionCallContent); ok {
			funcCallCount++
		}
		if _, ok := c.(*message.FunctionApprovalRequestContent); ok {
			approvalReqCount++
		}
	}

	// Should have 2 FunctionCallContent (for tools that don't need approval)
	// and 1 FunctionApprovalRequestContent (for send_email)
	if funcCallCount != 2 {
		t.Errorf("expected 2 FunctionCallContent in thread, got %d", funcCallCount)
	}
	if approvalReqCount != 1 {
		t.Errorf("expected 1 FunctionApprovalRequestContent in thread, got %d", approvalReqCount)
	}

	// Verify tool results message exists
	var toolResultMsg *message.Message
	for _, msg := range threadMessages {
		if msg.Role == message.RoleTool {
			toolResultMsg = msg
			break
		}
	}

	if toolResultMsg == nil {
		t.Fatal("expected to find tool result message in thread")
	}

	// Should have 2 tool results (weather and sensor)
	var resultCount int
	for _, c := range toolResultMsg.Contents {
		if _, ok := c.(*message.FunctionResultContent); ok {
			resultCount++
		}
	}

	if resultCount != 2 {
		t.Errorf("expected 2 FunctionResultContent in thread, got %d", resultCount)
	}
}

// TestAgent_ApprovalWithToolError tests that tool errors are handled correctly after approval.
func TestAgent_ApprovalWithToolError(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return tool call
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "delete_file",
							Arguments: `{"path": "/file.txt"}`,
						},
					}},
				},
			}, nil
		}
		// Second call: return final response after tool error
		return &agent.RunResponse{
			AgentID:    a.ID(),
			ResponseID: "resp-2",
			Messages: []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "Error occurred during deletion"},
				}},
			},
		}, nil
	}

	expectedErr := errors.New("permission denied")
	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return nil, expectedErr
		},
	})

	// First run: get approval request
	resp1, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete the file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	approvalReq := resp1.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	approvalResp := approvalReq.Response(true)

	// Second run: with approval response
	resp2, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{approvalResp},
	})

	if err != nil {
		t.Fatalf("expected no error from agent, got: %v", err)
	}

	// Should have final response despite tool error
	if resp2.String() != "Error occurred during deletion" {
		t.Errorf("expected error handling response, got %q", resp2.String())
	}
}

// TestAgent_ApprovalWithToolNotFound tests handling when approved tool no longer exists.
func TestAgent_ApprovalWithToolNotFound(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return tool call
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "delete_file",
							Arguments: `{}`,
						},
					}},
				},
			}, nil
		}
		// Second call: return final response
		return &agent.RunResponse{
			AgentID:    a.ID(),
			ResponseID: "resp-2",
			Messages: []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "Tool not available"},
				}},
			},
		}, nil
	}

	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "deleted", nil
		},
	})

	// First run: get approval request
	resp1, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	approvalReq := resp1.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	approvalResp := approvalReq.Response(true)

	// Second run: with approval response but WITHOUT the tool in options
	// This simulates a scenario where the tool is no longer available
	resp2, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{}, // No tools
	}}, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{approvalResp},
	})

	if err != nil {
		t.Fatalf("expected no error from agent, got: %v", err)
	}

	// Should handle gracefully
	if resp2 == nil {
		t.Fatal("expected response")
	}
}

// TestAgent_ApprovalRequestResponseRoundTrip tests the approval request/response creation.
func TestAgent_ApprovalRequestResponseRoundTrip(t *testing.T) {
	funcCall := &message.FunctionCallContent{
		CallID:    "call-123",
		Name:      "test_tool",
		Arguments: `{"arg": "value"}`,
	}

	// Create approval request
	approvalReq := &message.FunctionApprovalRequestContent{
		ID:           "approval-123",
		FunctionCall: funcCall,
	}

	// Create approval response (approved)
	approvalRespApproved := approvalReq.Response(true)
	if approvalRespApproved.ID != approvalReq.ID {
		t.Errorf("expected ID %q, got %q", approvalReq.ID, approvalRespApproved.ID)
	}
	if !approvalRespApproved.Approved {
		t.Error("expected approval to be true")
	}
	if approvalRespApproved.FunctionCall != funcCall {
		t.Error("expected function call to match")
	}

	// Create approval response (rejected)
	approvalRespRejected := approvalReq.Response(false)
	if approvalRespRejected.Approved {
		t.Error("expected approval to be false")
	}
}

// TestAgent_ApprovalStreamingToolCalls tests approval workflow with streaming.
func TestAgent_ApprovalStreamingToolCalls(t *testing.T) {
	client, a := agenttest.NewAgent()

	toolCalls := []*message.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "delete_file",
			Arguments: `{}`,
		},
	}
	client.WithStreamingToolCalls(toolCalls, "Done")

	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "deleted", nil
		},
	})

	var updates []*agent.RunResponseUpdate
	var approvalRequests []message.Content
	for update, err := range a.RunStream(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete file")) {
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		updates = append(updates, update)
		approvalRequests = append(approvalRequests, update.UserInputRequest...)
	}

	// Should have received updates
	if len(updates) == 0 {
		t.Fatal("expected updates")
	}

	// Look for approval request content
	foundApprovalRequest := false
	for _, req := range approvalRequests {
		if approvalReq, ok := req.(*message.FunctionApprovalRequestContent); ok {
			if approvalReq.FunctionCall.Name == "delete_file" {
				foundApprovalRequest = true
				break
			}
		}
	}

	if !foundApprovalRequest {
		t.Error("expected to find approval request for delete_file in streaming updates")
	}
}

// TestAgent_DuplicateApprovalRequestHandling tests that duplicate approval requests are handled.
func TestAgent_DuplicateApprovalRequestHandling(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return tool call
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "test_tool",
							Arguments: `{}`,
						},
					}},
				},
			}, nil
		}
		// Second call: return final response
		return &agent.RunResponse{
			AgentID:    a.ID(),
			ResponseID: "resp-2",
			Messages: []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "Done"},
				}},
			},
		}, nil
	}

	testTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "test_tool",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "result", nil
		},
	})

	// First run: get approval request
	resp1, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{testTool},
	}}, message.NewText("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	approvalReq := resp1.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	approvalResp := approvalReq.Response(true)

	// Second run: send approval response along with the original function call
	// This tests that duplicate handling works correctly
	resp2, err := a.Run(&agent.RunContext{Context: context.Background(), Options: &agent.RunOptions{
		Tools: []tool.Tool{testTool},
	}},
		&message.Message{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				approvalReq.FunctionCall, // Original call
			},
		},
		&message.Message{
			Role:     message.RoleUser,
			Contents: []message.Content{approvalResp}, // Approval response
		},
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp2.String() != "Done" {
		t.Errorf("expected 'Done', got %q", resp2.String())
	}
}

// TestAgent_ApprovalRequestInThreadHistory verifies that approval requests
// are properly added to thread history as FunctionApprovalRequestContent.
func TestAgent_ApprovalRequestInThreadHistory(t *testing.T) {
	client, a := agenttest.NewAgent()

	toolCalls := []*message.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "delete_file",
			Arguments: `{"path": "/file.txt"}`,
		},
	}
	client.WithToolCalls(toolCalls, "File deleted")

	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "deleted", nil
		},
	})

	thread := a.NewThread()

	// First run: get approval request
	resp1, err := a.Run(&agent.RunContext{Context: context.Background(), Thread: thread, Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete the file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(resp1.UserInputRequest) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(resp1.UserInputRequest))
	}

	approvalReq, ok := resp1.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	if !ok {
		t.Fatalf("expected FunctionApprovalRequestContent, got %T", resp1.UserInputRequest[0])
	}

	// Verify the approval request is in the response messages, not FunctionCallContent
	if len(resp1.Messages) != 1 {
		t.Fatalf("expected 1 message in response, got %d", len(resp1.Messages))
	}

	foundApprovalRequestInMessage := false
	for _, c := range resp1.Messages[0].Contents {
		if req, ok := c.(*message.FunctionApprovalRequestContent); ok {
			if req.ID == approvalReq.ID {
				foundApprovalRequestInMessage = true
				if req.FunctionCall.Name != "delete_file" {
					t.Errorf("expected function name 'delete_file', got %q", req.FunctionCall.Name)
				}
				break
			}
		}
		if fc, ok := c.(*message.FunctionCallContent); ok {
			t.Errorf("found FunctionCallContent in message instead of FunctionApprovalRequestContent: %+v", fc)
		}
	}

	if !foundApprovalRequestInMessage {
		t.Error("expected FunctionApprovalRequestContent in response message, but not found")
	}

	// Verify the thread contains the approval request
	threadMessages := thread.(*inmemory.Thread).Messages
	foundApprovalRequestInThread := false
	for _, msg := range threadMessages {
		for _, c := range msg.Contents {
			if req, ok := c.(*message.FunctionApprovalRequestContent); ok {
				if req.ID == approvalReq.ID {
					foundApprovalRequestInThread = true
					break
				}
			}
		}
	}

	if !foundApprovalRequestInThread {
		t.Error("expected FunctionApprovalRequestContent in thread history, but not found")
	}
}

// TestAgent_ApprovalRequestRestoredAsFunctionCall verifies that when processing
// an approval response, the approval request is properly restored as a FunctionCallContent.
func TestAgent_ApprovalRequestRestoredAsFunctionCall(t *testing.T) {
	client, a := agenttest.NewAgent()

	callCount := 0
	var secondCallMessages []*message.Message
	client.RunFunc = func(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			// First call: return tool call requiring approval
			return &agent.RunResponse{
				AgentID:    a.ID(),
				ResponseID: "resp-1",
				Messages: []*message.Message{
					{Role: message.RoleAssistant, Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-1",
							Name:      "delete_file",
							Arguments: `{"path": "/file.txt"}`,
						},
					}},
				},
			}, nil
		}
		// Second call: should see the function call restored from approval request
		secondCallMessages = messages
		return &agent.RunResponse{
			AgentID:    a.ID(),
			ResponseID: "resp-2",
			Messages: []*message.Message{
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "File deleted"},
				}},
			},
		}, nil
	}

	toolCalled := false
	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			toolCalled = true
			return "deleted", nil
		},
	})

	thread := a.NewThread()

	// First run: get approval request
	resp1, err := a.Run(&agent.RunContext{Context: context.Background(), Thread: thread, Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete the file"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	approvalReq := resp1.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
	approvalResp := approvalReq.Response(true)

	// Second run: with approval response
	_, err = a.Run(&agent.RunContext{Context: context.Background(), Thread: thread, Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{approvalResp},
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify the tool was called
	if !toolCalled {
		t.Error("expected tool to be called after approval")
	}

	// Verify the second call received FunctionCallContent (restored from approval request)
	foundFunctionCall := false
	foundFunctionResult := false
	for _, msg := range secondCallMessages {
		for _, c := range msg.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				if fc.Name == "delete_file" && fc.CallID == "call-1" {
					foundFunctionCall = true
				}
			}
			if fr, ok := c.(*message.FunctionResultContent); ok {
				if fr.CallID == "call-1" {
					foundFunctionResult = true
				}
			}
		}
	}

	if !foundFunctionCall {
		t.Error("expected FunctionCallContent (restored from approval request) in second call messages")
	}
	if !foundFunctionResult {
		t.Error("expected FunctionResultContent (from tool execution) in second call messages")
	}
}

// TestAgent_ApprovalRequestInThreadHistoryStreaming verifies that approval requests
// are properly added to thread history in streaming mode.
func TestAgent_ApprovalRequestInThreadHistoryStreaming(t *testing.T) {
	client, a := agenttest.NewAgent()

	toolCalls := []*message.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "delete_file",
			Arguments: `{}`,
		},
	}
	client.WithStreamingToolCalls(toolCalls, "Done")

	deleteTool := tool.ApprovalRequiredFunc(&agenttest.Tool{
		Name: "delete_file",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "deleted", nil
		},
	})

	thread := a.NewThread()

	var approvalRequests []message.Content
	for update, err := range a.RunStream(&agent.RunContext{Context: context.Background(), Thread: thread, Options: &agent.RunOptions{
		Tools: []tool.Tool{deleteTool},
	}}, message.NewText("Delete file")) {
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		approvalRequests = append(approvalRequests, update.UserInputRequest...)
	}

	if len(approvalRequests) == 0 {
		t.Fatal("expected approval requests in streaming mode")
	}

	// Verify the thread contains FunctionApprovalRequestContent, not FunctionCallContent
	threadMessages := thread.(*inmemory.Thread).Messages
	foundApprovalRequest := false
	foundIncorrectFunctionCall := false

	for _, msg := range threadMessages {
		if msg.Role != message.RoleAssistant {
			continue
		}
		for _, c := range msg.Contents {
			if req, ok := c.(*message.FunctionApprovalRequestContent); ok {
				if req.FunctionCall.Name == "delete_file" {
					foundApprovalRequest = true
				}
			}
			// Check for FunctionCallContent that should have been replaced
			if fc, ok := c.(*message.FunctionCallContent); ok {
				if fc.Name == "delete_file" && fc.CallID == "call-1" {
					// This should have been replaced with FunctionApprovalRequestContent
					foundIncorrectFunctionCall = true
				}
			}
		}
	}

	if !foundApprovalRequest {
		t.Error("expected FunctionApprovalRequestContent in thread history for streaming mode")
	}
	if foundIncorrectFunctionCall {
		t.Error("found FunctionCallContent in thread history that should have been replaced with FunctionApprovalRequestContent")
	}
}
