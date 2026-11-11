// Copyright (c) Microsoft. All rights reserved.

package mcptool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTaskAwareMcpClientAIFunction_InvokeAsync_TaskBackedToolReturnsResult(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("task-tool", taskTextTool("task-result")))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "task-tool")
	result, err := fn.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustTextResult(t, result); got != "task-result" {
		t.Fatalf("result = %q, want task-result", got)
	}
	created, polls, _, _, _ := server.counts()
	if created != 1 || polls == 0 {
		t.Fatalf("created tasks = %d, polls = %d, want one task and at least one poll", created, polls)
	}
}

func TestTaskAwareToolsStreamableHTTP(t *testing.T) {
	for name, jsonResponse := range map[string]bool{"json": true, "sse": false} {
		t.Run(name, func(t *testing.T) {
			for _, scenario := range []string{"complete", "input", "cancel", "inline"} {
				t.Run(scenario, func(t *testing.T) {
					config := taskTestConfig{
						disableTasks:      scenario == "inline",
						inputCapabilities: map[string]any{"elicitation": map[string]any{"form": map[string]any{}}},
					}
					handler := taskTextTool("completed")
					if scenario == "input" {
						config.initialStatus = "input_required"
						config.inputRequests = []mcp.InputRequest{elicitationRequest("Confirm operation.")}
					}
					if scenario == "cancel" {
						handler = blockingTaskTool(nil)
					}
					server := newTaskTestServer(t, config, taskTestTool("task-tool", handler))
					httpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
						return server.server
					}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: jsonResponse})
					httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if strings.HasPrefix(r.Header.Get("Mcp-Method"), "tasks/") && r.Header.Get("Mcp-Name") != "task-1" {
							http.Error(w, "missing or incorrect task routing header", http.StatusBadRequest)
							return
						}
						httpHandler.ServeHTTP(w, r)
					}))
					t.Cleanup(httpServer.Close)
					session, err := mcptool.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := session.Close(); err != nil {
							t.Error(err)
						}
					})
					tools, err := mcptool.ListToolsWithTasks(t.Context(), session, mcptool.TaskOptions{
						InputHandler:            acceptInputHandler,
						ElicitationCapabilities: &mcp.ElicitationCapabilities{},
					})
					if err != nil {
						t.Fatal(err)
					}
					fn := findFuncTool(t, tools, "task-tool")
					if scenario == "cancel" {
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						done := make(chan error, 1)
						go func() {
							_, err := fn.Call(ctx, `{}`)
							done <- err
						}()
						select {
						case <-server.firstPollObserved:
						case err := <-done:
							t.Fatalf("call ended before polling: %v", err)
						}
						cancel()
						if err := <-done; !errors.Is(err, context.Canceled) {
							t.Fatalf("Call() error = %v, want context.Canceled", err)
						}
						_, _, _, cancels, transitions := server.counts()
						if cancels != 1 || transitions != 1 {
							t.Fatalf("cancellation requests = %d, transitions = %d, want 1 each", cancels, transitions)
						}
						return
					}
					result, err := fn.Call(t.Context(), `{}`)
					if err != nil {
						t.Fatal(err)
					}
					if got := mustTextResult(t, result); got != "completed" {
						t.Fatalf("result = %q, want completed", got)
					}
					created, polls, inputs, _, _ := server.counts()
					if scenario == "inline" {
						if created != 0 {
							t.Fatalf("created tasks = %d, want 0", created)
						}
					} else if created != 1 || polls == 0 {
						t.Fatalf("created tasks = %d, polls = %d, want one task and at least one poll", created, polls)
					}
					if scenario == "input" && inputs != 1 {
						t.Fatalf("input responses = %d, want 1", inputs)
					}
				})
			}
		})
	}
}

