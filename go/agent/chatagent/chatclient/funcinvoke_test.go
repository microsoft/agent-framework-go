// Copyright (c) Microsoft. All rights reserved.

package chatclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework/go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/param"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

// testChatClient is a minimal test implementation of Client
type testChatClient struct {
	responseFunc          func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error)
	streamingResponseFunc func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error]
}

func (t *testChatClient) Response(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
	if t.responseFunc != nil {
		return t.responseFunc(ctx, opts, messages...)
	}
	return &chatclient.ChatResponse{}, nil
}

func (t *testChatClient) StreamingResponse(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
	if t.streamingResponseFunc != nil {
		return t.streamingResponseFunc(ctx, opts, messages...)
	}
	return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
		// Default empty implementation
	}
}

func TestFunctionInvoking_SupportsSingleFunctionCallPerRequest(t *testing.T) {
	type EmptyArgs struct{}
	type Func2Args struct {
		I int `json:"i"`
	}

	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
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
		},
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: json.RawMessage(`{"i":43}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssert(t, options, plan, nil, nil)
	invokeAndAssertStreaming(t, options, plan, nil, nil)
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
			options := &chatclient.ChatOptions{
				Tools: []tool.Tool{
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
				},
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: json.RawMessage(`{"i":null}`)},
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":34}`)},
					&message.FunctionCallContent{CallID: "callId3", Name: "Func2", Arguments: json.RawMessage(`{"i":56}`)},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
					&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 34"},
					&message.FunctionResultContent{CallID: "callId3", Result: "Result 2: 56"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId4", Name: "Func2", Arguments: json.RawMessage(`{"i":78}`)},
					&message.FunctionCallContent{CallID: "callId5", Name: "Func1", Arguments: json.RawMessage(`{"i":null}`)},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId4", Result: "Result 2: 78"},
					&message.FunctionResultContent{CallID: "callId5", Result: "Result 1"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "world"},
				}},
			}

			functionInvokingOptions := &chatclient.FunctionInvokingOptions{
				AllowConcurrentInvocations: param.NewOpt(tt.allowConcurrentInvocations),
			}

			invokeAndAssert(t, options, plan, nil, functionInvokingOptions)
			invokeAndAssertStreaming(t, options, plan, nil, functionInvokingOptions)
		})
	}
}

// invokeAndAssert is a helper that creates a test client following the given plan
// and asserts that the function-invoking client processes it correctly.
// Returns the final chat history.
// Plan should start with the initial user message and contain all expected messages.
// functionInvokingOptions can be nil to use default settings.
func invokeAndAssert(t *testing.T, options *chatclient.ChatOptions, plan []*message.Message, expected []*message.Message, functionInvokingOptions *chatclient.FunctionInvokingOptions) []*message.Message {
	t.Helper()

	if len(plan) == 0 {
		t.Fatal("plan must not be empty")
	}

	if expected == nil {
		expected = plan
	}

	innerClient := &testChatClient{
		responseFunc: func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
			idx := len(messages)
			if idx >= len(plan) {
				t.Fatalf("unexpected call to Response, message count=%d, len(plan)=%d", idx, len(plan))
			}
			msg := plan[idx]
			return &chatclient.ChatResponse{Messages: []*message.Message{msg}}, nil
		},
	}

	client := chatclient.NewFunctionInvoking(innerClient, functionInvokingOptions)
	ctx := context.Background()
	initialMessages := []*message.Message{plan[0]}

	response, err := client.Response(ctx, options, initialMessages...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if response == nil {
		t.Fatal("expected non-nil response")
	}

	// Build actual chat history
	actual := append(initialMessages, response.Messages...)

	// Assert messages match expected
	assertMessageListsEqual(t, expected, actual)

	return actual
}

