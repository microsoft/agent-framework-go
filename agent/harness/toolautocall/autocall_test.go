// Copyright (c) Microsoft. All rights reserved.

package toolautocall_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/messagetest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestFunctionInvoking_SupportsSingleFunctionCallPerRequest(t *testing.T) {
	type EmptyArgs struct{}
	type Func2Args struct {
		I int `json:"i"`
	}

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func1", Description: "Function 1"},
			func(ctx tool.Context, args EmptyArgs) (string, error) {
				return "Result 1", nil
			}),
		functool.MustNew(functool.Config{Name: "Func2", Description: "Function 2"},
			func(ctx tool.Context, args Func2Args) (string, error) {
				return "Result 2: 42", nil
			}),
		functool.MustNew(functool.Config{Name: "VoidReturn", Description: "Void return"},
			func(ctx tool.Context, args Func2Args) (string, error) {
				return "Success: Function completed.", nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: `{}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: `{"i":43}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{})
}

func TestFunctionInvoking_SkipsNilToolCallApprovalResponse(t *testing.T) {
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		message.New(&message.ToolApprovalResponseContent{
			RequestID: "missing-tool-call",
			Approved:  true,
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
	}

	invokeAndAssertApproval(t, nil, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

func TestFunctionInvoking_SkipsNilToolCallRejectedApprovalResponse(t *testing.T) {
	input := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		message.New(&message.ToolApprovalResponseContent{
			RequestID: "missing-tool-call",
			Approved:  false,
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
	}

	invokeAndAssertApproval(t, nil, input, downstreamAgentOutput, expectedOutput, expectedDownstreamAgentInput, nil)
}

func TestFunctionInvoking_SupportsMultipleFunctionCallsPerRequest(t *testing.T) {
	type Func1Args struct {
		I *int `json:"i"`
	}
	type Func2Args struct {
		I int `json:"i"`
	}

	tests := []struct {
		name                       string
		allowConcurrentInvocations bool
	}{
		{"serial", false},
		{"concurrent", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := []tool.Tool{
				functool.MustNew(functool.Config{Name: "Func1"},
					func(ctx tool.Context, args Func1Args) (string, error) {
						return "Result 1", nil
					}),
				functool.MustNew(functool.Config{Name: "Func2"},
					func(ctx tool.Context, args Func2Args) (string, error) {
						switch args.I {
						case 34:
							return "Result 2: 34", nil
						case 56:
							return "Result 2: 56", nil
						case 78:
							return "Result 2: 78", nil
						}
						return "Result 2", nil
					}),
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: `{"i":null}`},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":34}`},
					&message.FunctionCallContent{CallID: "callId3", Name: "Func2", Arguments: `{"i":56}`},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
					&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 34"},
					&message.FunctionResultContent{CallID: "callId3", Result: "Result 2: 56"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId4", Name: "Func2", Arguments: `{"i":78}`},
					&message.FunctionCallContent{CallID: "callId5", Name: "Func1", Arguments: `{"i":null}`},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId4", Result: "Result 2: 78"},
					&message.FunctionResultContent{CallID: "callId5", Result: "Result 1"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "world"},
				}},
			}

			autocallOptions := toolautocall.Config{
				AllowConcurrentInvocations: tt.allowConcurrentInvocations,
			}

			invokeAndAssert(t, tools, plan, nil, autocallOptions)
		})
	}
}

// invokeAndAssert is a helper that creates a test agent following the given plan
// and asserts that the autocall middleware processes it correctly.
// Returns the final chat history.
// Plan should start with the initial user message and contain all expected messages.
// autocallOptions can be nil to use default settings.
func invokeAndAssert(t *testing.T, tools []tool.Tool, plan []*message.Message, expected []*message.Message, autocallOptions toolautocall.Config) []*message.Message {
	t.Helper()

	if len(plan) == 0 {
		t.Fatal("plan must not be empty")
	}

	if expected == nil {
		expected = plan
	}

	rb := agenttest.NewResponseBuilder()
	for i := range plan {
		idx := i*2 + 1
		if idx >= len(plan) {
			break
		}
		msg := plan[idx]
		for _, content := range msg.Contents {
			rb.Add(&agent.ResponseUpdate{
				Role:     msg.Role,
				Contents: []message.Content{content},
			})
		}
		rb.NewTurn()
	}

	runner := &agenttest.Runner{
		Responses: rb.Build(),
	}

	initialMessages := []*message.Message{plan[0]}

	// Build options
	var opts []agent.Option
	for _, tool := range tools {
		opts = append(opts, agent.WithTool(tool))
	}
	// Use a deterministic (empty) ID generator for test reproducibility.
	// Do not use an empty ID generator in production code, as it breaks message tracking and deduplication.
	autocallOptions.NewID = func() string {
		return ""
	}

	// Collect all updates
	var resp agent.Response
	for update, err := range toolautocall.New(autocallOptions).Run(runner.Run, t.Context(), initialMessages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error during streaming: %v", err)
		}
		resp.Update(update)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one update")
	}

	// Build actual chat history
	actual := append(initialMessages, resp.Messages...)

	// Assert messages match expected
	if err := messagetest.MessagesEqual(actual, expected); err != nil {
		t.Error(err)
	}

	return actual
}

