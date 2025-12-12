// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agenttest"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func TestRun_BasicExecution(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			AddText("Hello, world!").
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if resp.AgentID != "test-agent-id" {
		t.Errorf("expected AgentID 'test-agent-id', got %q", resp.AgentID)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	if resp.String() != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", resp.String())
	}
}

func TestRun_MultipleUpdates(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			AddText("First ").
			AddText("Second ").
			AddText("Third").
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	// Content should be coalesced
	if resp.String() != "First Second Third" {
		t.Errorf("expected 'First Second Third', got %q", resp.String())
	}
}

func TestRun_MultipleMessages(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			Add(
				&agent.RunResponseUpdate{
					MessageID: "msg-1",
					Role:      message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "First message"},
					},
				},
			).
			Add(
				&agent.RunResponseUpdate{
					MessageID: "msg-2",
					Role:      message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "Second message"},
					},
				},
			).
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}

	if resp.Messages[0].ID != "msg-1" {
		t.Errorf("expected first message ID 'msg-1', got %q", resp.Messages[0].ID)
	}

	if resp.Messages[1].ID != "msg-2" {
		t.Errorf("expected second message ID 'msg-2', got %q", resp.Messages[1].ID)
	}
}

func TestRun_ErrorHandling(t *testing.T) {
	ctx := t.Context()
	expectedErr := errors.New("test error")

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			AddText("Before error").
			AddError(expectedErr).
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	if resp != nil {
		t.Errorf("expected nil response on error, got %v", resp)
	}
}

func TestRun_UsageTracking(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.RunResponseUpdate{
				Contents: []message.Content{
					&message.UsageContent{
						Details: message.UsageDetails{
							InputTokenCount:  10,
							OutputTokenCount: 20,
							TotalTokenCount:  30,
						},
					},
					&message.TextContent{Text: "Response"},
				},
			}).
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if resp.Usage == nil {
		t.Fatal("expected usage details, got nil")
	}

	if resp.Usage.InputTokenCount != 10 {
		t.Errorf("expected input tokens 10, got %d", resp.Usage.InputTokenCount)
	}

	if resp.Usage.OutputTokenCount != 20 {
		t.Errorf("expected output tokens 20, got %d", resp.Usage.OutputTokenCount)
	}

	if resp.Usage.TotalTokenCount != 30 {
		t.Errorf("expected total tokens 30, got %d", resp.Usage.TotalTokenCount)
	}
}

func TestRun_ContinuationToken(t *testing.T) {
	ctx := t.Context()
	token := "test-continuation-token"

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.RunResponseUpdate{
				ContinuationToken: token,
				Contents: []message.Content{
					&message.TextContent{Text: "Response"},
				},
			}).
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if resp.ContinuationToken != token {
		t.Errorf("expected continuation token %q, got %v", token, resp.ContinuationToken)
	}
}

func TestRun_ResponseMetadata(t *testing.T) {
	ctx := t.Context()
	now := time.Now()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.RunResponseUpdate{
				ResponseID: "resp-123",
				MessageID:  "msg-456",
				AuthorName: "TestAgent",
				CreatedAt:  now,
				Contents: []message.Content{
					&message.TextContent{Text: "Response"},
				},
			}).
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if resp.ID != "resp-123" {
		t.Errorf("expected response ID 'resp-123', got %q", resp.ID)
	}

	if resp.CreatedAt != now {
		t.Errorf("expected CreatedAt %v, got %v", now, resp.CreatedAt)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	if resp.Messages[0].AuthorName != "TestAgent" {
		t.Errorf("expected author name 'TestAgent', got %q", resp.Messages[0].AuthorName)
	}
}

func TestRun_WithMiddleware(t *testing.T) {
	ctx := t.Context()
	mw := &agenttest.Middleware{}

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			AddText("Response").
			Build(),
	}

	_, err := agent.Run(ctx, a, agent.WithMiddleware(mw))
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !mw.Called() {
		t.Error("expected middleware to be invoked")
	}
}

func TestRun_MiddlewareOrder(t *testing.T) {
	ctx := t.Context()

	mw1 := &agenttest.Middleware{
		PreResponses: agenttest.NewResponseBuilder().
			AddText("mw1-before ").
			Build(),
		PostResponses: agenttest.NewResponseBuilder().
			AddText("mw1-after").
			Build(),
	}

	mw2 := &agenttest.Middleware{
		PreResponses: agenttest.NewResponseBuilder().
			AddText("mw2-before ").
			Build(),
		PostResponses: agenttest.NewResponseBuilder().
			AddText("mw2-after ").
			Build(),
	}

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.RunResponseUpdate{
				Contents: []message.Content{
					&message.TextContent{Text: "Response "},
				},
			}).
			Build(),
	}

	resp, err := agent.Run(ctx, a, agent.WithMiddleware(mw1), agent.WithMiddleware(mw2))
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	want := "mw1-before mw2-before Response mw2-after mw1-after"
	got := resp.String()
	if got != want {
		t.Errorf("expected response %q, got %q", want, got)
	}
}