// invokeAndAssertStreaming is similar to invokeAndAssert but tests streaming response.
// functionInvokingOptions can be nil to use default settings.
func invokeAndAssertStreaming(t *testing.T, options *chatclient.ChatOptions, plan []*message.Message, expected []*message.Message, functionInvokingOptions *chatclient.FunctionInvokingOptions) []*message.Message {
	t.Helper()

	if len(plan) == 0 {
		t.Fatal("plan must not be empty")
	}

	if expected == nil {
		expected = plan
	}

	innerClient := &testChatClient{
		streamingResponseFunc: func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
			return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
				idx := len(messages)
				if idx >= len(plan) {
					t.Fatalf("unexpected call to StreamingResponse, message count=%d, len(plan)=%d", idx, len(plan))
				}
				msg := plan[idx]

				// Convert message to updates
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
		},
	}

	client := chatclient.NewFunctionInvoking(innerClient, functionInvokingOptions)
	ctx := context.Background()
	initialMessages := []*message.Message{plan[0]}

	// Collect all updates
	var updates []*chatclient.ChatResponseUpdate
	for update, err := range client.StreamingResponse(ctx, options, initialMessages...) {
		if err != nil {
			t.Fatalf("unexpected error during streaming: %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) == 0 {
		t.Fatal("expected at least one update")
	}

	// Convert streaming updates back to consolidated messages
	consolidated := chatclient.NewChatResponseFromUpdates(updates)

	// Build actual chat history
	actual := append(initialMessages, consolidated.Messages...)

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

// TestFunctionInvoking_InvalidArgs tests that NewFunctionInvoking panics with nil client
func TestFunctionInvoking_InvalidArgs(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewFunctionInvoking with nil client should panic")
		}
	}()

	chatclient.NewFunctionInvoking(nil, nil)
	t.Error("Should not reach here - NewFunctionInvoking should have panicked")
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
		{"without_chat_options_tools", false},
		{"with_chat_options_tools", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var options *chatclient.ChatOptions
			if tt.provideOptions {
				options = &chatclient.ChatOptions{
					Tools: []tool.Tool{
						functool.MustNew(&functool.Func{Name: "ChatOptionsFunc"},
							func(ctx context.Context, args struct{}) (string, error) {
								t.Error("ChatOptionsFunc should not be invoked")
								return "Shouldn't be invoked", nil
							}),
					},
				}
			}

			functionInvokingOptions := &chatclient.FunctionInvokingOptions{
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
					&message.FunctionCallContent{CallID: "callId1", Name: "Func1", Arguments: json.RawMessage(`{}`)},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId1", Result: "Result 1"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: json.RawMessage(`{"i":43}`)},
				}},
				{Role: message.RoleTool, Contents: []message.Content{
					&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
				}},
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "world"},
				}},
			}

			invokeAndAssert(t, options, plan, nil, functionInvokingOptions)
			invokeAndAssertStreaming(t, options, plan, nil, functionInvokingOptions)
		})
	}
}