func TestFunctionInvoking_FunctionReturningFunctionResultContentWithMatchingCallID_UsesItDirectly(t *testing.T) {
	var returnedFrc *message.FunctionResultContent

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func1"},
			func(ctx tool.Context, args struct{}) (any, error) {
				returnedFrc = &message.FunctionResultContent{
					CallID: ctx.CallID,
					Result: "Custom result from function",
					ContentHeader: message.ContentHeader{
						RawRepresentation: "CustomRaw",
					},
				}
				return returnedFrc, nil
			}),
	}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: `{}`}},
			}).
			NewTurn().
			Add(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "done"}},
			}).
			Build(),
	}

	var opts []agent.Option
	for _, tl := range tools {
		opts = append(opts, agent.WithTool(tl))
	}

	initialMessages := []*message.Message{message.NewText("hello")}
	var resp agent.Response
	for update, err := range toolautocall.New(toolautocall.Config{}).Run(runner.Run, t.Context(), initialMessages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Update(update)
	}

	var toolMessage *message.Message
	for _, msg := range resp.Messages {
		if msg.Role == message.RoleTool {
			toolMessage = msg
			break
		}
	}
	if toolMessage == nil {
		t.Fatal("expected a tool message in response")
	}

	var frcs []*message.FunctionResultContent
	for _, c := range toolMessage.Contents {
		if frc, ok := c.(*message.FunctionResultContent); ok {
			frcs = append(frcs, frc)
		}
	}
	if len(frcs) != 1 {
		t.Fatalf("expected exactly one FunctionResultContent in tool message, got %d", len(frcs))
	}

	if frcs[0] != returnedFrc {
		t.Fatalf("expected tool FunctionResultContent to be the same instance returned by tool")
	}
	if frcs[0].Result != "Custom result from function" {
		t.Fatalf("expected result %q, got %v", "Custom result from function", frcs[0].Result)
	}
	if frcs[0].RawRepresentation != "CustomRaw" {
		t.Fatalf("expected RawRepresentation %q, got %v", "CustomRaw", frcs[0].RawRepresentation)
	}
	if frcs[0].CallID != "callId1" {
		t.Fatalf("expected CallID %q, got %q", "callId1", frcs[0].CallID)
	}
}

