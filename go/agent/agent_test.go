// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/internal/agenttest"
)

func TestAgent_BasicRun(t *testing.T) {
	client := agenttest.NewClient()

	a := agent.New(client, &agent.Config{
		ID:   "test-agent",
		Name: "Test Agent",
	}, nil)

	resp, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Hello"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response, got nil")
	}

	if client.GetRunCallCount() != 1 {
		t.Errorf("expected 1 call to Run, got %d", client.GetRunCallCount())
	}

	lastCall := client.GetLastRunCall()
	if lastCall == nil {
		t.Fatal("expected last call to be recorded")
	}
	if want := a.ID(); lastCall.Config.ID != want {
		t.Errorf("expected config ID %q, got %q", want, lastCall.Config.ID)
	}
}

func TestAgent_CustomResponse(t *testing.T) {
	client := agenttest.NewClient()

	const respTest = "Custom response text"

	customResponse := &agent.RunResponse{
		AgentID:    "custom-agent",
		ResponseID: "custom-response",
		Messages: []*agent.Message{
			agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respTest}),
		},
	}
	client.SetResponse(customResponse)

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp.Text() != respTest {
		t.Errorf("expected %q, got %q", respTest, resp.Text())
	}
}

func TestAgent_ErrorHandling(t *testing.T) {
	client := agenttest.NewClient()

	expectedError := errors.New("an error")
	client.SetError(expectedError)

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	_, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != expectedError {
		t.Errorf("expected error %v, got %v", expectedError, err)
	}
}

func TestAgent_RunStream(t *testing.T) {
	client := agenttest.NewClient()

	client.SetStreamUpdates([]*agent.RunResponseUpdate{
		{
			Role:     agent.RoleAssistant,
			Contents: []agent.Content{&agent.TextContent{Text: "Hello "}},
		},
		{
			Role:     agent.RoleAssistant,
			Contents: []agent.Content{&agent.TextContent{Text: "world!"}},
		},
	})

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	var receivedUpdates []*agent.RunResponseUpdate
	for update, err := range a.RunStream(t.Context(), nil, nil, agent.NewTextMessage("Test")) {
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		receivedUpdates = append(receivedUpdates, update)
	}

	if len(receivedUpdates) < 1 {
		t.Errorf("expected at least 1 update, got %d", len(receivedUpdates))
	}

	if client.GetRunStreamCallCount() == 0 {
		t.Error("expected RunStream to be called")
	}
}

func TestAgent_ResponseSequence(t *testing.T) {
	client := agenttest.NewClient()

	const respText1 = "First response"
	const respText2 = "Second response"

	responses := []*agent.RunResponse{
		{
			Messages: []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText1})},
		},
		{
			Messages: []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText2})},
		},
	}
	client.WithResponseSequence(responses...)

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp1, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test 1"))
	if err != nil {
		t.Fatalf("expected no error on first call, got: %v", err)
	}
	if resp1.Text() != respText1 {
		t.Errorf("expected %q, got %q", respText1, resp1.Text())
	}

	resp2, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test 2"))
	if err != nil {
		t.Fatalf("expected no error on second call, got: %v", err)
	}
	if resp2.Text() != respText2 {
		t.Errorf("expected %q, got %q", respText2, resp2.Text())
	}
}

func TestAgent_WithToolCalls(t *testing.T) {
	client := agenttest.NewClient()

	const respText = "The weather in Seattle is sunny"

	toolCalls := []*agent.FunctionCallContent{
		{
			Name:      "get_weather",
			Arguments: `{"location": "Seattle"}`,
		},
	}
	client.WithToolCalls(toolCalls, respText)

	tool := &agenttest.Tool{
		NameValue: "get_weather",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return map[string]any{"temperature": "72F", "condition": "sunny"}, nil
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(t.Context(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("What's the weather?"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp.Text() != respText {
		t.Errorf("expected final response %q, got %q", respText, resp.Text())
	}

	if client.GetRunCallCount() < 2 {
		t.Errorf("expected at least 2 calls to Run, got %d", client.GetRunCallCount())
	}

	if tool.CallCount != 1 {
		t.Errorf("expected tool to be called once, got %d", tool.CallCount)
	}
}

func TestAgent_CustomFunction(t *testing.T) {
	client := agenttest.NewClient()

	const respText1 = "Please confirm"
	const respText2 = "Confirmed!"

	callCount := 0
	client.RunFunc = func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			return &agent.RunResponse{
				AgentID:    config.ID,
				ResponseID: "resp-1",
				Messages:   []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText1})},
			}, nil
		}
		return &agent.RunResponse{
			AgentID:    config.ID,
			ResponseID: "resp-2",
			Messages:   []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText2})},
		}, nil
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp1, _ := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test"))
	if resp1.Text() != respText1 {
		t.Errorf("expected %q, got %q", respText1, resp1.Text())
	}

	resp2, _ := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Yes"))
	if resp2.Text() != respText2 {
		t.Errorf("expected %q, got %q", respText2, resp2.Text())
	}
}