// TestFunctionInvoking_PrefersToolsProvidedByChatOptions tests that ChatOptions.Tools take precedence over AdditionalTools
func TestFunctionInvoking_PrefersToolsProvidedByChatOptions(t *testing.T) {
	type Func2Args struct {
		I int `json:"i"`
	}

	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			functool.MustNew(&functool.Func{Name: "Func1"},
				func(ctx context.Context, args struct{}) (string, error) {
					return "Result 1", nil
				}),
		},
	}

	functionInvokingOptions := &chatclient.FunctionInvokingOptions{
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
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: json.RawMessage(`{"i":43}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	invokeAndAssert(t, options, plan, nil, functionInvokingOptions)
	invokeAndAssertStreaming(t, options, plan, nil, functionInvokingOptions)
}

// TestFunctionInvoking_ParallelFunctionCallsMayBeInvokedConcurrently tests concurrent invocation
func TestFunctionInvoking_ParallelFunctionCallsMayBeInvokedConcurrently(t *testing.T) {
	t.Run("Response", func(t *testing.T) {
		var remaining atomic.Int32
		remaining.Store(2)
		done := make(chan bool)

		options := &chatclient.ChatOptions{
			Tools: []tool.Tool{
				functool.MustNew(&functool.Func{Name: "Func"},
					func(ctx context.Context, args struct{ Arg string }) (string, error) {
						if remaining.Add(-1) == 0 {
							close(done)
						}
						<-done
						return args.Arg + args.Arg, nil
					}),
			},
		}

		plan := []*message.Message{
			message.New(&message.TextContent{Text: "hello"}),
			{Role: message.RoleAssistant, Contents: []message.Content{
				&message.FunctionCallContent{CallID: "callId1", Name: "Func", Arguments: json.RawMessage(`{"Arg":"hello"}`)},
				&message.FunctionCallContent{CallID: "callId2", Name: "Func", Arguments: json.RawMessage(`{"Arg":"world"}`)},
			}},
			{Role: message.RoleTool, Contents: []message.Content{
				&message.FunctionResultContent{CallID: "callId1", Result: "hellohello"},
				&message.FunctionResultContent{CallID: "callId2", Result: "worldworld"},
			}},
			{Role: message.RoleAssistant, Contents: []message.Content{
				&message.TextContent{Text: "done"},
			}},
		}

		functionInvokingOptions := &chatclient.FunctionInvokingOptions{
			AllowConcurrentInvocations: param.NewOpt(true),
		}

		invokeAndAssert(t, options, plan, nil, functionInvokingOptions)
	})

	t.Run("StreamingResponse", func(t *testing.T) {
		var remaining atomic.Int32
		remaining.Store(2)
		done := make(chan bool)

		options := &chatclient.ChatOptions{
			Tools: []tool.Tool{
				functool.MustNew(&functool.Func{Name: "Func"},
					func(ctx context.Context, args struct{ Arg string }) (string, error) {
						if remaining.Add(-1) == 0 {
							close(done)
						}
						<-done
						return args.Arg + args.Arg, nil
					}),
			},
		}

		plan := []*message.Message{
			message.New(&message.TextContent{Text: "hello"}),
			{Role: message.RoleAssistant, Contents: []message.Content{
				&message.FunctionCallContent{CallID: "callId1", Name: "Func", Arguments: json.RawMessage(`{"Arg":"hello"}`)},
				&message.FunctionCallContent{CallID: "callId2", Name: "Func", Arguments: json.RawMessage(`{"Arg":"world"}`)},
			}},
			{Role: message.RoleTool, Contents: []message.Content{
				&message.FunctionResultContent{CallID: "callId1", Result: "hellohello"},
				&message.FunctionResultContent{CallID: "callId2", Result: "worldworld"},
			}},
			{Role: message.RoleAssistant, Contents: []message.Content{
				&message.TextContent{Text: "done"},
			}},
		}

		functionInvokingOptions := &chatclient.FunctionInvokingOptions{
			AllowConcurrentInvocations: param.NewOpt(true),
		}

		invokeAndAssertStreaming(t, options, plan, nil, functionInvokingOptions)
	})
}

// TestFunctionInvoking_ConcurrentInvocationOfParallelCallsDisabledByDefault tests serial invocation by default
func TestFunctionInvoking_ConcurrentInvocationOfParallelCallsDisabledByDefault(t *testing.T) {
	var activeCount atomic.Int32

	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
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
		},
	}

	plan := []*message.Message{
		message.New(&message.TextContent{Text: "hello"}),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId1", Name: "Func", Arguments: json.RawMessage(`{"Arg":"hello"}`)},
			&message.FunctionCallContent{CallID: "callId2", Name: "Func", Arguments: json.RawMessage(`{"Arg":"world"}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId1", Result: "hellohello"},
			&message.FunctionResultContent{CallID: "callId2", Result: "worldworld"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "done"},
		}},
	}

	invokeAndAssert(t, options, plan, nil, nil)
	invokeAndAssertStreaming(t, options, plan, nil, nil)
}