func TestFunctionInvoking_FunctionReturningFunctionResultContentWithMismatchedCallID_WrapsIt(t *testing.T) {
	returnedFrc := &message.FunctionResultContent{
		CallID: "differentCallId",
		Result: "Result from function",
	}

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func1"},
			func(ctx tool.Context, args struct{}) (any, error) {
				return returnedFrc, nil
			}),
	}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: `{}`}},
			}).
			NewTurn().
			Add(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "done"}},
			}).
			Build(),
	}

	var opts []agent.Option
	for _, tl := range tools {
		opts = append(opts, agent.WithTool(tl))
	}

	initialMessages := []*message.Message{message.NewText("hello")}
	var resp agent.Response
	for update, err := range toolautocall.New(toolautocall.Config{}).Run(runner.Run, t.Context(), initialMessages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Update(update)
	}

	var toolMessage *message.Message
	for _, msg := range resp.Messages {
		if msg.Role == message.RoleTool {
			toolMessage = msg
			break
		}
	}
	if toolMessage == nil {
		t.Fatal("expected a tool message in response")
	}

	var frcs []*message.FunctionResultContent
	for _, c := range toolMessage.Contents {
		if frc, ok := c.(*message.FunctionResultContent); ok {
			frcs = append(frcs, frc)
		}
	}
	if len(frcs) != 1 {
		t.Fatalf("expected exactly one FunctionResultContent in tool message, got %d", len(frcs))
	}

	frc := frcs[0]
	if frc.CallID != "callId1" {
		t.Fatalf("expected outer CallID %q, got %q", "callId1", frc.CallID)
	}
	inner, ok := frc.Result.(*message.FunctionResultContent)
	if !ok {
		t.Fatalf("expected outer Result to be *message.FunctionResultContent, got %T", frc.Result)
	}
	if inner != returnedFrc {
		t.Fatalf("expected wrapped inner FunctionResultContent to be the same instance returned by tool")
	}
	if inner.CallID != "differentCallId" {
		t.Fatalf("expected inner CallID %q, got %q", "differentCallId", inner.CallID)
	}
	if inner.Result != "Result from function" {
		t.Fatalf("expected inner result %q, got %v", "Result from function", inner.Result)
	}
}

// TestFunctionInvoking_SupportsToolsProvidedByAdditionalTools tests AdditionalTools functionality
func TestFunctionInvoking_SupportsToolsProvidedByAdditionalTools(t *testing.T) {
	type Func1Args struct{}
	type Func2Args struct {
		I int `json:"i"`
	}

	tests := []struct {
		name           string
		provideOptions bool
	}{
		{"without_tools", false},
		{"with_tools", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tools []tool.Tool
			if tt.provideOptions {
				tools = []tool.Tool{
					functool.MustNew(functool.Config{Name: "OptionsFunc"},
						func(ctx tool.Context, args struct{}) (string, error) {
							t.Error("OptionsFunc should not be invoked")
							return "Shouldn't be invoked", nil
						}),
				}
			}

			autocallOptions := toolautocall.Config{
				AdditionalTools: []tool.Tool{
					functool.MustNew(functool.Config{Name: "Func1"},
						func(ctx tool.Context, args Func1Args) (string, error) {
							return "Result 1", nil
						}),
					functool.MustNew(functool.Config{Name: "Func2"},
						func(ctx tool.Context, args Func2Args) (string, error) {
							return fmt.Sprintf("Result 2: %d", args.I), nil
						}),
					functool.MustNew(functool.Config{Name: "VoidReturn"},
						func(ctx tool.Context, args Func2Args) (string, error) {
							return "Success: Function completed.", nil
						}),
				},
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: `{}`},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: `{"i":43}`},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "world"},
				}},
			}

			invokeAndAssert(t, tools, plan, nil, autocallOptions)
		})
	}
}

