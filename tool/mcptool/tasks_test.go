// Copyright (c) Microsoft. All rights reserved.

package mcptool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	taskGetMethod    = "tasks/get"
	taskUpdateMethod = "tasks/update"
	taskCancelMethod = "tasks/cancel"
)

type taskTestParams struct {
	mcp.ParamsBase
	TaskID         string               `json:"taskId"`
	InputResponses mcp.InputResponseMap `json:"inputResponses,omitempty"`
}

type taskTestTaskResult struct {
	mcp.ResultBase
	ResultType     string              `json:"resultType"`
	TaskID         string              `json:"taskId"`
	Status         string              `json:"status"`
	PollIntervalMS *int64              `json:"pollIntervalMs,omitempty"`
	InputRequests  mcp.InputRequestMap `json:"inputRequests,omitempty"`
	Result         json.RawMessage     `json:"result,omitempty"`
	Error          json.RawMessage     `json:"error,omitempty"`
}

type taskTestAck struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
}

type taskTestCreated struct {
	mcp.ResultBase
	ResultType     string `json:"resultType"`
	TaskID         string `json:"taskId"`
	Status         string `json:"status"`
	PollIntervalMS *int64 `json:"pollIntervalMs,omitempty"`
}

type taskTestConfig struct {
	disableTasks          bool
	initialPollIntervalMS *int64
	updatedPollIntervalMS *int64
	omitPollIntervals     bool
	getErr                error
	updateErr             error
	initialStatus         string
	inputRequests         []mcp.InputRequest
	ignoreInputResponses  bool
	completedResult       json.RawMessage
	failedResult          json.RawMessage
}

type taskTestState struct {
	id         string
	status     string
	pollCount  int
	inputIndex int
	result     json.RawMessage
	error      json.RawMessage
	cancel     context.CancelFunc
	config     taskTestConfig
}

type taskTestServer struct {
	client               *mcp.ClientSession
	serverSession        *mcp.ServerSession
	server               *mcp.Server
	config               taskTestConfig
	mu                   sync.Mutex
	tasks                map[string]*taskTestState
	createdTaskCount     int
	pollCount            int
	inputRequestCount    int
	updateCount          int
	cancelRequestCount   int
	successCancelCount   int
	firstPollObserved    chan struct{}
	secondPollObserved   chan struct{}
	remoteCancelObserved chan struct{}
	firstPollOnce        sync.Once
	secondPollOnce       sync.Once
	cancelPollOnce       sync.Once
	toolHandlers         map[string]mcp.ToolHandler
}

func newTaskTestServer(t *testing.T, config taskTestConfig, handlers map[string]mcp.ToolHandler) *taskTestServer {
	t.Helper()
	ctx := context.Background()
	state := &taskTestServer{
		config:               config,
		tasks:                make(map[string]*taskTestState),
		firstPollObserved:    make(chan struct{}),
		secondPollObserved:   make(chan struct{}),
		remoteCancelObserved: make(chan struct{}),
		toolHandlers:         handlers,
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "task-test-server", Version: "1.0.0"}, nil)
	state.server = server
	for name, handler := range handlers {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, handler)
	}
	server.AddReceivingMiddleware(state.taskMiddleware)
	if err := mcp.AddReceivingCustomMethod[*taskTestParams, *taskTestTaskResult](
		server, taskGetMethod, state.getTask,
	); err != nil {
		t.Fatal(err)
	}
	if err := mcp.AddReceivingCustomMethod[*taskTestParams, *taskTestAck](
		server, taskUpdateMethod, state.updateTask,
	); err != nil {
		t.Fatal(err)
	}
	if err := mcp.AddReceivingCustomMethod[*taskTestParams, *taskTestAck](
		server, taskCancelMethod, state.cancelTask,
	); err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := mcptool.Connect(ctx, clientTransport)
	if err != nil {
		t.Fatal(err)
	}
	state.client = client
	state.serverSession = serverSession
	t.Cleanup(func() {
		state.mu.Lock()
		for _, task := range state.tasks {
			if task.cancel != nil {
				task.cancel()
			}
		}
		state.mu.Unlock()
		if err := client.Close(); err != nil {
			t.Errorf("closing MCP task test client: %v", err)
		}
		if err := serverSession.Close(); err != nil {
			t.Errorf("closing MCP task test server session: %v", err)
		}
	})
	return state
}