// TestFunctionInvoking_ContinuesWithSuccessfulCallsUntilMaximumIterations tests MaximumIterationsPerRequest
func TestFunctionInvoking_ContinuesWithSuccessfulCallsUntilMaximumIterations(t *testing.T) {
	maxIterations := 7
	actualCallCount := 0

	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			functool.MustNew(&functool.Func{Name: "VoidReturn"},
				func(ctx context.Context, args struct{}) (string, error) {
					actualCallCount++
					return "Success: Function completed.", nil
				}),
		},
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

	functionInvokingOptions := &chatclient.FunctionInvokingOptions{
		MaximumIterationsPerRequest: param.NewOpt(maxIterations),
	}

	invokeAndAssert(t, options, plan, expectedPlan, functionInvokingOptions)

	if actualCallCount != maxIterations {
		t.Errorf("Expected %d function calls, got %d", maxIterations, actualCallCount)
	}

	actualCallCount = 0
	invokeAndAssertStreaming(t, options, plan, expectedPlan, functionInvokingOptions)

	if actualCallCount != maxIterations {
		t.Errorf("Expected %d function calls (streaming), got %d", maxIterations, actualCallCount)
	}
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

			options := &chatclient.ChatOptions{
				Tools: []tool.Tool{
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
				},
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

			functionInvokingOptions := &chatclient.FunctionInvokingOptions{
				MaximumConsecutiveErrorsPerRequest: param.NewOpt(2),
				AllowConcurrentInvocations:         param.NewOpt(tt.allowConcurrentInvocations),
			}

			// The test expects an error to be thrown
			innerClient := &testChatClient{
				responseFunc: func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
					idx := len(messages)
					if idx >= len(plan) {
						t.Fatalf("unexpected call to Response, message count=%d, len(plan)=%d", idx, len(plan))
					}
					msg := plan[idx]
					return &chatclient.ChatResponse{Messages: []*message.Message{msg}}, nil
				},
			}

			client := chatclient.NewFunctionInvoking(innerClient, functionInvokingOptions)
			ctx := context.Background()
			initialMessages := []*message.Message{plan[0]}

			_, err := client.Response(ctx, options, initialMessages...)
			if err == nil {
				t.Error("Expected error due to MaximumConsecutiveErrors exceeded, got nil")
			} else if !errors.Is(err, context.Canceled) && err.Error() != "maximum consecutive function call errors reached" {
				// Check for expected error message
				t.Logf("Got error: %v", err)
			}

			// Test streaming as well
			innerClient.streamingResponseFunc = func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
				return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
					idx := len(messages)
					if idx >= len(plan) {
						t.Fatalf("unexpected call to StreamingResponse, message count=%d, len(plan)=%d", idx, len(plan))
					}
					msg := plan[idx]

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

			var streamErr error
			for _, err := range client.StreamingResponse(ctx, options, initialMessages...) {
				if err != nil {
					streamErr = err
					break
				}
			}

			if streamErr == nil {
				t.Error("Expected error in streaming due to MaximumConsecutiveErrors exceeded, got nil")
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
			Arguments: json.RawMessage(arguments),
		})

		result := "Success"
		if callShouldThrow {
			result = "Error: Function failed."
		}
		toolContents = append(toolContents, &message.FunctionResultContent{
			CallID: callID,
			Result: result,
		})
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

			options := &chatclient.ChatOptions{
				Tools: []tool.Tool{
					functool.MustNew(&functool.Func{Name: "Func"},
						func(ctx context.Context, args struct{}) (string, error) {
							return "", errors.New("It failed")
						}),
				},
			}

			plan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
			}
			plan = append(plan, createFunctionCallIterationPlan(&callIndex, true)...)

			functionInvokingOptions := &chatclient.FunctionInvokingOptions{
				MaximumConsecutiveErrorsPerRequest: param.NewOpt(0),
				AllowConcurrentInvocations:         param.NewOpt(tt.allowConcurrentInvocations),
			}

			innerClient := &testChatClient{
				responseFunc: func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
					idx := len(messages)
					if idx >= len(plan) {
						t.Fatalf("unexpected call to Response")
					}
					msg := plan[idx]
					return &chatclient.ChatResponse{Messages: []*message.Message{msg}}, nil
				},
			}

			client := chatclient.NewFunctionInvoking(innerClient, functionInvokingOptions)
			ctx := context.Background()

			_, err := client.Response(ctx, options, plan[0])
			if err == nil {
				t.Error("Expected error on first exception, got nil")
			}

			// Test streaming
			innerClient.streamingResponseFunc = func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
				return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
					idx := len(messages)
					if idx >= len(plan) {
						t.Fatalf("unexpected call to StreamingResponse")
					}
					msg := plan[idx]
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

			var streamErr error
			for _, err := range client.StreamingResponse(ctx, options, plan[0]) {
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

	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
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
		},
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
			&message.FunctionCallContent{CallID: "callId2", Name: "Func2", Arguments: json.RawMessage(`{"i":42}`)},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId2", Result: "Result 2: 42"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "callId3", Name: "VoidReturn", Arguments: json.RawMessage(`{"i":43}`)},
			&message.TextContent{Text: "more"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "callId3", Result: "Success: Function completed."},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "world"},
		}},
	}

	finalChat := invokeAndAssert(t, options, plan, nil, nil)
	validateFunctionContent(t, finalChat)

	finalChat = invokeAndAssertStreaming(t, options, plan, nil, nil)
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

			options := &chatclient.ChatOptions{
				Tools: []tool.Tool{
					functool.MustNew(&functool.Func{Name: "KnownFunc"},
						func(ctx context.Context, args Func2Args) (string, error) {
							return fmt.Sprintf("Known: %d", args.I), nil
						}),
				},
			}

			functionInvokingOptions := &chatclient.FunctionInvokingOptions{
				TerminateOnUnknownCalls: param.NewOpt(tt.terminateOnUnknownCalls),
			}

			fullPlan := []*message.Message{
				message.New(&message.TextContent{Text: "hello"}),
				{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "callId1", Name: "UnknownFunc", Arguments: json.RawMessage(`{"i":1}`)},
					&message.FunctionCallContent{CallID: "callId2", Name: "KnownFunc", Arguments: json.RawMessage(`{"i":2}`)},
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
				invokeAndAssert(t, options, fullPlan, expectedPlan, functionInvokingOptions)
				invokeAndAssertStreaming(t, options, fullPlan, expectedPlan, functionInvokingOptions)
			} else {
				// Should continue and add error result for unknown function
				invokeAndAssert(t, options, fullPlan, nil, functionInvokingOptions)
				invokeAndAssertStreaming(t, options, fullPlan, nil, functionInvokingOptions)
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
			options := &chatclient.ChatOptions{
				Tools: []tool.Tool{
					functool.MustNew(&functool.Func{Name: "Func1"},
						func(ctx context.Context, args struct{}) (string, error) {
							return "", errors.New("Oh no!")
						}),
				},
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

			functionInvokingOptions := &chatclient.FunctionInvokingOptions{
				IncludeDetailedErrors: param.NewOpt(tt.detailedErrors),
			}

			invokeAndAssert(t, options, plan, nil, functionInvokingOptions)
			invokeAndAssertStreaming(t, options, plan, nil, functionInvokingOptions)
		})
	}
}