// TestFunctionInvoking_PrefersToolsProvidedByOptions tests that provided tools take precedence over AdditionalTools
func TestFunctionInvoking_PrefersToolsProvidedByOptions(t *testing.T) {
	type Func2Args struct {
		I int `json:"i"`
	}

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func1"},
			func(ctx tool.Context, args struct{}) (string, error) {
				return "Result 1", nil
			}),
	}

	autocallOptions := toolautocall.Config{
		AdditionalTools: []tool.Tool{
			functool.MustNew(functool.Config{Name: "Func1"},
				func(ctx tool.Context, args struct{}) (string, error) {
					t.Error("AdditionalTools Func1 should not be invoked")
					return "Should never be invoked", nil
				}),
			functool.MustNew(functool.Config{Name: "Func2"},
				func(ctx tool.Context, args Func2Args) (string, error) {
					return fmt.Sprintf("Result 2: %d", args.I), nil
				}),
			functool.MustNew(functool.Config{Name: "VoidReturn"},
				func(ctx tool.Context, args Func2Args) (string, error) {
					return "Success: Function completed.", nil
				}),
		},
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: `{"i":43}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, autocallOptions)
}

// TestFunctionInvoking_ParallelFunctionCallsMayBeInvokedConcurrently tests concurrent invocation
func TestFunctionInvoking_ParallelFunctionCallsMayBeInvokedConcurrently(t *testing.T) {
	var remaining atomic.Int32
	remaining.Store(2)
	done := make(chan bool)

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func"},
			func(ctx tool.Context, args struct{ Arg string }) (string, error) {
				if remaining.Add(-1) == 0 {
					close(done)
				}
				<-done
				return args.Arg + args.Arg, nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func", Arguments: `{"Arg":"hello"}`},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func", Arguments: `{"Arg":"world"}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "hellohello"},
			&message.FunctionResultContent{CallID: "callId2", Result: "worldworld"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	autocallOptions := toolautocall.Config{
		AllowConcurrentInvocations: true,
	}

	invokeAndAssert(t, tools, plan, nil, autocallOptions)
}