func TestRun_AgentCapabilitiesMiddleware(t *testing.T) {
	ctx := t.Context()
	capsMw := &agenttest.Middleware{}

	a := &agenttest.Agent{
		Caps: agent.Capabilities{
			Middlewares: []agent.Middleware{capsMw},
		},
		Responses: agenttest.NewResponseBuilder().
			AddText("Response").
			Build(),
	}

	_, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !capsMw.Called() {
		t.Error("expected capabilities middleware to be invoked")
	}
}

func TestRun_AgentCapabilitiesTools(t *testing.T) {
	ctx := t.Context()

	testTool := agenttest.NewTool("test_tool", "A test tool")
	var receivedTools []string

	a := &agenttest.Agent{
		Caps: agent.Capabilities{
			Tools: []tool.Tool{testTool},
		},
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				for tool := range agent.GetOptions(opts, agent.WithTool) {
					receivedTools = append(receivedTools, tool.Name())
				}
			}).
			AddText("Response").
			Build(),
	}

	_, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(receivedTools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(receivedTools))
	}

	if receivedTools[0] != "test_tool" {
		t.Errorf("expected tool name 'test_tool', got %q", receivedTools[0])
	}
}

func TestRun_AdditionalProperties(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.RunResponseUpdate{
				AdditionalProperties: map[string]any{
					"key1": "value1",
					"key2": 42,
				},
				Contents: []message.Content{
					&message.TextContent{Text: "Response"},
				},
			}).
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if resp.AdditionalProperties == nil {
		t.Fatal("expected additional properties, got nil")
	}

	if resp.AdditionalProperties["key1"] != "value1" {
		t.Errorf("expected key1='value1', got %v", resp.AdditionalProperties["key1"])
	}

	if resp.AdditionalProperties["key2"] != 42 {
		t.Errorf("expected key2=42, got %v", resp.AdditionalProperties["key2"])
	}
}

func TestRun_NilUpdateHandling(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			AddText("Before nil").
			Add(nil). // Nil update
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err == nil {
		t.Fatal("expected error for nil update with nil error, got nil")
	}

	if resp != nil {
		t.Errorf("expected nil response on error, got %v", resp)
	}
}

func TestRun_PopulatesAgentIDAndAuthorName(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Iden: agent.NewIdentity("custom-id", "CustomAgent", "Description"),
		Responses: agenttest.NewResponseBuilder().
			AddText("Response").
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if resp.AgentID != "custom-id" {
		t.Errorf("expected AgentID 'custom-id', got %q", resp.AgentID)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	if resp.Messages[0].AuthorName != "CustomAgent" {
		t.Errorf("expected AuthorName 'CustomAgent', got %q", resp.Messages[0].AuthorName)
	}
}

func TestRun_ThreadCreation(t *testing.T) {
	ctx := t.Context()
	newThreadCalled := false
	a := &agenttest.Agent{
		NewThreadFunc: func() memory.Thread {
			newThreadCalled = true
			return agenttest.NewThread()
		},
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				thread, ok := agent.GetOption(opts, agent.WithThread)
				if !ok || thread == nil {
					t.Error("no thread provided")
				}
			}).
			AddText("Response").
			Build(),
	}

	_, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !newThreadCalled {
		t.Error("expected NewThread to be called")
	}
}

func TestRun_ProvidedThread(t *testing.T) {
	ctx := t.Context()
	providedThread := agenttest.NewThread()
	newThreadCalled := false

	a := &agenttest.Agent{
		NewThreadFunc: func() memory.Thread {
			newThreadCalled = true
			return agenttest.NewThread()
		},
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				thread, ok := agent.GetOption(opts, agent.WithThread)
				if !ok {
					t.Fatal("no thread provided")
				}
				// Type assert to compare
				if mt, ok := thread.(*agenttest.Thread); !ok || mt != providedThread {
					t.Fatal("wrong thread provided")
				}
			}).
			Add(&agent.RunResponseUpdate{
				Contents: []message.Content{
					&message.TextContent{Text: "Response"},
				},
			}).
			Build(),
	}

	_, err := agent.Run(ctx, a, agent.WithThread(providedThread))
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if newThreadCalled {
		t.Error("expected NewThread not to be called when thread is provided")
	}
}