// TestFunctionInvoking_AllResponseMessagesReturned tests that all response messages are returned
func TestFunctionInvoking_AllResponseMessagesReturned(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			functool.MustNew(&functool.Func{Name: "Func1"},
				func(ctx context.Context, args struct{}) (string, error) {
					return "doesn't matter", nil
				}),
		},
	}

	messages := []*message.Message{
		message.New(&message.TextContent{Text: "Hello"}),
	}

	callCount := 0
	innerClient := &testChatClient{
		responseFunc: func(ctx context.Context, opts *chatclient.ChatOptions, msgs ...*message.Message) (*chatclient.ChatResponse, error) {
			callCount++
			var msg *message.Message
			if len(msgs) == 1 || len(msgs) == 3 {
				// First and third call - return function call
				msg = &message.Message{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: fmt.Sprintf("callId%d", len(msgs)), Name: "Func1"},
				}}
			} else {
				// Second call - return final answer
				msg = &message.Message{Role: message.RoleAssistant, Contents: []message.Content{
					&message.TextContent{Text: "The answer is 42."},
				}}
			}
			return &chatclient.ChatResponse{Messages: []*message.Message{msg}}, nil
		},
	}

	client := chatclient.NewFunctionInvoking(innerClient, nil)
	ctx := context.Background()

	response, err := client.Response(ctx, options, messages...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(response.Messages) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(response.Messages))
	}

	if response.String() != "The answer is 42." {
		t.Errorf("Expected text 'The answer is 42.', got %q", response.String())
	}

	// Verify message types
	if _, ok := response.Messages[0].Contents[0].(*message.FunctionCallContent); !ok {
		t.Error("Expected first message to be FunctionCallContent")
	}
	if _, ok := response.Messages[1].Contents[0].(*message.FunctionResultContent); !ok {
		t.Error("Expected second message to be FunctionResultContent")
	}
	if _, ok := response.Messages[2].Contents[0].(*message.FunctionCallContent); !ok {
		t.Error("Expected third message to be FunctionCallContent")
	}
	if _, ok := response.Messages[3].Contents[0].(*message.FunctionResultContent); !ok {
		t.Error("Expected fourth message to be FunctionResultContent")
	}
	if _, ok := response.Messages[4].Contents[0].(*message.TextContent); !ok {
		t.Error("Expected fifth message to be TextContent")
	}
}