// TestFunctionInvoking_ConcurrentInvocationOfParallelCallsDisabledByDefault tests serial invocation by default
func TestFunctionInvoking_ConcurrentInvocationOfParallelCallsDisabledByDefault(t *testing.T) {
	var activeCount atomic.Int32

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func"},
			func(ctx tool.Context, args struct{ Arg string }) (string, error) {
				activeCount.Add(1)
				time.Sleep(100 * time.Millisecond)
				if activeCount.Load() != 1 {
					t.Error("Expected only 1 active function call at a time")
				}
				activeCount.Add(-1)
				return args.Arg + args.Arg, nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func", Arguments: `{"Arg":"hello"}`},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func", Arguments: `{"Arg":"world"}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "hellohello"},
			&message.FunctionResultContent{CallID: "callId2", Result: "worldworld"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, tools, plan, nil, toolautocall.Config{})
}

// TestFunctionInvoking_ContinuesWithSuccessfulCallsUntilMaximumIterations tests MaximumIterationsPerRequest
func TestFunctionInvoking_ContinuesWithSuccessfulCallsUntilMaximumIterations(t *testing.T) {
	maxIterations := 7
	actualCallCount := 0

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "VoidReturn"},
			func(ctx tool.Context, args struct{}) (string, error) {
				actualCallCount++
				return "Success: Function completed.", nil
			}),
	}

	// Build a plan that has more iterations than the max
	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
	}

	for i := 0; i < maxIterations+5; i++ {
		plan = append(plan,
			&message.Message{Role: message.RoleAssistant, Contents: []message.Content{
				&message.FunctionCallContent{CallID: fmt.Sprintf("callId%d", i), Name: "VoidReturn"},
			}},
		)
		plan = append(plan,
			&message.Message{Role: message.RoleTool, Contents: []message.Content{
				&message.FunctionResultContent{CallID: fmt.Sprintf("callId%d", i), Result: "Success: Function completed."},
			}},
		)
	}

	// Expected plan: initial message + (assistant + tool) * maxIterations + final assistant message
	// The loop runs maxIterations times, each time adding assistant+tool, then stops with one more assistant message
	expectedPlan := plan[:maxIterations*2+2]

	autocallOptions := toolautocall.Config{
		MaximumIterationsPerRequest: maxIterations,
	}

	invokeAndAssert(t, tools, plan, expectedPlan, autocallOptions)

	if actualCallCount != maxIterations {
		t.Errorf("Expected %d function calls, got %d", maxIterations, actualCallCount)
	}

	actualCallCount = 0
}

// TestFunctionInvoking_ContinuesWithFailingCallsUntilMaximumConsecutiveErrors tests MaximumConsecutiveErrorsPerRequest
func TestFunctionInvoking_ContinuesWithFailingCallsUntilMaximumConsecutiveErrors(t *testing.T) {
	tests := []struct {
		name                       string
		allowConcurrentInvocations bool
	}{
		{"serial", false},
		{"concurrent", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callIndex := 0

			tools := []tool.Tool{
				functool.MustNew(functool.Config{Name: "Func"},
					func(ctx tool.Context, args struct {
						ShouldThrow bool `json:"shouldThrow"`
						CallIndex   int  `json:"callIndex"`
					},
					) (string, error) {
						if args.ShouldThrow {
							return "", fmt.Errorf("Exception from call %d", args.CallIndex)
						}
						return "Success", nil
					}),
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}

			// Single failure (NumConsecutiveErrors = 1)
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, true)...)

			// Reset with successful iteration (NumConsecutiveErrors = 0)
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, false, false, false)...)

			// Any failure within an iteration causes it to be treated as failed (NumConsecutiveErrors = 1)
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, false, true, false)...)

			// Multiple failures in same iteration still counts as single iteration failed (NumConsecutiveErrors = 2)
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, true, true, true)...)

			// Any more failures will exceed the limit (should throw)
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, true, true)...)

			autocallOptions := toolautocall.Config{
				MaximumConsecutiveErrorsPerRequest: 2,
				AllowConcurrentInvocations:         tt.allowConcurrentInvocations,
			}

			// The test expects an error to be thrown
			rb := agenttest.NewResponseBuilder()
			for i := range plan {
				idx := i*2 + 1
				if idx >= len(plan) {
					break
				}
				msg := plan[idx]
				for _, content := range msg.Contents {
					rb.Add(&agent.ResponseUpdate{
						Role:     msg.Role,
						Contents: []message.Content{content},
					})
				}
				rb.NewTurn()
			}

			runner := &agenttest.Runner{
				Responses: rb.Build(),
			}

			initialMessages := []*message.Message{plan[0]}

			// Build options
			var opts []agent.Option
			for _, tool := range tools {
				opts = append(opts, agent.WithTool(tool))
			}

			var streamErr error
			for _, err := range toolautocall.New(autocallOptions).Run(runner.Run, t.Context(), initialMessages, opts...) {
				if err != nil {
					streamErr = err
					break
				}
			}

			if streamErr == nil {
				t.Error("Expected error in streaming due to MaximumConsecutiveErrors exceeded, got nil")
			} else if !errors.Is(streamErr, context.Canceled) && streamErr.Error() != "maximum consecutive function call errors reached" {
				// Check for expected error message
				t.Logf("Got error: %v", streamErr)
			}
		})
	}
}

// createFunctionCallIterationPlan creates an assistant message with function calls and a tool message with results
func createFunctionCallIterationPlan(callIndex *int, shouldThrow ...bool) []*message.Message {
	assistantContents := make([]message.Content, 0, len(shouldThrow))
	toolContents := make([]message.Content, 0, len(shouldThrow))

	for _, callShouldThrow := range shouldThrow {
		thisCallIndex := *callIndex
		*callIndex++
		callID := fmt.Sprintf("callId%d", thisCallIndex)

		arguments, _ := json.Marshal(map[string]any{"shouldThrow": callShouldThrow, "callIndex": thisCallIndex})
		assistantContents = append(assistantContents, &message.FunctionCallContent{
			CallID:    callID,
			Name:      "Func",
			Arguments: string(arguments),
		})

		var funcResult *message.FunctionResultContent
		if callShouldThrow {
			funcResult = &message.FunctionResultContent{
				CallID: callID,
				Result: "Error: Function failed.",
				Error:  fmt.Errorf("Exception from call %d", thisCallIndex),
			}
		} else {
			funcResult = &message.FunctionResultContent{
				CallID: callID,
				Result: "Success",
			}
		}
		toolContents = append(toolContents, funcResult)
	}

	return []*message.Message{
		{Role: message.RoleAssistant, Contents: assistantContents},
		{Role: message.RoleTool, Contents: toolContents},
	}
}

// TestFunctionInvoking_CanFailOnFirstException tests MaximumConsecutiveErrors=0
func TestFunctionInvoking_CanFailOnFirstException(t *testing.T) {
	tests := []struct {
		name                       string
		allowConcurrentInvocations bool
	}{
		{"serial", false},
		{"concurrent", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callIndex := 0

			tools := []tool.Tool{
				functool.MustNew(functool.Config{Name: "Func"},
					func(ctx tool.Context, args struct{}) (string, error) {
						return "", errors.New("It failed")
					}),
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, true)...)

			autocallOptions := toolautocall.Config{
				MaximumConsecutiveErrorsPerRequest: 0,
				AllowConcurrentInvocations:         tt.allowConcurrentInvocations,
			}

			rb := agenttest.NewResponseBuilder()
			for i := range plan {
				idx := i*2 + 1
				if idx >= len(plan) {
					break
				}
				msg := plan[idx]
				for _, content := range msg.Contents {
					rb.Add(&agent.ResponseUpdate{
						Role:     msg.Role,
						Contents: []message.Content{content},
					})
				}
				rb.NewTurn()
			}

			runner := &agenttest.Runner{
				Responses: rb.Build(),
			}

			initialMessages := []*message.Message{plan[0]}

			// Build options
			var opts []agent.Option
			for _, tool := range tools {
				opts = append(opts, agent.WithTool(tool))
			}

			var streamErr error
			for _, err := range toolautocall.New(autocallOptions).Run(runner.Run, t.Context(), initialMessages, opts...) {
				if err != nil {
					streamErr = err
					break
				}
			}

			if streamErr == nil {
				t.Error("Expected error in streaming on first exception, got nil")
			}
		})
	}
}

// TestFunctionInvoking_KeepsFunctionCallingContent tests that function call/result content is preserved
func TestFunctionInvoking_KeepsFunctionCallingContent(t *testing.T) {
	type Func2Args struct {
		I int `json:"i"`
	}

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func1"},
			func(ctx tool.Context, args struct{}) (string, error) {
				return "Result 1", nil
			}),
		functool.MustNew(functool.Config{Name: "Func2"},
			func(ctx tool.Context, args Func2Args) (string, error) {
				return fmt.Sprintf("Result 2: %d", args.I), nil
			}),
		functool.MustNew(functool.Config{Name: "VoidReturn"},
			func(ctx tool.Context, args Func2Args) (string, error) {
				return "Success: Function completed.", nil
			}),
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "extra"},
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
			&message.TextContent{Text: "stuff"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: `{"i":42}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: `{"i":43}`},
			&message.TextContent{Text: "more"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	finalChat := invokeAndAssert(t, tools, plan, nil, toolautocall.Config{})
	validateFunctionContent(t, finalChat)
}

func validateFunctionContent(t *testing.T, messages []*message.Message) {
	t.Helper()
	hasFunctionContent := false
	for _, msg := range messages {
		for _, content := range msg.Contents {
			switch content.(type) {
			case *message.FunctionCallContent, *message.FunctionResultContent:
				hasFunctionContent = true
			}
		}
	}
	if !hasFunctionContent {
		t.Error("Expected final chat to contain FunctionCallContent or FunctionResultContent")
	}
}

// TestFunctionInvoking_TerminateOnUnknownCalls tests TerminateOnUnknownCalls behavior
func TestFunctionInvoking_TerminateOnUnknownCalls(t *testing.T) {
	tests := []struct {
		name                    string
		terminateOnUnknownCalls bool
	}{
		{"continue_on_unknown", false},
		{"terminate_on_unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type Func2Args struct {
				I int `json:"i"`
			}

			tools := []tool.Tool{
				functool.MustNew(functool.Config{Name: "KnownFunc"},
					func(ctx tool.Context, args Func2Args) (string, error) {
						return fmt.Sprintf("Known: %d", args.I), nil
					}),
			}

			autocallOptions := toolautocall.Config{
				TerminateOnUnknownCalls: tt.terminateOnUnknownCalls,
			}

			fullPlan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "UnknownFunc", Arguments: `{"i":1}`},
					&message.FunctionCallContent{CallID: "callId2", Name: "KnownFunc", Arguments: `{"i":2}`},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId1", Result: "Error: Requested function \"UnknownFunc\" not found."},
					&message.FunctionResultContent{CallID: "callId2", Result: "Known: 2"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "done"},
				}},
			}

			if tt.terminateOnUnknownCalls {
				// Should terminate after the assistant message with unknown function call
				expectedPlan := fullPlan[:2]
				invokeAndAssert(t, tools, fullPlan, expectedPlan, autocallOptions)
			} else {
				// Should continue and add error result for unknown function
				invokeAndAssert(t, tools, fullPlan, nil, autocallOptions)
			}
		})
	}
}