// Test Agent methods
func TestAgent_Name(t *testing.T) {
	client := agenttest.NewClient()
	const agentName = "MyAgent"

	a := agent.New(client, &agent.Config{
		ID:   "test-agent",
		Name: agentName,
	}, nil)

	if a.Name() != agentName {
		t.Errorf("expected name %q, got %q", agentName, a.Name())
	}
}

func TestAgent_NewThread(t *testing.T) {
	client := agenttest.NewClient()
	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	thread := a.NewThread()
	if thread == nil {
		t.Fatal("expected thread, got nil")
	}
}

func TestAgent_RunText(t *testing.T) {
	client := agenttest.NewClient()
	const responseText = "Response to hello"

	client.SetResponse(&agent.RunResponse{
		AgentID:    "test-agent",
		ResponseID: "resp-1",
		Messages: []*agent.Message{
			agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: responseText}),
		},
	})

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.RunText(context.Background(), "hello")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp.Text() != responseText {
		t.Errorf("expected %q, got %q", responseText, resp.Text())
	}
}

func TestAgent_SystemInstructions(t *testing.T) {
	client := agenttest.NewClient()
	const sysInstr = "You are a helpful assistant."

	a := agent.New(client, &agent.Config{
		ID:                 "test-agent",
		SystemInstructions: sysInstr,
	}, nil)

	_, err := a.Run(context.Background(), nil, nil, agent.NewTextMessage("Test"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lastCall := client.GetLastRunCall()
	if lastCall == nil {
		t.Fatal("expected last call to be recorded")
	}

	// Verify system message was prepended
	if len(lastCall.Messages) < 2 {
		t.Errorf("expected at least 2 messages (system + user), got %d", len(lastCall.Messages))
	}
	if lastCall.Messages[0].Role != agent.RoleSystem {
		t.Errorf("expected first message to be system role, got %s", lastCall.Messages[0].Role)
	}
	if lastCall.Messages[0].Text() != sysInstr {
		t.Errorf("expected system instruction %q, got %q", sysInstr, lastCall.Messages[0].Text())
	}
}

func TestAgent_WithThread(t *testing.T) {
	client := agenttest.NewClient()
	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	thread := a.NewThread()

	// Add some messages to the thread
	err := thread.Add(context.Background(), agent.NewTextMessage("First message"))
	if err != nil {
		t.Fatalf("expected no error adding to thread, got: %v", err)
	}

	// Run with the thread
	resp, err := a.Run(context.Background(), thread, nil, agent.NewTextMessage("Second message"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response, got nil")
	}

	// Verify the thread now contains messages
	var messageCount int
	for _, err := range thread.All(context.Background()) {
		if err != nil {
			t.Fatalf("expected no error iterating thread, got: %v", err)
		}
		messageCount++
	}

	if messageCount < 2 {
		t.Errorf("expected at least 2 messages in thread, got %d", messageCount)
	}
}

func TestAgent_RunOptionsTemperature(t *testing.T) {
	client := agenttest.NewClient()
	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	temp := 0.7
	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Temperature: &temp,
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lastCall := client.GetLastRunCall()
	if lastCall.Opts == nil {
		t.Fatal("expected opts to be passed")
	}
	if lastCall.Opts.Temperature == nil || *lastCall.Opts.Temperature != temp {
		t.Errorf("expected temperature %v, got %v", temp, lastCall.Opts.Temperature)
	}
}

func TestAgent_RunOptionsMaxTokens(t *testing.T) {
	client := agenttest.NewClient()
	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	maxTokens := 100
	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		MaxTokens: &maxTokens,
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lastCall := client.GetLastRunCall()
	if lastCall.Opts == nil {
		t.Fatal("expected opts to be passed")
	}
	if lastCall.Opts.MaxTokens == nil || *lastCall.Opts.MaxTokens != maxTokens {
		t.Errorf("expected max tokens %v, got %v", maxTokens, lastCall.Opts.MaxTokens)
	}
}

func TestAgent_RunOptionsAdditionalMetadata(t *testing.T) {
	client := agenttest.NewClient()
	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	metadata := map[string]any{
		"custom_key": "custom_value",
		"number":     42,
	}

	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		AdditionalMetadata: metadata,
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lastCall := client.GetLastRunCall()
	if lastCall.Opts == nil || lastCall.Opts.AdditionalMetadata == nil {
		t.Fatal("expected additional metadata to be passed")
	}
	if lastCall.Opts.AdditionalMetadata["custom_key"] != "custom_value" {
		t.Errorf("expected custom_key to be %q, got %v", "custom_value", lastCall.Opts.AdditionalMetadata["custom_key"])
	}
}

func TestAgent_DefaultRunOptions(t *testing.T) {
	client := agenttest.NewClient()
	temp := 0.5
	defaultOpts := &agent.RunOptions{
		Temperature: &temp,
		ToolMode:    agent.ToolModeAuto,
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, defaultOpts)

	_, err := a.Run(context.Background(), nil, nil, agent.NewTextMessage("Test"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lastCall := client.GetLastRunCall()
	if lastCall.Opts == nil {
		t.Fatal("expected opts to be passed")
	}
	if lastCall.Opts.Temperature == nil || *lastCall.Opts.Temperature != temp {
		t.Errorf("expected default temperature %v, got %v", temp, lastCall.Opts.Temperature)
	}
	if lastCall.Opts.ToolMode != agent.ToolModeAuto {
		t.Errorf("expected default tool mode %v, got %v", agent.ToolModeAuto, lastCall.Opts.ToolMode)
	}
}

func TestAgent_RunOptionsMerge(t *testing.T) {
	client := agenttest.NewClient()
	defaultTemp := 0.5
	overrideTemp := 0.8
	defaultOpts := &agent.RunOptions{
		Temperature: &defaultTemp,
		ToolMode:    agent.ToolModeAuto,
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, defaultOpts)

	_, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Temperature: &overrideTemp,
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lastCall := client.GetLastRunCall()
	if lastCall.Opts == nil {
		t.Fatal("expected opts to be passed")
	}
	// Override should take precedence
	if lastCall.Opts.Temperature == nil || *lastCall.Opts.Temperature != overrideTemp {
		t.Errorf("expected override temperature %v, got %v", overrideTemp, lastCall.Opts.Temperature)
	}
	// Default should still be present
	if lastCall.Opts.ToolMode != agent.ToolModeAuto {
		t.Errorf("expected default tool mode %v, got %v", agent.ToolModeAuto, lastCall.Opts.ToolMode)
	}
}

func TestAgent_MaxRetries(t *testing.T) {
	client := agenttest.NewClient()

	callCount := 0
	// Return tool calls on every call except when we've been called 5 times
	client.RunFunc = func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount <= 5 {
			// Return a tool call
			return &agent.RunResponse{
				AgentID:    config.ID,
				ResponseID: "resp",
				Messages: []*agent.Message{
					agent.NewMessage(agent.RoleAssistant, &agent.FunctionCallContent{
						CallID:    "call-1",
						Name:      "test_tool",
						Arguments: `{}`,
					}),
				},
			}, nil
		}
		// After max retries, return final response
		return &agent.RunResponse{
			AgentID:    config.ID,
			ResponseID: "resp-final",
			Messages: []*agent.Message{
				agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "Final response"}),
			},
		}, nil
	}

	tool := &agenttest.Tool{
		NameValue: "test_tool",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "result", nil
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Should have called client 6 times (5 retries + 1 final)
	if callCount != 6 {
		t.Errorf("expected 6 calls, got %d", callCount)
	}

	if resp.Text() != "Final response" {
		t.Errorf("expected final response, got %q", resp.Text())
	}
}

func TestAgent_RunStreamFallback(t *testing.T) {
	// Create a client that doesn't implement streamableClient
	client := &NonStreamingClient{
		response: &agent.RunResponse{
			AgentID:    "test-agent",
			ResponseID: "resp-1",
			Messages: []*agent.Message{
				agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "Fallback response"}),
			},
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	// Should use fallback since client doesn't implement streaming
	var updates []*agent.RunResponseUpdate
	for update, err := range a.RunStream(context.Background(), nil, nil, agent.NewTextMessage("Test")) {
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) < 1 {
		t.Fatal("expected at least one update")
	}

	var text string
	for _, u := range updates {
		text += u.Text()
	}
	if text != "Fallback response" {
		t.Errorf("expected 'Fallback response', got %q", text)
	}
}

// Test Message methods
func TestMessage_Text(t *testing.T) {
	msg := agent.NewMessage(agent.RoleAssistant,
		&agent.TextContent{Text: "Hello "},
		&agent.TextContent{Text: "world!"},
	)

	if msg.Text() != "Hello " {
		t.Errorf("expected first text content 'Hello ', got %q", msg.Text())
	}

	// Test with no text content
	msgNoText := agent.NewMessage(agent.RoleAssistant,
		&agent.FunctionCallContent{Name: "test"},
	)
	if msgNoText.Text() != "" {
		t.Errorf("expected empty string for message without text, got %q", msgNoText.Text())
	}

	// Test with nil message
	var nilMsg *agent.Message
	if nilMsg.Text() != "" {
		t.Errorf("expected empty string for nil message, got %q", nilMsg.Text())
	}
}

// Test RunResponse methods
func TestRunResponse_Text(t *testing.T) {
	resp := &agent.RunResponse{
		AgentID:    "test",
		ResponseID: "resp-1",
		Messages: []*agent.Message{
			agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "First "}),
			agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "Second"}),
		},
	}

	expected := "First Second"
	if resp.Text() != expected {
		t.Errorf("expected %q, got %q", expected, resp.Text())
	}
}