func TestRun_ContentCoalescing(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.RunResponseUpdate{
				MessageID: "msg-1",
				Contents: []message.Content{
					&message.TextContent{Text: "Part "},
				},
			}).
			Add(&agent.RunResponseUpdate{
				MessageID: "msg-1",
				Contents: []message.Content{
					&message.TextContent{Text: "1 "},
				},
			}).
			Add(&agent.RunResponseUpdate{
				MessageID: "msg-1",
				Contents: []message.Content{
					&message.TextContent{Text: "Part "},
				},
			}).
			Add(&agent.RunResponseUpdate{
				MessageID: "msg-1",
				Contents: []message.Content{
					&message.TextContent{Text: "2"},
				},
			}).
			Build(),
	}

	resp, err := agent.Run(ctx, a)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	// After coalescing, should have fewer content items
	msg := resp.Messages[0]
	if len(msg.Contents) >= 4 {
		t.Errorf("expected contents to be coalesced, got %d items", len(msg.Contents))
	}

	if resp.String() != "Part 1 Part 2" {
		t.Errorf("expected 'Part 1 Part 2', got %q", resp.String())
	}
}

// Tests for RunStream

func TestRunStream_BasicExecution(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				// Verify streaming option is set
				streaming, ok := agent.GetOption(opts, agent.WithStreaming)
				if !ok || !streaming {
					t.Fatal("streaming not enabled")
				}
			}).
			AddText("Streamed ").
			AddText("response").
			Build(),
	}

	updateCount := 0
	for update, err := range agent.RunStream(ctx, a) {
		if err != nil {
			t.Fatalf("RunStream failed: %v", err)
		}
		if update != nil {
			updateCount++
		}
	}

	if updateCount != 2 {
		t.Errorf("expected 2 updates, got %d", updateCount)
	}
}

func TestRunStream_ErrorHandling(t *testing.T) {
	ctx := t.Context()
	expectedErr := errors.New("stream error")

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder().
			AddText("Before error").
			AddError(expectedErr).
			Build(),
	}

	var receivedErr error
	updateCount := 0
	for update, err := range agent.RunStream(ctx, a) {
		if err != nil {
			receivedErr = err
			break
		}
		if update != nil {
			updateCount++
		}
	}

	if !errors.Is(receivedErr, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, receivedErr)
	}

	if updateCount != 1 {
		t.Errorf("expected 1 update before error, got %d", updateCount)
	}
}

// Tests for RunText

func TestRunText_BasicExecution(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				// Verify message option is set with correct text
				hasMessage := false
				for msg := range agent.GetOptions(opts, agent.WithMessage) {
					if len(msg.Contents) > 0 {
						if tc, ok := msg.Contents[0].(*message.TextContent); ok {
							if tc.Text == "Hello" {
								hasMessage = true
							}
						}
					}
				}
				if !hasMessage {
					t.Fatal("expected message not found")
				}
			}).
			AddText("Response to Hello").
			Build(),
	}

	resp, err := agent.RunText(ctx, a, "Hello")
	if err != nil {
		t.Fatalf("RunText failed: %v", err)
	}

	if resp.String() != "Response to Hello" {
		t.Errorf("expected 'Response to Hello', got %q", resp.String())
	}
}

func TestRunText_WithAdditionalOptions(t *testing.T) {
	ctx := t.Context()
	providedThread := agenttest.NewThread()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				// Verify both message and thread are present
				_, hasMessage := agent.GetOption(opts, agent.WithMessage)
				thread, hasThread := agent.GetOption(opts, agent.WithThread)

				if !hasMessage {
					t.Fatal("message not found")
				}
				if !hasThread {
					t.Fatal("thread not found")
				}
				if mt, ok := thread.(*agenttest.Thread); !ok || mt != providedThread {
					t.Fatal("wrong thread provided")
				}
			}).
			AddText("Success").
			Build(),
	}

	_, err := agent.RunText(ctx, a, "Test", agent.WithThread(providedThread))
	if err != nil {
		t.Fatalf("RunText failed: %v", err)
	}
}

// Tests for RunTextStream