func (s *taskTestServer) taskMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		if method != "tools/call" || s.config.disableTasks {
			return next(ctx, method, request)
		}
		callRequest, ok := request.(*mcp.CallToolRequest)
		if !ok {
			return nil, fmt.Errorf("tools/call request is %T", request)
		}
		capabilities, _ := callRequest.Params.Meta[mcp.MetaKeyClientCapabilities].(map[string]any)
		extensions, _ := capabilities["extensions"].(map[string]any)
		if _, enabled := extensions["io.modelcontextprotocol/tasks"]; !enabled {
			return next(ctx, method, request)
		}
		toolName := callRequest.Params.Name
		if _, ok := s.toolHandlers[toolName]; !ok {
			return nil, fmt.Errorf("unknown test tool %q", toolName)
		}

		taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		task := s.createTask(cancel)
		if len(s.config.inputRequests) == 0 && s.config.initialStatus == "" {
			go func() {
				result, err := next(taskCtx, method, request)
				s.completeTask(task.id, result, err)
			}()
		}
		return &taskTestCreated{
			ResultType:     "task",
			TaskID:         task.id,
			Status:         "working",
			PollIntervalMS: s.initialPollInterval(),
		}, nil
	}
}

func (s *taskTestServer) createTask(cancel context.CancelFunc) *taskTestState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createdTaskCount++
	task := &taskTestState{
		id:     fmt.Sprintf("task-%d", s.createdTaskCount),
		status: s.config.initialStatus,
		cancel: cancel,
		config: s.config,
	}
	if task.status == "" {
		task.status = "working"
	}
	if task.status == "input_required" {
		task.inputIndex = 0
	}
	if task.status == "completed" {
		task.result = s.completedResult()
	}
	if task.status == "failed" {
		task.error = s.config.failedResult
		if len(task.error) == 0 {
			task.error, _ = json.Marshal(map[string]any{"message": "simulated failure"})
		}
	}
	s.tasks[task.id] = task
	return task
}

func (s *taskTestServer) completeTask(taskID string, result mcp.Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil || task.status == "cancelled" {
		return
	}
	if err != nil {
		task.status = "failed"
		task.error, _ = json.Marshal(map[string]any{"message": err.Error()})
		return
	}
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		task.status = "failed"
		task.error, _ = json.Marshal(map[string]any{"message": fmt.Sprintf("unexpected tool result %T", result)})
		return
	}
	task.status = "completed"
	task.result, _ = json.Marshal(callResult)
}

func (s *taskTestServer) initialPollInterval() *int64 {
	if s.config.omitPollIntervals {
		return nil
	}
	if s.config.initialPollIntervalMS != nil {
		return s.config.initialPollIntervalMS
	}
	interval := int64(10)
	return &interval
}

func (s *taskTestServer) updatedPollInterval() *int64 {
	if s.config.omitPollIntervals {
		return nil
	}
	if s.config.updatedPollIntervalMS != nil {
		return s.config.updatedPollIntervalMS
	}
	return s.initialPollInterval()
}

func (s *taskTestServer) completedResult() json.RawMessage {
	if len(s.config.completedResult) != 0 {
		return s.config.completedResult
	}
	raw, _ := json.Marshal(&mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "completed"}},
	})
	return raw
}

func (s *taskTestServer) getTask(_ context.Context, _ *mcp.ServerSession, params *taskTestParams) (*taskTestTaskResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.getErr != nil {
		return nil, s.config.getErr
	}
	task := s.tasks[params.TaskID]
	if task == nil {
		return nil, fmt.Errorf("unknown task %q", params.TaskID)
	}
	task.pollCount++
	s.pollCount++
	s.firstPollOnce.Do(func() { close(s.firstPollObserved) })
	if s.pollCount > 1 {
		s.secondPollOnce.Do(func() { close(s.secondPollObserved) })
	}
	result := &taskTestTaskResult{
		ResultType: "complete",
		TaskID:     task.id,
		Status:     task.status,
	}
	if !task.config.omitPollIntervals {
		result.PollIntervalMS = s.updatedPollInterval()
	}
	if task.status == "input_required" && len(task.config.inputRequests) > 0 {
		index := task.inputIndex
		if index >= len(task.config.inputRequests) {
			index = len(task.config.inputRequests) - 1
		}
		result.InputRequests = mcp.InputRequestMap{
			fmt.Sprintf("input-%d", index): task.config.inputRequests[index],
		}
	}
	if task.status == "completed" {
		result.Result = append(json.RawMessage(nil), task.result...)
	}
	if task.status == "failed" {
		result.Error = append(json.RawMessage(nil), task.error...)
	}
	return result, nil
}