func TestTaskAwareInputCapabilities(t *testing.T) {
	form := elicitationRequest("Confirm operation.")
	url := &mcp.ElicitParams{
		Mode:          "url",
		Message:       "Confirm operation.",
		URL:           "https://example.com/confirm",
		ElicitationID: "confirmation",
	}
	sampling := &mcp.CreateMessageWithToolsParams{ //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
		MaxTokens: 32,
		Messages: []*mcp.SamplingMessageV2{{ //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
			Role:    "user",
			Content: []mcp.Content{&mcp.TextContent{Text: "Summarize this operation."}},
		}},
	}
	for _, tt := range []struct {
		name     string
		options  mcptool.TaskOptions
		requests []mcp.InputRequest
		want     string
	}{
		{
			name: "no input support",
			want: `{}`,
		},
		{
			name:     "default form",
			options:  mcptool.TaskOptions{ElicitationCapabilities: &mcp.ElicitationCapabilities{}},
			requests: []mcp.InputRequest{form},
			want:     `{"elicitation":{"form":{}}}`,
		},
		{
			name: "explicit form",
			options: mcptool.TaskOptions{ElicitationCapabilities: &mcp.ElicitationCapabilities{
				Form: &mcp.FormElicitationCapabilities{},
			}},
			requests: []mcp.InputRequest{form},
			want:     `{"elicitation":{"form":{}}}`,
		},
		{
			name: "url only",
			options: mcptool.TaskOptions{ElicitationCapabilities: &mcp.ElicitationCapabilities{
				URL: &mcp.URLElicitationCapabilities{},
			}},
			requests: []mcp.InputRequest{url},
			want:     `{"elicitation":{"url":{}}}`,
		},
		{
			name: "both elicitation modes",
			options: mcptool.TaskOptions{ElicitationCapabilities: &mcp.ElicitationCapabilities{
				Form: &mcp.FormElicitationCapabilities{},
				URL:  &mcp.URLElicitationCapabilities{},
			}},
			requests: []mcp.InputRequest{form, url},
			want:     `{"elicitation":{"form":{},"url":{}}}`,
		},
		{
			name:     "basic sampling",
			options:  mcptool.TaskOptions{SamplingCapabilities: &mcp.SamplingCapabilities{}}, //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
			requests: []mcp.InputRequest{sampling},
			want:     `{"sampling":{}}`,
		},
		{
			name: "sampling with context and tools",
			options: mcptool.TaskOptions{SamplingCapabilities: &mcp.SamplingCapabilities{ //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
				Context: &mcp.SamplingContextCapabilities{}, //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
				Tools:   &mcp.SamplingToolsCapabilities{},   //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
			}},
			requests: []mcp.InputRequest{sampling},
			want:     `{"sampling":{"context":{},"tools":{}}}`,
		},
		{
			name: "elicitation and sampling",
			options: mcptool.TaskOptions{
				ElicitationCapabilities: &mcp.ElicitationCapabilities{},
				SamplingCapabilities:    &mcp.SamplingCapabilities{}, //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
			},
			requests: []mcp.InputRequest{form, sampling},
			want:     `{"elicitation":{"form":{}},"sampling":{}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var capabilities map[string]any
			if err := json.Unmarshal([]byte(tt.want), &capabilities); err != nil {
				t.Fatal(err)
			}
			config := taskTestConfig{
				initialStatus:     "completed",
				inputCapabilities: capabilities,
				inputRequests:     tt.requests,
			}
			handled := 0
			if len(tt.requests) != 0 {
				config.initialStatus = "input_required"
				tt.options.InputHandler = func(ctx context.Context, request mcp.InputRequest) (mcp.InputResponse, error) {
					handled++
					return acceptInputHandler(ctx, request)
				}
			}
			server := newTaskTestServer(t, config, taskTestTool("task-tool", taskTextTool("inline")))
			fn := getTaskTool(t, server, tt.options, "task-tool")
			result, err := fn.Call(t.Context(), `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustTextResult(t, result); got != "completed" {
				t.Fatalf("result = %q, want completed", got)
			}
			created, polls, inputs, cancels, _ := server.counts()
			if created != 1 || polls == 0 || inputs != len(tt.requests) || handled != len(tt.requests) || cancels != 0 {
				t.Fatalf("created = %d, polls = %d, inputs = %d, handled = %d, cancels = %d", created, polls, inputs, handled, cancels)
			}
			ordinary, err := mcptool.ListTools(t.Context(), server.client)
			if err != nil {
				t.Fatal(err)
			}
			result, err = findFuncTool(t, ordinary, "task-tool").Call(t.Context(), `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustTextResult(t, result); got != "inline" {
				t.Fatalf("ordinary result = %q, want inline", got)
			}
		})
	}
}

func TestTaskAwareInputCapabilitiesAreIsolated(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{initialStatus: "completed"},
		taskTestTool("task-tool", taskTextTool("completed")))
	observed := make(chan map[string]any, 5)
	server.server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				capabilities, _ := request.GetParams().GetMeta()[mcp.MetaKeyClientCapabilities].(map[string]any)
				observed <- taskInputCapabilities(capabilities)
			}
			return next(ctx, method, request)
		}
	})
	elicitation := getTaskTool(t, server, mcptool.TaskOptions{
		InputHandler:            acceptInputHandler,
		ElicitationCapabilities: &mcp.ElicitationCapabilities{},
	}, "task-tool")
	sampling := getTaskTool(t, server, mcptool.TaskOptions{
		InputHandler:         acceptInputHandler,
		SamplingCapabilities: &mcp.SamplingCapabilities{}, //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
	}, "task-tool")
	noInputs := getTaskTool(t, server, mcptool.TaskOptions{}, "task-tool")
	ordinary, err := mcptool.ListTools(t.Context(), server.client)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		fn   tool.FuncTool
		want string
	}{
		{elicitation, `{"elicitation":{"form":{}}}`},
		{sampling, `{"sampling":{}}`},
		{noInputs, `{}`},
		{findFuncTool(t, ordinary, "task-tool"), `{}`},
		{elicitation, `{"elicitation":{"form":{}}}`},
	} {
		result, err := tt.fn.Call(t.Context(), `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustTextResult(t, result); got != "completed" {
			t.Fatalf("result = %q, want completed", got)
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
			t.Fatal(err)
		}
		if got := <-observed; !reflect.DeepEqual(got, want) {
			t.Fatalf("input capabilities = %v, want %v", got, want)
		}
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_InvalidInitialPollIntervalCancelsRemoteTask(t *testing.T) {
	goRuntimeMaximum := (1<<63-1)/int64(time.Millisecond) + 1
	for _, interval := range []int64{0, -1, goRuntimeMaximum} {
		t.Run(fmt.Sprint(interval), func(t *testing.T) {
			server := newTaskTestServer(t, taskTestConfig{
				initialPollIntervalMS: &interval,
			}, taskTestTool("invalid-interval-tool", blockingTaskTool(nil)))
			fn := getTaskTool(t, server, mcptool.TaskOptions{}, "invalid-interval-tool")
			_, err := fn.Call(t.Context(), `{}`)
			assertTaskErrorContains(t, err, fmt.Sprintf("pollIntervalMs of %d", interval))
			waitForSignal(t, server.remoteCancelObserved)
			created, _, _, cancels, transitions := server.counts()
			if created != 1 || cancels != 1 || transitions != 1 {
				t.Fatalf("task counts = created %d, cancellation requests %d, transitions %d", created, cancels, transitions)
			}
		})
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_InvalidUpdatedPollIntervalCancelsRemoteTask(t *testing.T) {
	initial, updated := int64(10), int64(0)
	server := newTaskTestServer(t, taskTestConfig{
		initialPollIntervalMS: &initial,
		updatedPollIntervalMS: &updated,
	}, taskTestTool("updated-interval-tool", blockingTaskTool(nil)))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "updated-interval-tool")
	_, err := fn.Call(t.Context(), `{}`)
	assertTaskErrorContains(t, err, "pollIntervalMs of 0")
	waitForSignal(t, server.remoteCancelObserved)
	_, _, _, cancels, transitions := server.counts()
	if cancels != 1 || transitions != 1 {
		t.Fatalf("cancellation requests = %d, transitions = %d, want 1 each", cancels, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_ConfiguredPollingRangeAcceptsShortServerInterval(t *testing.T) {
	initial := int64(1)
	release := make(chan struct{})
	server := newTaskTestServer(t, taskTestConfig{
		initialPollIntervalMS: &initial,
		updatedPollIntervalMS: &initial,
	}, taskTestTool("short-interval-tool", blockingTaskTool(release)))
	options := mcptool.TaskOptions{}
	options.MinimumPollingInterval = time.Millisecond
	options.MaximumPollingInterval = time.Second
	fn := getTaskTool(t, server, options, "short-interval-tool")
	resultCh := make(chan struct {
		result any
		err    error
	}, 1)
	go func() {
		result, err := fn.Call(t.Context(), `{}`)
		resultCh <- struct {
			result any
			err    error
		}{result: result, err: err}
	}()
	waitForSignal(t, server.firstPollObserved)
	waitForSignal(t, server.secondPollObserved)
	close(release)
	outcome := <-resultCh
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if got := mustTextResult(t, outcome.result); got != "completed" {
		t.Fatalf("result = %q, want completed", got)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_MissingPollIntervalConstrainsFallbackToConfiguredRange(t *testing.T) {
	release := make(chan struct{})
	server := newTaskTestServer(t, taskTestConfig{
		omitPollIntervals: true,
	}, taskTestTool("missing-interval-tool", blockingTaskTool(release)))
	options := mcptool.TaskOptions{}
	options.MinimumPollingInterval = 10 * time.Millisecond
	options.MaximumPollingInterval = 100 * time.Millisecond
	fn := getTaskTool(t, server, options, "missing-interval-tool")
	resultCh := make(chan struct {
		result any
		err    error
	}, 1)
	go func() {
		result, err := fn.Call(t.Context(), `{}`)
		resultCh <- struct {
			result any
			err    error
		}{result: result, err: err}
	}()
	waitForSignal(t, server.firstPollObserved)
	waitForSignal(t, server.secondPollObserved)
	close(release)
	outcome := <-resultCh
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if got := mustTextResult(t, outcome.result); got != "completed" {
		t.Fatalf("result = %q, want completed", got)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_ServerWithoutTasksReturnsInlineResult(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{disableTasks: true},
		taskTestTool("inline-tool", taskTextTool("inline-result")))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "inline-tool")
	result, err := fn.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustTextResult(t, result); got != "inline-result" {
		t.Fatalf("result = %q, want inline-result", got)
	}
	created, _, _, _, _ := server.counts()
	if created != 0 {
		t.Fatalf("created tasks = %d, want 0", created)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_InputRequiredDispatchesClientHandler(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		initialStatus:   "input_required",
		inputRequests:   []mcp.InputRequest{elicitationRequest("Confirm operation.")},
		completedResult: taskInlineResult("accept:yes"),
	}, taskTestTool("input-tool", taskTextTool("unused")))
	options := mcptool.TaskOptions{}
	options.InputHandler = acceptInputHandler
	options.ElicitationCapabilities = &mcp.ElicitationCapabilities{}
	fn := getTaskTool(t, server, options, "input-tool")
	result, err := fn.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustTextResult(t, result); got != "accept:yes" {
		t.Fatalf("result = %q, want accept:yes", got)
	}
	created, _, inputs, _, _ := server.counts()
	if created != 1 || inputs != 1 {
		t.Fatalf("created tasks = %d, input responses = %d, want 1 each", created, inputs)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_ForwardsNullPrimitiveAndComplexArguments(t *testing.T) {
	var observed map[string]json.RawMessage
	handler := func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := json.Unmarshal(request.Params.Arguments, &observed); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	}
	server := newTaskTestServer(t, taskTestConfig{disableTasks: true}, taskTestTool("arguments-tool", handler))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "arguments-tool")
	if _, err := fn.Call(t.Context(), `{"optional":null,"count":3,"payload":{"label":"nested"}}`); err != nil {
		t.Fatal(err)
	}
	if string(observed["optional"]) != "null" || string(observed["count"]) != "3" {
		t.Fatalf("captured arguments = %s, want null optional and count 3", observed)
	}
	var payload map[string]string
	if err := json.Unmarshal(observed["payload"], &payload); err != nil {
		t.Fatal(err)
	}
	if payload["label"] != "nested" {
		t.Fatalf("payload = %v, want nested label", payload)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_SimpleResultMatchesMcpClientToolProjection(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("projection-tool", taskTextTool("projected-result")))
	ordinary, err := mcptool.ListTools(t.Context(), server.client)
	if err != nil {
		t.Fatal(err)
	}
	taskAware, err := mcptool.ListToolsWithTasks(t.Context(), server.client, mcptool.TaskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ordinaryFn := findFuncTool(t, ordinary, "projection-tool")
	taskFn := findFuncTool(t, taskAware, "projection-tool")
	ordinaryResult, err := ordinaryFn.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	taskResult, err := taskFn.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordinaryResult, taskResult) {
		t.Fatalf("task result = %#v, ordinary result = %#v", taskResult, ordinaryResult)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_ToolErrorPreservesCallToolResultEnvelope(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("error-tool", func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "tool failed"}},
		}, nil
	}))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "error-tool")
	result, err := fn.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	text := mustTextResult(t, result)
	var envelope mcp.CallToolResult
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("decoding result envelope %q: %v", text, err)
	}
	if !envelope.IsError || len(envelope.Content) != 1 || envelope.Content[0].(*mcp.TextContent).Text != "tool failed" {
		t.Fatalf("result envelope = %#v, want tool error content", envelope)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_FailedTaskThrowsMcpException(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{initialStatus: "failed"},
		taskTestTool("failed-tool", taskTextTool("unused")))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "failed-tool")
	_, err := fn.Call(t.Context(), `{}`)
	assertTaskErrorContains(t, err, "simulated failure")
	_, _, _, cancels, _ := server.counts()
	if cancels != 0 {
		t.Fatalf("cancellation requests = %d, want 0 for terminal failure", cancels)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_ServerCancelledTaskThrowsOperationCanceled(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("server-cancelled-tool", blockingTaskTool(nil)))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "server-cancelled-tool")
	resultCh := make(chan error, 1)
	go func() {
		_, err := fn.Call(t.Context(), `{}`)
		resultCh <- err
	}()
	waitForSignal(t, server.firstPollObserved)
	server.cancelLatestTask()
	err := <-resultCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	_, _, _, requests, transitions := server.counts()
	if requests != 0 || transitions != 1 {
		t.Fatalf("task cancellation requests = %d, transitions = %d, want 0 and 1", requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_InputHandlerFailureCancelsRemoteTask(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		initialStatus: "input_required",
		inputRequests: []mcp.InputRequest{elicitationRequest("Confirm operation.")},
	}, taskTestTool("failing-input-tool", taskTextTool("unused")))
	options := mcptool.TaskOptions{}
	options.InputHandler = failInputHandler
	options.ElicitationCapabilities = &mcp.ElicitationCapabilities{}
	fn := getTaskTool(t, server, options, "failing-input-tool")
	_, err := fn.Call(t.Context(), `{}`)
	if !errors.Is(err, errTaskInputHandler) {
		t.Fatalf("error = %v, want input handler failure", err)
	}
	waitForSignal(t, server.remoteCancelObserved)
	_, _, _, requests, transitions := server.counts()
	if requests != 1 || transitions != 1 {
		t.Fatalf("task cancellation requests = %d, transitions = %d, want 1 each", requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_GetTaskFailureCancelsRemoteTaskAndPreservesError(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		getErr: errors.New("get task failed"),
	}, taskTestTool("get-failure-tool", blockingTaskTool(nil)))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "get-failure-tool")
	_, err := fn.Call(t.Context(), `{}`)
	assertTaskErrorContains(t, err, "get task failed")
	waitForSignal(t, server.remoteCancelObserved)
	_, _, _, requests, transitions := server.counts()
	if requests != 1 || transitions != 1 {
		t.Fatalf("task cancellation requests = %d, transitions = %d, want 1 each", requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_UpdateTaskFailureCancelsRemoteTaskAndPreservesError(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		initialStatus: "input_required",
		inputRequests: []mcp.InputRequest{elicitationRequest("Confirm operation.")},
		updateErr:     errors.New("update task failed"),
	}, taskTestTool("update-failure-tool", taskTextTool("unused")))
	options := mcptool.TaskOptions{}
	options.InputHandler = acceptInputHandler
	options.ElicitationCapabilities = &mcp.ElicitationCapabilities{}
	fn := getTaskTool(t, server, options, "update-failure-tool")
	_, err := fn.Call(t.Context(), `{}`)
	assertTaskErrorContains(t, err, "update task failed")
	waitForSignal(t, server.remoteCancelObserved)
	_, _, _, requests, transitions := server.counts()
	if requests != 1 || transitions != 1 {
		t.Fatalf("task cancellation requests = %d, transitions = %d, want 1 each", requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_MalformedCompletedResultDoesNotCancelTerminalTask(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result string
		err    string
	}{
		{"string", `"not a call tool result"`, "decoding completed MCP task"},
		{"null", `null`, "completed without a result"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newTaskTestServer(t, taskTestConfig{
				initialStatus:   "completed",
				completedResult: json.RawMessage(tt.result),
			}, taskTestTool("malformed-result-tool", taskTextTool("unused")))
			fn := getTaskTool(t, server, mcptool.TaskOptions{}, "malformed-result-tool")
			_, err := fn.Call(t.Context(), `{}`)
			assertTaskErrorContains(t, err, tt.err)
			_, _, _, requests, transitions := server.counts()
			if requests != 0 || transitions != 0 {
				t.Fatalf("task cancellation requests = %d, transitions = %d, want 0", requests, transitions)
			}
		})
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_StuckInputRequiredCancelsRemoteTask(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		initialStatus:        "input_required",
		inputRequests:        []mcp.InputRequest{elicitationRequest("Confirm operation.")},
		ignoreInputResponses: true,
	}, taskTestTool("stuck-input-tool", taskTextTool("unused")))
	options := mcptool.TaskOptions{}
	options.MaxConsecutiveStuckPolls = 2
	options.InputHandler = acceptInputHandler
	options.ElicitationCapabilities = &mcp.ElicitationCapabilities{}
	fn := getTaskTool(t, server, options, "stuck-input-tool")
	_, err := fn.Call(t.Context(), `{}`)
	assertTaskErrorContains(t, err, "2 consecutive polls")
	waitForSignal(t, server.remoteCancelObserved)
	_, _, inputs, requests, transitions := server.counts()
	if inputs != 1 || requests != 1 || transitions != 1 {
		t.Fatalf("input responses = %d, cancellation requests = %d, transitions = %d", inputs, requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_InputRequestsAtLimitCompletes(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		initialStatus: "input_required",
		inputRequests: []mcp.InputRequest{
			elicitationRequest("Confirm operation 0."),
			elicitationRequest("Confirm operation 1."),
		},
	}, taskTestTool("bounded-input-tool", taskTextTool("unused")))
	var handled atomic.Int32
	options := mcptool.TaskOptions{}
	options.MaxTotalInputRequests = 2
	options.ElicitationCapabilities = &mcp.ElicitationCapabilities{}
	options.InputHandler = func(ctx context.Context, request mcp.InputRequest) (mcp.InputResponse, error) {
		handled.Add(1)
		return acceptInputHandler(ctx, request)
	}
	fn := getTaskTool(t, server, options, "bounded-input-tool")
	result, err := fn.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustTextResult(t, result); got != "completed" {
		t.Fatalf("result = %q, want completed", got)
	}
	if handled.Load() != 2 {
		t.Fatalf("handled input requests = %d, want 2", handled.Load())
	}
	_, _, _, requests, transitions := server.counts()
	if requests != 0 || transitions != 0 {
		t.Fatalf("task cancellation requests = %d, transitions = %d, want 0", requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_InputRequestLimitExceededCancelsBeforeDispatch(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{
		initialStatus: "input_required",
		inputRequests: []mcp.InputRequest{
			elicitationRequest("Confirm operation 0."),
			elicitationRequest("Confirm operation 1."),
			elicitationRequest("Confirm operation 2."),
		},
	}, taskTestTool("unbounded-input-tool", taskTextTool("unused")))
	var handled atomic.Int32
	options := mcptool.TaskOptions{}
	options.MaxTotalInputRequests = 2
	options.ElicitationCapabilities = &mcp.ElicitationCapabilities{}
	options.InputHandler = func(ctx context.Context, request mcp.InputRequest) (mcp.InputResponse, error) {
		handled.Add(1)
		return acceptInputHandler(ctx, request)
	}
	fn := getTaskTool(t, server, options, "unbounded-input-tool")
	_, err := fn.Call(t.Context(), `{}`)
	assertTaskErrorContains(t, err, "limit of 2 unique input requests")
	waitForSignal(t, server.remoteCancelObserved)
	if handled.Load() != 2 {
		t.Fatalf("handled input requests = %d, want 2", handled.Load())
	}
	_, _, inputs, requests, transitions := server.counts()
	if inputs != 2 || requests != 1 || transitions != 1 {
		t.Fatalf("input responses = %d, cancellation requests = %d, transitions = %d", inputs, requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_LocalCancellationCancelsRemoteTask(t *testing.T) {
	serverCancelled := make(chan struct{})
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("blocking-tool", blockingTaskToolWithCancel(serverCancelled)))
	fn := getTaskTool(t, server, mcptool.TaskOptions{}, "blocking-tool")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resultCh := make(chan error, 1)
	go func() {
		_, err := fn.Call(ctx, `{}`)
		resultCh <- err
	}()
	waitForSignal(t, server.firstPollObserved)
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call() error = %v, want context.Canceled", err)
	}
	waitForSignal(t, server.remoteCancelObserved)
	waitForSignal(t, serverCancelled)
	created, polls, _, requests, transitions := server.counts()
	if created != 1 || polls == 0 || requests != 1 || transitions != 1 {
		t.Fatalf("task counts = created %d, polls %d, cancellation requests %d, transitions %d", created, polls, requests, transitions)
	}
}

func TestTaskAwareMcpClientAIFunction_InvokeAsync_LocalCancellationDoesNotCancelRemoteTaskWhenDisabled(t *testing.T) {
	server := newTaskTestServer(t, taskTestConfig{}, taskTestTool("blocking-tool", blockingTaskTool(nil)))
	options := mcptool.TaskOptions{DisableCancelRemoteTaskOnLocalCancellation: true}
	fn := getTaskTool(t, server, options, "blocking-tool")
	ctx, cancel := context.WithCancel(t.Context())
	resultCh := make(chan error, 1)
	go func() {
		_, err := fn.Call(ctx, `{}`)
		resultCh <- err
	}()
	waitForSignal(t, server.firstPollObserved)
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call() error = %v, want context.Canceled", err)
	}
	_, _, _, requests, transitions := server.counts()
	if requests != 0 || transitions != 0 {
		t.Fatalf("task cancellation requests = %d, transitions = %d, want 0", requests, transitions)
	}
}

func blockingTaskTool(release <-chan struct{}) mcp.ToolHandler {
	return func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if release == nil {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		select {
		case <-release:
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "completed"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func blockingTaskToolWithCancel(cancelled chan<- struct{}) mcp.ToolHandler {
	return func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		if cancelled != nil {
			close(cancelled)
		}
		return nil, ctx.Err()
	}
}

func findFuncTool(t *testing.T, tools []tool.Tool, name string) tool.FuncTool {
	t.Helper()
	for _, current := range tools {
		if current.Name() == name {
			fn, ok := current.(tool.FuncTool)
			if !ok {
				t.Fatalf("tool %q is %T, want tool.FuncTool", name, current)
			}
			return fn
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
