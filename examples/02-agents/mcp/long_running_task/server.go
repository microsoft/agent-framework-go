// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file contains a deliberately small, in-memory implementation of the
// server side of the MCP Tasks extension
// (https://modelcontextprotocol.io/extensions/tasks/overview). The Go MCP SDK
// does not provide a task store yet, so the server:
//
//   - turns tools/call requests into tasks when the client advertises the
//     io.modelcontextprotocol/tasks extension, running the tool in the background;
//   - answers tasks/get with the task status and, once finished, its result;
//   - answers tasks/cancel by canceling the running tool.
//
// Clients without the extension still receive inline tools/call results.
// Tasks are kept in memory for the lifetime of the process, and input_required
// (tasks/update) is not supported.

const (
	tasksExtension = "io.modelcontextprotocol/tasks"
	pollIntervalMS = int64(1000)
)

type datasetInput struct {
	DatasetName string `json:"datasetName" jsonschema:"The dataset identifier, e.g. 'sales-2025-q1'."`
}

// newDatasetServer returns an MCP server exposing a task-capable AnalyzeDataset
// tool that takes analysisDuration to complete.
func newDatasetServer(analysisDuration time.Duration) (*mcp.Server, *taskStore) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "DatasetAnalyzer", Version: "1.0.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "AnalyzeDataset",
		Description: "Analyze a tabular dataset and return summary statistics. This tool simulates a long-running analytic job.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in datasetInput) (*mcp.CallToolResult, any, error) {
		select {
		case <-time.After(analysisDuration):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		text := fmt.Sprintf("Findings for '%s': 12,403 rows; avg revenue $48,712; 3 anomalies detected in week 7; outliers concentrated in EMEA region.", in.DatasetName)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	})

	store := &taskStore{tasks: make(map[string]*task)}
	srv.AddReceivingMiddleware(store.middleware)
	mustAddMethod(mcp.AddReceivingCustomMethod(srv, "tasks/get", store.get))
	mustAddMethod(mcp.AddReceivingCustomMethod(srv, "tasks/cancel", store.cancel))
	return srv, store
}

func mustAddMethod(err error) {
	if err != nil {
		panic(err)
	}
}

type taskParams struct {
	mcp.ParamsBase
	TaskID string `json:"taskId"`
}

type createTaskResult struct {
	mcp.ResultBase
	ResultType     string `json:"resultType"`
	TaskID         string `json:"taskId"`
	Status         string `json:"status"`
	PollIntervalMS int64  `json:"pollIntervalMs"`
}

type getTaskResult struct {
	mcp.ResultBase
	ResultType     string          `json:"resultType"`
	TaskID         string          `json:"taskId"`
	Status         string          `json:"status"`
	PollIntervalMS int64           `json:"pollIntervalMs"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          json.RawMessage `json:"error,omitempty"`
}

type cancelTaskResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
}

type task struct {
	status string // working, completed, failed, or cancelled
	result json.RawMessage
	err    json.RawMessage
	cancel context.CancelFunc
}

type taskStore struct {
	mu     sync.Mutex
	nextID int
	tasks  map[string]*task
}

func (s *taskStore) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if method != "tools/call" || !ok || !wantsTask(call) {
			return next(ctx, method, req)
		}
		// The task outlives the tools/call request, so detach it from the
		// request context and cancel it only through tasks/cancel.
		taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		id := s.create(cancel)
		go func() {
			result, err := next(taskCtx, method, req)
			s.finish(id, result, err)
		}()
		return &createTaskResult{ResultType: "task", TaskID: id, Status: "working", PollIntervalMS: pollIntervalMS}, nil
	}
}

func wantsTask(call *mcp.CallToolRequest) bool {
	capabilities, _ := call.Params.Meta[mcp.MetaKeyClientCapabilities].(map[string]any)
	extensions, _ := capabilities["extensions"].(map[string]any)
	_, ok := extensions[tasksExtension]
	return ok
}

func (s *taskStore) create(cancel context.CancelFunc) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := fmt.Sprintf("task-%d", s.nextID)
	s.tasks[id] = &task{status: "working", cancel: cancel}
	return id
}

func (s *taskStore) finish(id string, result mcp.Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	t.cancel()
	if t.status != "working" {
		return
	}
	if err == nil {
		t.result, err = json.Marshal(result)
	}
	if err != nil {
		t.status = "failed"
		t.err, _ = json.Marshal(map[string]string{"message": err.Error()})
		return
	}
	t.status = "completed"
}

func (s *taskStore) get(_ context.Context, _ *mcp.ServerSession, params *taskParams) (*getTaskResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[params.TaskID]
	if t == nil {
		return nil, fmt.Errorf("unknown task %q", params.TaskID)
	}
	return &getTaskResult{
		ResultType:     "complete",
		TaskID:         params.TaskID,
		Status:         t.status,
		PollIntervalMS: pollIntervalMS,
		Result:         t.result,
		Error:          t.err,
	}, nil
}

func (s *taskStore) cancel(_ context.Context, _ *mcp.ServerSession, params *taskParams) (*cancelTaskResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[params.TaskID]
	if t == nil {
		return nil, fmt.Errorf("unknown task %q", params.TaskID)
	}
	if t.status == "working" {
		t.status = "cancelled"
		t.cancel()
	}
	return &cancelTaskResult{ResultType: "complete"}, nil
}