func TestRunResponseUpdate_Text(t *testing.T) {
	update := &agent.RunResponseUpdate{
		AgentID:    "test",
		ResponseID: "resp-1",
		Role:       agent.RoleAssistant,
		Contents: []agent.Content{
			&agent.TextContent{Text: "Part 1 "},
			&agent.TextContent{Text: "Part 2"},
		},
	}

	expected := "Part 1 Part 2"
	if update.Text() != expected {
		t.Errorf("expected %q, got %q", expected, update.Text())
	}
}

// Test FunctionCallContent
func TestFunctionCallContent_ParseArgs(t *testing.T) {
	fc := &agent.FunctionCallContent{
		CallID:    "call-1",
		Name:      "test_func",
		Arguments: `{"key": "value", "number": 42}`,
	}

	args, err := fc.ParseArgs()
	if err != nil {
		t.Fatalf("expected no error parsing args, got: %v", err)
	}

	if args["key"] != "value" {
		t.Errorf("expected key 'value', got %v", args["key"])
	}
	if args["number"] != float64(42) {
		t.Errorf("expected number 42, got %v", args["number"])
	}

	// Test invalid JSON
	fcInvalid := &agent.FunctionCallContent{
		CallID:    "call-2",
		Name:      "test_func",
		Arguments: `invalid json`,
	}
	_, err = fcInvalid.ParseArgs()
	if err == nil {
		t.Error("expected error parsing invalid JSON, got nil")
	}
}

