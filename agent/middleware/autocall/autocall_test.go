// Copyright (c) Microsoft. All rights reserved.

package autocall_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/agenttest"
	"github.com/microsoft/agent-framework-go/agent/middleware/autocall"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/param"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestFunctionInvoking_SupportsSingleFunctionCallPerRequest(t *testing.T) {
	type EmptyArgs struct{}
	type Func2Args struct {
		I int `json:"i"`
	}

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "Func1", Description: "Function 1"},
			func(ctx context.Context, args EmptyArgs) (string, error) {
				return "Result 1", nil
			}),
		functool.MustNew(&functool.Func{Name: "Func2", Description: "Function 2"},
			func(ctx context.Context, args Func2Args) (string, error) {
				return "Result 2: 42", nil
			}),
		functool.MustNew(&functool.Func{Name: "VoidReturn", Description: "Void return"},
			func(ctx context.Context, args Func2Args) (string, error) {
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

	invokeAndAssert(t, tools, plan, nil, autocall.Options{})
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
				functool.MustNew(&functool.Func{Name: "Func1"},
					func(ctx context.Context, args Func1Args) (string, error) {
						return "Result 1", nil
					}),
				functool.MustNew(&functool.Func{Name: "Func2"},
					func(ctx context.Context, args Func2Args) (string, error) {
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

			autocallOptions := autocall.Options{
				AllowConcurrentInvocations: param.NewOpt(tt.allowConcurrentInvocations),
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
func invokeAndAssert(t *testing.T, tools []tool.Tool, plan []*message.Message, expected []*message.Message, autocallOptions autocall.Options) []*message.Message {
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
			rb.Add(&message.ResponseUpdate{
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
	var opts []agentopt.RunOption
	for _, tool := range tools {
		opts = append(opts, agentopt.Tool(tool))
	}

	// Collect all updates
	var resp message.Response
	for update, err := range autocall.New(autocallOptions).Run(t.Context(), runner.Run, initialMessages, opts...) {
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
	assertMessageListsEqual(t, expected, actual)

	return actual
}

// assertMessageListsEqual compares two message lists for equality
func assertMessageListsEqual(t *testing.T, expected, actual []*message.Message) {
	t.Helper()

	if len(expected) != len(actual) {
		t.Errorf("message count mismatch: expected %d, got %d", len(expected), len(actual))
		t.Logf("Expected messages:")
		for i, msg := range expected {
			t.Logf("  [%d] role=%s, contents=%d", i, msg.Role, len(msg.Contents))
		}
		t.Logf("Actual messages:")
		for i, msg := range actual {
			t.Logf("  [%d] role=%s, contents=%d", i, msg.Role, len(msg.Contents))
		}
		return
	}

	for i := range expected {
		exp := expected[i]
		act := actual[i]

		if exp.Role != act.Role {
			t.Errorf("message %d: role mismatch: expected %s, got %s", i, exp.Role, act.Role)
		}

		if exp.String() != act.String() {
			t.Errorf("message %d: string representation mismatch:\nexpected: %q\ngot:      %q", i, exp.String(), act.String())
		}

		if len(exp.Contents) != len(act.Contents) {
			t.Errorf("message %d: content count mismatch: expected %d, got %d", i, len(exp.Contents), len(act.Contents))
			continue
		}

		for j := range exp.Contents {
			expContent := exp.Contents[j]
			actContent := act.Contents[j]

			if reflect.TypeOf(expContent) != reflect.TypeOf(actContent) {
				t.Errorf("message %d, content %d: type mismatch: expected %T, got %T", i, j, expContent, actContent)
				continue
			}

			if !reflect.DeepEqual(expContent.Header(), actContent.Header()) {
				t.Errorf("message %d, content %d: header mismatch: expected %v, got %v", i, j, expContent.Header(), actContent.Header())
				continue
			}

			switch expContent := expContent.(type) {
			case fmt.Stringer:
				if actContent.(fmt.Stringer).String() != expContent.String() {
					t.Errorf("message %d, content %d: %T mismatch: expected %q, got %q", i, j, expContent, expContent.String(), actContent.(fmt.Stringer).String())
				}
			case *message.FunctionResultContent:
				act := actContent.(*message.FunctionResultContent)
				if expContent.CallID != act.CallID {
					t.Errorf("message %d, content %d: CallID mismatch: expected %q, got %q", i, j, expContent.CallID, act.CallID)
					break
				}

				// Compare Error fields
				if (expContent.Error == nil) != (act.Error == nil) {
					t.Errorf("message %d, content %d: Error presence mismatch: expected %q, got %q", i, j, expContent.Error, act.Error)
					break
				}
				if expContent.Error != nil && act.Error != nil && expContent.Error.Error() != act.Error.Error() {
					t.Errorf("message %d, content %d: Error message mismatch: expected %q, got %q", i, j, expContent.Error, act.Error)
					break
				}

				// Compare Result - handle json.RawMessage wrapping
				expResult := expContent.Result
				actResult := act.Result

				// If actual result is json.RawMessage, try to unmarshal it
				if actRaw, ok := actResult.(json.RawMessage); ok {
					var unmarshaled any
					if err := json.Unmarshal(actRaw, &unmarshaled); err == nil {
						actResult = unmarshaled
					}
				}
				if !reflect.DeepEqual(expResult, actResult) {
					t.Errorf("message %d, content %d: Result mismatch:\nexpected: %#v\ngot:      %#v", i, j, expResult, actResult)
				}
			default:
				if !reflect.DeepEqual(expContent, actContent) {
					t.Errorf("message %d, content %d: content mismatch:\nexpected: %#v\ngot:      %#v", i, j, expContent, actContent)
				}
			}
		}
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
					functool.MustNew(&functool.Func{Name: "OptionsFunc"},
						func(ctx context.Context, args struct{}) (string, error) {
							t.Error("OptionsFunc should not be invoked")
							return "Shouldn't be invoked", nil
						}),
				}
			}

			autocallOptions := autocall.Options{
				AdditionalTools: []tool.Tool{
					functool.MustNew(&functool.Func{Name: "Func1"},
						func(ctx context.Context, args Func1Args) (string, error) {
							return "Result 1", nil
						}),
					functool.MustNew(&functool.Func{Name: "Func2"},
						func(ctx context.Context, args Func2Args) (string, error) {
							return fmt.Sprintf("Result 2: %d", args.I), nil
						}),
					functool.MustNew(&functool.Func{Name: "VoidReturn"},
						func(ctx context.Context, args Func2Args) (string, error) {
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
		functool.MustNew(&functool.Func{Name: "Func1"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "Result 1", nil
			}),
	}

	autocallOptions := autocall.Options{
		AdditionalTools: []tool.Tool{
			functool.MustNew(&functool.Func{Name: "Func1"},
				func(ctx context.Context, args struct{}) (string, error) {
					t.Error("AdditionalTools Func1 should not be invoked")
					return "Should never be invoked", nil
				}),
			functool.MustNew(&functool.Func{Name: "Func2"},
				func(ctx context.Context, args Func2Args) (string, error) {
					return fmt.Sprintf("Result 2: %d", args.I), nil
				}),
			functool.MustNew(&functool.Func{Name: "VoidReturn"},
				func(ctx context.Context, args Func2Args) (string, error) {
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
		functool.MustNew(&functool.Func{Name: "Func"},
			func(ctx context.Context, args struct{ Arg string }) (string, error) {
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

	autocallOptions := autocall.Options{
		AllowConcurrentInvocations: param.NewOpt(true),
	}

	invokeAndAssert(t, tools, plan, nil, autocallOptions)
}

// TestFunctionInvoking_ConcurrentInvocationOfParallelCallsDisabledByDefault tests serial invocation by default
func TestFunctionInvoking_ConcurrentInvocationOfParallelCallsDisabledByDefault(t *testing.T) {
	var activeCount atomic.Int32

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "Func"},
			func(ctx context.Context, args struct{ Arg string }) (string, error) {
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

	invokeAndAssert(t, tools, plan, nil, autocall.Options{})
}

// TestFunctionInvoking_ContinuesWithSuccessfulCallsUntilMaximumIterations tests MaximumIterationsPerRequest
func TestFunctionInvoking_ContinuesWithSuccessfulCallsUntilMaximumIterations(t *testing.T) {
	maxIterations := 7
	actualCallCount := 0

	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "VoidReturn"},
			func(ctx context.Context, args struct{}) (string, error) {
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

	autocallOptions := autocall.Options{
		MaximumIterationsPerRequest: param.NewOpt(maxIterations),
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
				functool.MustNew(&functool.Func{Name: "Func"},
					func(ctx context.Context, args struct {
						ShouldThrow bool `json:"shouldThrow"`
						CallIndex   int  `json:"callIndex"`
					}) (string, error) {
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

			autocallOptions := autocall.Options{
				MaximumConsecutiveErrorsPerRequest: param.NewOpt(2),
				AllowConcurrentInvocations:         param.NewOpt(tt.allowConcurrentInvocations),
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
					rb.Add(&message.ResponseUpdate{
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
			var opts []agentopt.RunOption
			for _, tool := range tools {
				opts = append(opts, agentopt.Tool(tool))
			}

			var streamErr error
			for _, err := range autocall.New(autocallOptions).Run(t.Context(), runner.Run, initialMessages, opts...) {
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
				functool.MustNew(&functool.Func{Name: "Func"},
					func(ctx context.Context, args struct{}) (string, error) {
						return "", errors.New("It failed")
					}),
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, true)...)

			autocallOptions := autocall.Options{
				MaximumConsecutiveErrorsPerRequest: param.NewOpt(0),
				AllowConcurrentInvocations:         param.NewOpt(tt.allowConcurrentInvocations),
			}

			rb := agenttest.NewResponseBuilder()
			for i := range plan {
				idx := i*2 + 1
				if idx >= len(plan) {
					break
				}
				msg := plan[idx]
				for _, content := range msg.Contents {
					rb.Add(&message.ResponseUpdate{
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
			var opts []agentopt.RunOption
			for _, tool := range tools {
				opts = append(opts, agentopt.Tool(tool))
			}

			var streamErr error
			for _, err := range autocall.New(autocallOptions).Run(t.Context(), runner.Run, initialMessages, opts...) {
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
		functool.MustNew(&functool.Func{Name: "Func1"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "Result 1", nil
			}),
		functool.MustNew(&functool.Func{Name: "Func2"},
			func(ctx context.Context, args Func2Args) (string, error) {
				return fmt.Sprintf("Result 2: %d", args.I), nil
			}),
		functool.MustNew(&functool.Func{Name: "VoidReturn"},
			func(ctx context.Context, args Func2Args) (string, error) {
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

	finalChat := invokeAndAssert(t, tools, plan, nil, autocall.Options{})
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
				functool.MustNew(&functool.Func{Name: "KnownFunc"},
					func(ctx context.Context, args Func2Args) (string, error) {
						return fmt.Sprintf("Known: %d", args.I), nil
					}),
			}

			autocallOptions := autocall.Options{
				TerminateOnUnknownCalls: param.NewOpt(tt.terminateOnUnknownCalls),
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
				functool.MustNew(&functool.Func{Name: "Func1"},
					func(ctx context.Context, args struct{}) (string, error) {
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

			autocallOptions := autocall.Options{
				IncludeDetailedErrors: param.NewOpt(tt.detailedErrors),
			}

			invokeAndAssert(t, tools, plan, nil, autocallOptions)
		})
	}
}

// TestFunctionInvoking_AllResponseMessagesReturned tests that all response messages are returned
func TestFunctionInvoking_AllResponseMessagesReturned(t *testing.T) {
	tools := []tool.Tool{
		functool.MustNew(&functool.Func{Name: "Func1"},
			func(ctx context.Context, args struct{}) (string, error) {
				return "doesn't matter", nil
			}),
	}

	messages := []*message.Message{
		message.New(&message.TextContent{Text: "Hello"}),
	}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.FunctionCallContent{CallID: "callId0", Name: "Func1"}},
			}).
			NewTurn().
			Add(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.FunctionCallContent{CallID: "callId1", Name: "Func1"}},
			}).
			NewTurn().
			Add(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "The answer is 42."}},
			}).
			Build(),
	}

	initialMessages := []*message.Message{messages[0]}
	var opts []agentopt.RunOption
	for _, tool := range tools {
		opts = append(opts, agentopt.Tool(tool))
	}

	var resp message.Response
	for update, err := range autocall.New(autocall.Options{}).Run(t.Context(), runner.Run, initialMessages, opts...) {
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