// TestFunctionInvoking_PropagatesResponseConversationIdToOptions tests conversation ID propagation
func TestFunctionInvoking_PropagatesResponseConversationIdToOptions(t *testing.T) {
	options := &chatclient.ChatOptions{
		Tools: []tool.Tool{
			functool.MustNew(&functool.Func{Name: "Func1"},
				func(ctx context.Context, args struct{}) (string, error) {
					return "Result 1", nil
				}),
		},
	}

	iteration := 0
	responseFunc := func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
		iteration++

		switch iteration {
		case 1:
			if opts != nil && opts.ConversationID != "" {
				t.Errorf("First call should have empty ConversationID, got %q", opts.ConversationID)
			}
			return &chatclient.ChatResponse{
				Messages:       []*message.Message{{Role: message.RoleAssistant, Contents: []message.Content{&message.FunctionCallContent{CallID: "callId-abc", Name: "Func1"}}}},
				ConversationID: "12345",
			}, nil
		case 2:
			if opts == nil || opts.ConversationID != "12345" {
				t.Errorf("Second call should have ConversationID '12345', got %q", opts.ConversationID)
			}
			return &chatclient.ChatResponse{
				Messages: []*message.Message{{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "done!"}}}},
			}, nil
		default:
			t.Fatal("Unexpected iteration")
			return nil, nil
		}
	}

	innerClient := &testChatClient{
		responseFunc: responseFunc,
		streamingResponseFunc: func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
			return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
				var response *chatclient.ChatResponse
				var err error
				if responseFunc != nil {
					response, err = responseFunc(ctx, opts, messages...)
					if err != nil {
						yield(nil, err)
						return
					}
				}
				if response != nil {
					for _, msg := range response.Messages {
						for _, content := range msg.Contents {
							update := &chatclient.ChatResponseUpdate{
								Role:           msg.Role,
								Contents:       []message.Content{content},
								ConversationID: response.ConversationID,
							}
							if !yield(update, nil) {
								return
							}
						}
					}
				}
			}
		},
	}

	client := chatclient.NewFunctionInvoking(innerClient, nil)
	ctx := context.Background()

	iteration = 0
	response, err := client.Response(ctx, options, message.New(&message.TextContent{Text: "hey"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response.String() != "done!" {
		t.Errorf("Expected 'done!', got %q", response.String())
	}

	iteration = 0
	response, err = nil, nil
	for update, e := range client.StreamingResponse(ctx, options, message.New(&message.TextContent{Text: "hey"})) {
		if e != nil {
			err = e
			break
		}
		if response == nil {
			response = &chatclient.ChatResponse{}
		}
		// Consolidate updates
		if len(response.Messages) == 0 || response.Messages[len(response.Messages)-1].Role != update.Role {
			response.Messages = append(response.Messages, &message.Message{Role: update.Role, Contents: update.Contents})
		} else {
			response.Messages[len(response.Messages)-1].Contents = append(response.Messages[len(response.Messages)-1].Contents, update.Contents...)
		}
	}
	if err != nil {
		t.Fatalf("unexpected streaming error: %v", err)
	}
	if response.String() != "done!" {
		t.Errorf("Expected streaming 'done!', got %q", response.String())
	}
}

// TestFunctionInvoking_ClonesChatOptionsAndResetContinuationToken tests continuation token handling
func TestFunctionInvoking_ClonesChatOptionsAndResetContinuationToken(t *testing.T) {
	var actualChatOptions *chatclient.ChatOptions

	innerClient := &testChatClient{
		responseFunc: func(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
			actualChatOptions = opts

			// Simulate the model returning a function call for the first call only
			hasFunctionCall := false
			for _, m := range messages {
				for _, c := range m.Contents {
					if _, ok := c.(*message.FunctionCallContent); ok {
						hasFunctionCall = true
						break
					}
				}
			}

			if !hasFunctionCall {
				return &chatclient.ChatResponse{
					Messages: []*message.Message{{Role: message.RoleAssistant, Contents: []message.Content{&message.FunctionCallContent{CallID: "callId1", Name: "Func1"}}}},
				}, nil
			}

			return &chatclient.ChatResponse{Messages: []*message.Message{}}, nil
		},
	}

	client := chatclient.NewFunctionInvoking(innerClient, nil)
	ctx := context.Background()

	originalChatOptions := &chatclient.ChatOptions{
		Tools:             []tool.Tool{functool.MustNew(&functool.Func{Name: "Func1"}, func(ctx context.Context, args struct{}) (string, error) { return "", nil })},
		ContinuationToken: []byte{1, 2, 3, 4},
	}

	_, err := client.Response(ctx, originalChatOptions, message.New(&message.TextContent{Text: "hi"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The original options should be cloned and have a nil ContinuationToken
	if actualChatOptions == originalChatOptions {
		t.Error("ChatOptions should have been cloned")
	}
	if actualChatOptions.ContinuationToken != nil {
		t.Error("ContinuationToken should be nil in cloned options")
	}
}