// Test tool error handling
func TestAgent_ToolError(t *testing.T) {
	client := agenttest.NewClient()

	toolCalls := []*agent.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "error_tool",
			Arguments: `{}`,
		},
	}
	client.WithToolCalls(toolCalls, "Handled error")

	expectedErr := errors.New("tool execution failed")
	tool := &agenttest.Tool{
		NameValue: "error_tool",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return nil, expectedErr
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error from agent, got: %v", err)
	}

	// Agent should handle the tool error and return final response
	if resp.Text() != "Handled error" {
		t.Errorf("expected final response, got %q", resp.Text())
	}
}

// Test tool not found
func TestAgent_ToolNotFound(t *testing.T) {
	client := agenttest.NewClient()

	toolCalls := []*agent.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "nonexistent_tool",
			Arguments: `{}`,
		},
	}
	client.WithToolCalls(toolCalls, "Handled missing tool")

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	// Provide an empty tool list - the agent will still process the response
	// Tool call will fail but agent continues
	resp, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools:    []agent.Tool{}, // No tools provided
		ToolMode: agent.ToolModeAuto,
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error from agent, got: %v", err)
	}

	// Agent should eventually return the final response after handling tool call failure
	if len(resp.Messages) == 0 {
		t.Error("expected messages in response")
	}
}