func TestRunTextStream_BasicExecution(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				// Verify streaming is enabled
				streaming, ok := agent.GetOption(opts, agent.WithStreaming)
				if !ok || !streaming {
					t.Fatal("streaming not enabled")
				}
				// Verify message is present
				_, hasMessage := agent.GetOption(opts, agent.WithMessage)
				if !hasMessage {
					t.Fatal("message not found")
				}
			}).
			AddText("Streamed ").
			AddText("text").
			Build(),
	}

	updateCount := 0
	for update, err := range agent.RunTextStream(ctx, a, "Test message") {
		if err != nil {
			t.Fatalf("RunTextStream failed: %v", err)
		}
		if update != nil {
			updateCount++
		}
	}

	if updateCount != 2 {
		t.Errorf("expected 2 updates, got %d", updateCount)
	}
}

// Tests for RunFor

type testStructuredOutput struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

type mockFormatter struct {
	formatFunc    func(v any) (format.Format, error)
	unmarshalFunc func(data []byte, f format.Format, v any) error
}

func (m *mockFormatter) Format(v any) (format.Format, error) {
	if m.formatFunc != nil {
		return m.formatFunc(v)
	}
	return format.JSON(), nil
}

func (m *mockFormatter) Unmarshal(data []byte, f format.Format, v any) error {
	if m.unmarshalFunc != nil {
		return m.unmarshalFunc(data, f, v)
	}
	return nil
}

func TestRunFor_BasicExecution(t *testing.T) {
	ctx := t.Context()

	formatter := &mockFormatter{
		formatFunc: func(v any) (format.Format, error) {
			return format.JSON(), nil
		},
		unmarshalFunc: func(data []byte, f format.Format, v any) error {
			// Simulate unmarshaling
			if output, ok := v.(*testStructuredOutput); ok {
				output.Name = "TestName"
				output.Value = 42
			}
			return nil
		},
	}

	a := &agenttest.Agent{
		Caps: agent.Capabilities{
			StructuredOutput: formatter,
		},
		Responses: agenttest.NewResponseBuilder(
			func(ctx context.Context, opts ...agent.Option) {
				// Verify response format is set
				_, hasFormat := agent.GetOption(opts, agent.WithResponseFormat)
				if !hasFormat {
					t.Fatal("response format not set")
				}
			}).
			Add(&agent.RunResponseUpdate{
				Contents: []message.Content{
					&message.TextContent{Text: `{"name":"TestName","value":42}`},
				},
			}).
			Build(),
	}

	result, resp, err := agent.RunFor[testStructuredOutput](ctx, a)
	if err != nil {
		t.Fatalf("RunFor failed: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if result.Name != "TestName" {
		t.Errorf("expected Name 'TestName', got %q", result.Name)
	}

	if result.Value != 42 {
		t.Errorf("expected Value 42, got %d", result.Value)
	}
}

func TestRunFor_NoStructuredOutputSupport(t *testing.T) {
	ctx := t.Context()

	a := &agenttest.Agent{
		Caps: agent.Capabilities{
			StructuredOutput: nil, // No structured output support
		},
		Responses: agenttest.NewResponseBuilder().
			AddText("Response").
			Build(),
	}

	_, resp, err := agent.RunFor[testStructuredOutput](ctx, a)
	if err == nil {
		t.Fatal("expected error for unsupported structured output, got nil")
	}

	if resp != nil {
		t.Errorf("expected nil response when agent doesn't support structured output, got %v", resp)
	}

	if err.Error() != "agent does not support structured output" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRunFor_FormatterError(t *testing.T) {
	ctx := t.Context()
	expectedErr := errors.New("format error")

	formatter := &mockFormatter{
		formatFunc: func(v any) (format.Format, error) {
			return nil, expectedErr
		},
	}

	a := &agenttest.Agent{
		Caps: agent.Capabilities{
			StructuredOutput: formatter,
		},
		Responses: agenttest.NewResponseBuilder().
			AddText("Response").
			Build(),
	}

	_, resp, err := agent.RunFor[testStructuredOutput](ctx, a)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	if resp != nil {
		t.Errorf("expected nil response on formatter error, got %v", resp)
	}
}

func TestRunFor_UnmarshalError(t *testing.T) {
	ctx := t.Context()
	expectedErr := errors.New("unmarshal error")

	formatter := &mockFormatter{
		formatFunc: func(v any) (format.Format, error) {
			return format.JSON(), nil
		},
		unmarshalFunc: func(data []byte, f format.Format, v any) error {
			return expectedErr
		},
	}

	a := &agenttest.Agent{
		Caps: agent.Capabilities{
			StructuredOutput: formatter,
		},
		Responses: agenttest.NewResponseBuilder().
			AddText("Invalid JSON").
			Build(),
	}

	_, resp, err := agent.RunFor[testStructuredOutput](ctx, a)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	if resp == nil {
		t.Error("expected response even when unmarshal fails")
	}
}