// TestFunctionInvoking_ExceptionDetailsOnlyReportedWhenRequested tests IncludeDetailedErrors flag
func TestFunctionInvoking_ExceptionDetailsOnlyReportedWhenRequested(t *testing.T) {
	tests := []struct {
		name           string
		detailedErrors bool
		expectedResult string
	}{
		{"without_detailed_errors", false, "Error: Function failed."},
		{"with_detailed_errors", true, "Error: Function failed. Exception: Oh no!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := []tool.Tool{
				functool.MustNew(functool.Config{Name: "Func1"},
					func(ctx tool.Context, args struct{}) (string, error) {
						return "", errors.New("Oh no!")
					}),
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1"},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId1", Result: tt.expectedResult, Error: fmt.Errorf("Oh no!")},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "world"},
				}},
			}

			autocallOptions := toolautocall.Config{
				MaximumConsecutiveErrorsPerRequest: 3,
				IncludeDetailedErrors:              tt.detailedErrors,
			}

			invokeAndAssert(t, tools, plan, nil, autocallOptions)
		})
	}
}

// TestFunctionInvoking_AllResponseMessagesReturned tests that all response messages are returned
func TestFunctionInvoking_AllResponseMessagesReturned(t *testing.T) {
	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func1"},
			func(ctx tool.Context, args struct{}) (string, error) {
				return "doesn't matter", nil
			}),
	}

	messages := []*message.Message{
		message.New(&message.TextContent{Text: "Hello"}),
	}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.FunctionCallContent{CallID: "callId0", Name: "Func1"}},
			}).
			NewTurn().
			Add(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			}).
			NewTurn().
			Add(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "The answer is 42."}},
			}).
			Build(),
	}

	initialMessages := []*message.Message{messages[0]}
	var opts []agent.Option
	for _, tool := range tools {
		opts = append(opts, agent.WithTool(tool))
	}

	var resp agent.Response
	for update, err := range toolautocall.New(toolautocall.Config{}).Run(runner.Run, t.Context(), initialMessages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Update(update)
	}

	if len(resp.Messages) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(resp.Messages))
	}

	// Check last message text
	lastMsg := resp.Messages[len(resp.Messages)-1]
	if lastMsg.String() != "The answer is 42." {
		t.Errorf("Expected text 'The answer is 42.', got %q", lastMsg.String())
	}

	// Verify message types
	if _, ok := resp.Messages[0].Contents[0].(*message.FunctionCallContent); !ok {
		t.Error("Expected first message to be FunctionCallContent")
	}
	if _, ok := resp.Messages[1].Contents[0].(*message.FunctionResultContent); !ok {
		t.Error("Expected second message to be FunctionResultContent")
	}
	if _, ok := resp.Messages[2].Contents[0].(*message.FunctionCallContent); !ok {
		t.Error("Expected third message to be FunctionCallContent")
	}
	if _, ok := resp.Messages[3].Contents[0].(*message.FunctionResultContent); !ok {
		t.Error("Expected fourth message to be FunctionResultContent")
	}
	if _, ok := resp.Messages[4].Contents[0].(*message.TextContent); !ok {
		t.Error("Expected fifth message to be TextContent")
	}
}