// Test tool with invalid arguments
func TestAgent_ToolInvalidArgs(t *testing.T) {
	client := agenttest.NewClient()

	toolCalls := []*agent.FunctionCallContent{
		{
			CallID:    "call-1",
			Name:      "test_tool",
			Arguments: `invalid json`,
		},
	}
	client.WithToolCalls(toolCalls, "Handled invalid args")

	tool := &agenttest.Tool{
		NameValue: "test_tool",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return "should not be called", nil
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(context.Background(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("Test"))

	if err != nil {
		t.Fatalf("expected no error from agent, got: %v", err)
	}

	// Tool should not have been called
	if tool.CallCount != 0 {
		t.Errorf("expected tool not to be called, but was called %d times", tool.CallCount)
	}

	if resp.Text() != "Handled invalid args" {
		t.Errorf("expected final response, got %q", resp.Text())
	}
}

// Test New with nil config
func TestAgent_NewWithNilConfig(t *testing.T) {
	client := agenttest.NewClient()
	a := agent.New(client, nil, nil)

	if a == nil {
		t.Fatal("expected agent, got nil")
	}

	// ID should be auto-generated
	if a.ID() == "" {
		t.Error("expected auto-generated ID, got empty string")
	}

	if a.Name() != "" {
		t.Errorf("expected empty name, got %q", a.Name())
	}
}

// Test New with nil client (should panic)
func TestAgent_NewWithNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when creating agent with nil client")
		}
	}()

	agent.New(nil, &agent.Config{ID: "test"}, nil)
}

// NonStreamingClient is a client that doesn't implement streaming
type NonStreamingClient struct {
	response *agent.RunResponse
	err      error
}

func (c *NonStreamingClient) Run(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}