func (s *taskTestServer) updateTask(_ context.Context, _ *mcp.ServerSession, params *taskTestParams) (*taskTestAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.updateErr != nil {
		return nil, s.config.updateErr
	}
	task := s.tasks[params.TaskID]
	if task == nil {
		return nil, fmt.Errorf("unknown task %q", params.TaskID)
	}
	s.updateCount++
	s.inputRequestCount += len(params.InputResponses)
	if !task.config.ignoreInputResponses {
		task.inputIndex += len(params.InputResponses)
		if task.inputIndex >= len(task.config.inputRequests) {
			task.status = "completed"
			task.result = s.completedResult()
			if task.cancel != nil {
				task.cancel()
				task.cancel = nil
			}
		}
	}
	return &taskTestAck{ResultType: "complete"}, nil
}

func (s *taskTestServer) cancelTask(_ context.Context, _ *mcp.ServerSession, params *taskTestParams) (*taskTestAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[params.TaskID]
	if task == nil {
		return nil, fmt.Errorf("unknown task %q", params.TaskID)
	}
	s.cancelRequestCount++
	if task.status != "completed" && task.status != "failed" && task.status != "cancelled" {
		task.status = "cancelled"
		s.successCancelCount++
		if task.cancel != nil {
			task.cancel()
			task.cancel = nil
		}
		s.cancelPollOnce.Do(func() { close(s.remoteCancelObserved) })
	}
	return &taskTestAck{ResultType: "complete"}, nil
}

func (s *taskTestServer) cancelLatestTask() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := s.createdTaskCount; i > 0; i-- {
		task := s.tasks[fmt.Sprintf("task-%d", i)]
		if task != nil {
			task.status = "cancelled"
			if task.cancel != nil {
				task.cancel()
				task.cancel = nil
			}
			s.successCancelCount++
			return
		}
	}
}

func (s *taskTestServer) counts() (created, polls, inputs, cancels, cancelTransitions int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createdTaskCount, s.pollCount, s.inputRequestCount, s.cancelRequestCount, s.successCancelCount
}

func taskTestTool(name string, handler mcp.ToolHandler) map[string]mcp.ToolHandler {
	return map[string]mcp.ToolHandler{name: handler}
}

func getTaskTool(t *testing.T, server *taskTestServer, options mcptool.TaskOptions, name string) tool.FuncTool {
	t.Helper()
	tools, err := mcptool.ListToolsWithTasks(t.Context(), server.client, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range tools {
		if current.Name() != name {
			continue
		}
		fn, ok := current.(tool.FuncTool)
		if !ok {
			t.Fatalf("tool %q is %T, want tool.FuncTool", name, current)
		}
		return fn
	}
	t.Fatalf("task-aware tool %q not found", name)
	return nil
}

func mustTextResult(t *testing.T, result any) string {
	t.Helper()
	contents, ok := result.(message.Contents)
	if !ok {
		t.Fatalf("task tool result is %T, want message.Contents", result)
	}
	if len(contents) != 1 {
		t.Fatalf("task tool returned %d content items, want 1", len(contents))
	}
	text, ok := contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("task tool content is %T, want *mcp.TextContent", contents[0])
	}
	return text.Text
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-t.Context().Done():
		t.Fatal("timed out waiting for MCP task fixture event")
	}
}

func elicitationRequest(message string) mcp.InputRequest {
	return &mcp.ElicitParams{
		Message: message,
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"confirmed": {Type: "string"},
			},
		},
	}
}

func acceptInputHandler(_ context.Context, request mcp.InputRequest) (mcp.InputResponse, error) {
	if _, ok := request.(*mcp.ElicitParams); !ok {
		return nil, fmt.Errorf("input request is %T, want *mcp.ElicitParams", request)
	}
	return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": "yes"}}, nil
}

var errTaskInputHandler = errors.New("input handler failed")

func failInputHandler(context.Context, mcp.InputRequest) (mcp.InputResponse, error) {
	return nil, errTaskInputHandler
}

func taskTextTool(text string) mcp.ToolHandler {
	return func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

func taskInlineResult(text string) json.RawMessage {
	result, _ := json.Marshal(&mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	})
	return result
}

func assertTaskErrorContains(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %v, want it to contain %q", err, text)
	}
}