// TestFunctionInvoking_NextIterationIncludesAssistantFunctionCallMessage verifies that
// when the autocall middleware invokes the next function for a subsequent iteration,
// the messages include the assistant message with function calls before the tool result.
// This is required by chat APIs like OpenAI, which reject tool messages that aren't
// preceded by an assistant message with matching tool_calls.
func TestFunctionInvoking_NextIterationIncludesAssistantFunctionCallMessage(t *testing.T) {
	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Func1"},
			func(ctx tool.Context, args struct{}) (string, error) {
				return "Result 1", nil
			}),
	}

	rb := agenttest.NewResponseBuilder()
	// First turn: model calls a function.
	rb.Add(&agent.ResponseUpdate{
		Role:     message.RoleAssistant,
		Contents: []message.Content{&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: `{}`}},
	})
	// Second turn: verify messages, then return final text.
	rb.NewTurn(func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
		// The messages should contain: original user message, assistant with tool_calls, tool result.
		if len(messages) < 3 {
			t.Fatalf("expected at least 3 messages in next iteration, got %d", len(messages))
		}
		// First message should be the user message.
		if messages[0].Role != message.RoleUser {
			t.Errorf("expected first message to be user role, got %s", messages[0].Role)
		}
		// Second message should be the assistant with function call content.
		if messages[len(messages)-2].Role != message.RoleAssistant {
			t.Errorf("expected second-to-last message to be assistant role, got %s", messages[len(messages)-2].Role)
		}
		hasFCC := false
		for _, c := range messages[len(messages)-2].Contents {
			if fcc, ok := c.(*message.FunctionCallContent); ok && fcc.CallID == "callId1" {
				hasFCC = true
			}
		}
		if !hasFCC {
			t.Error("expected assistant message to contain FunctionCallContent with callId1")
		}
		// Last message should be tool result.
		if messages[len(messages)-1].Role != message.RoleTool {
			t.Errorf("expected last message to be tool role, got %s", messages[len(messages)-1].Role)
		}
		hasFRC := false
		for _, c := range messages[len(messages)-1].Contents {
			if frc, ok := c.(*message.FunctionResultContent); ok && frc.CallID == "callId1" {
				hasFRC = true
			}
		}
		if !hasFRC {
			t.Error("expected tool message to contain FunctionResultContent with callId1")
		}
	})
	rb.Add(&agent.ResponseUpdate{
		Role:     message.RoleAssistant,
		Contents: []message.Content{&message.TextContent{Text: "done"}},
	})

	runner := &agenttest.Runner{Responses: rb.Build()}

	var opts []agent.Option
	for _, tool := range tools {
		opts = append(opts, agent.WithTool(tool))
	}

	autocallConfig := toolautocall.Config{
		NewID: func() string { return "" },
	}

	initialMessages := []*message.Message{message.NewText("hello")}
	var resp agent.Response
	for update, err := range toolautocall.New(autocallConfig).Run(runner.Run, t.Context(), initialMessages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Update(update)
	}

	// Verify final response has function call, tool result, and text.
	if len(resp.Messages) < 3 {
		t.Fatalf("expected at least 3 response messages, got %d", len(resp.Messages))
	}
	lastMsg := resp.Messages[len(resp.Messages)-1]
	if lastMsg.String() != "done" {
		t.Errorf("expected final text 'done', got %q", lastMsg.String())
	}
}