// Test RunOptions.Merge with various combinations
func TestRunOptions_Merge(t *testing.T) {
	tests := []struct {
		name     string
		base     *agent.RunOptions
		override *agent.RunOptions
		check    func(t *testing.T, result *agent.RunOptions)
	}{
		{
			name:     "both nil",
			base:     nil,
			override: nil,
			check: func(t *testing.T, result *agent.RunOptions) {
				if result == nil {
					t.Error("expected non-nil result")
				}
			},
		},
		{
			name: "override nil",
			base: &agent.RunOptions{
				ToolMode: agent.ToolModeAuto,
			},
			override: nil,
			check: func(t *testing.T, result *agent.RunOptions) {
				if result.ToolMode != agent.ToolModeAuto {
					t.Errorf("expected ToolModeAuto, got %v", result.ToolMode)
				}
			},
		},
		{
			name: "base nil",
			base: nil,
			override: &agent.RunOptions{
				ToolMode: agent.ToolModeRequired,
			},
			check: func(t *testing.T, result *agent.RunOptions) {
				if result.ToolMode != agent.ToolModeRequired {
					t.Errorf("expected ToolModeRequired, got %v", result.ToolMode)
				}
			},
		},
		{
			name: "tools merged",
			base: &agent.RunOptions{
				Tools: []agent.Tool{&agenttest.Tool{NameValue: "tool1"}},
			},
			override: &agent.RunOptions{
				Tools: []agent.Tool{&agenttest.Tool{NameValue: "tool2"}},
			},
			check: func(t *testing.T, result *agent.RunOptions) {
				if len(result.Tools) != 2 {
					t.Errorf("expected 2 tools, got %d", len(result.Tools))
				}
			},
		},
		{
			name: "metadata merged",
			base: &agent.RunOptions{
				AdditionalMetadata: map[string]any{"key1": "value1"},
			},
			override: &agent.RunOptions{
				AdditionalMetadata: map[string]any{"key2": "value2"},
			},
			check: func(t *testing.T, result *agent.RunOptions) {
				if len(result.AdditionalMetadata) != 2 {
					t.Errorf("expected 2 metadata entries, got %d", len(result.AdditionalMetadata))
				}
				if result.AdditionalMetadata["key1"] != "value1" {
					t.Error("expected key1 from base")
				}
				if result.AdditionalMetadata["key2"] != "value2" {
					t.Error("expected key2 from override")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.base.Merge(tt.override)
			tt.check(t, result)
		})
	}
}

// Test TextContent
func TestTextContent_String(t *testing.T) {
	tc := &agent.TextContent{Text: "test content"}
	if tc.String() != "test content" {
		t.Errorf("expected 'test content', got %q", tc.String())
	}
}

// Test TextReasoningContent
func TestTextReasoningContent_String(t *testing.T) {
	trc := &agent.TextReasoningContent{Text: "reasoning text"}
	if trc.String() != "reasoning text" {
		t.Errorf("expected 'reasoning text', got %q", trc.String())
	}
}

// Test UsageDetails
func TestUsageDetails_Add(t *testing.T) {
	ud1 := &agent.UsageDetails{
		InputTokenCount:  10,
		OutputTokenCount: 20,
		TotalTokenCount:  30,
	}
	ud2 := &agent.UsageDetails{
		InputTokenCount:  5,
		OutputTokenCount: 15,
		TotalTokenCount:  20,
	}

	ud1.Add(ud2)
	if ud1.InputTokenCount != 15 {
		t.Errorf("expected input tokens 15, got %d", ud1.InputTokenCount)
	}
	if ud1.OutputTokenCount != 35 {
		t.Errorf("expected output tokens 35, got %d", ud1.OutputTokenCount)
	}
	if ud1.TotalTokenCount != 50 {
		t.Errorf("expected total tokens 50, got %d", ud1.TotalTokenCount)
	}
}

func TestUsageDetails_AddWithAdditionalCounts(t *testing.T) {
	ud1 := &agent.UsageDetails{
		InputTokenCount: 10,
		AdditionalCounts: map[string]int64{
			"cache_read": 5,
		},
	}
	ud2 := &agent.UsageDetails{
		InputTokenCount: 5,
		AdditionalCounts: map[string]int64{
			"cache_read":  3,
			"cache_write": 2,
		},
	}

	ud1.Add(ud2)
	if ud1.AdditionalCounts["cache_read"] != 8 {
		t.Errorf("expected cache_read 8, got %d", ud1.AdditionalCounts["cache_read"])
	}
	if ud1.AdditionalCounts["cache_write"] != 2 {
		t.Errorf("expected cache_write 2, got %d", ud1.AdditionalCounts["cache_write"])
	}
}

func TestUsageDetails_AddWithNil(t *testing.T) {
	var ud1 *agent.UsageDetails
	ud2 := &agent.UsageDetails{InputTokenCount: 10}

	// Should not panic
	ud1.Add(ud2)

	ud1 = &agent.UsageDetails{InputTokenCount: 10}
	ud1.Add(nil)
	if ud1.InputTokenCount != 10 {
		t.Errorf("expected input tokens to remain 10, got %d", ud1.InputTokenCount)
	}
}

// Test JSON marshaling of InMemoryThread
func TestInMemoryThread_MarshalJSON(t *testing.T) {
	thread := &agent.InMemoryThread{}
	thread.Add(context.Background(), agent.NewTextMessage("test"))

	data, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("expected no error marshaling thread, got: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty JSON data")
	}

	// Verify it's valid JSON array (thread marshals as array of messages)
	var result []interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Errorf("expected valid JSON array, got error: %v", err)
	}
}
