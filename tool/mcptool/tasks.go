// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	taskExtensionID   = "io.modelcontextprotocol/tasks"
	taskCreateMethod  = "tools/call"
	taskGetMethod     = "tasks/get"
	taskUpdateMethod  = "tasks/update"
	taskCancelMethod  = "tasks/cancel"
	taskCallMarkerKey = "io.modelcontextprotocol/agent-framework-task-call"
	taskResultMetaKey = "io.modelcontextprotocol/agent-framework-task"
	taskProtocol      = "2026-07-28"
	defaultPollMS     = int64(1000)
)

const maxTaskDelay = time.Duration((1<<63 - 1) / int64(time.Millisecond) * int64(time.Millisecond))

type TaskInputHandler func(context.Context, mcp.InputRequest) (mcp.InputResponse, error)

// TaskOptions configures the task lifecycle for tools returned by
// [ListToolsWithTasks]. The zero value uses the default lifecycle settings.
type TaskOptions struct {
	// DisableCancelRemoteTaskOnLocalCancellation leaves the remote task running
	// when the local invocation is canceled. When false, the tool makes a
	// best-effort attempt to cancel the remote task.
	DisableCancelRemoteTaskOnLocalCancellation bool

	// MaxConsecutiveStuckPolls limits consecutive input_required polls
	// without new input requests. Zero defaults to 60.
	MaxConsecutiveStuckPolls int

	// MaxTotalInputRequests limits unique input requests per invocation.
	// Zero defaults to 100.
	MaxTotalInputRequests int

	// RemoteCancellationTimeout bounds a remote cancellation attempt.
	// Zero defaults to 5 seconds.
	RemoteCancellationTimeout time.Duration

	// MinimumPollingInterval is the shortest accepted server polling interval.
	// Zero defaults to 10 milliseconds.
	MinimumPollingInterval time.Duration

	// MaximumPollingInterval is the longest accepted server polling interval.
	// Zero uses the largest whole-millisecond [time.Duration].
	MaximumPollingInterval time.Duration

	InputHandler TaskInputHandler
}

func (o *TaskOptions) validate() error {
	if o.MaxConsecutiveStuckPolls == 0 {
		o.MaxConsecutiveStuckPolls = 60
	}
	if o.MaxTotalInputRequests == 0 {
		o.MaxTotalInputRequests = 100
	}
	if o.RemoteCancellationTimeout == 0 {
		o.RemoteCancellationTimeout = 5 * time.Second
	}
	if o.MinimumPollingInterval == 0 {
		o.MinimumPollingInterval = 10 * time.Millisecond
	}
	if o.MaximumPollingInterval == 0 {
		o.MaximumPollingInterval = maxTaskDelay
	}

	if o.MaxConsecutiveStuckPolls <= 0 {
		return fmt.Errorf("MaxConsecutiveStuckPolls must be greater than zero")
	}
	if o.MaxTotalInputRequests <= 0 {
		return fmt.Errorf("MaxTotalInputRequests must be greater than zero")
	}
	if o.RemoteCancellationTimeout < time.Millisecond || o.RemoteCancellationTimeout > maxTaskDelay {
		return fmt.Errorf("RemoteCancellationTimeout must be between 1ms and %s", maxTaskDelay)
	}
	if o.MinimumPollingInterval <= 0 {
		return fmt.Errorf("MinimumPollingInterval must be greater than zero")
	}
	if o.MaximumPollingInterval < o.MinimumPollingInterval {
		return fmt.Errorf("MaximumPollingInterval must be greater than or equal to MinimumPollingInterval")
	}
	if o.MaximumPollingInterval > maxTaskDelay {
		return fmt.Errorf("MaximumPollingInterval must not exceed %s", maxTaskDelay)
	}
	minimumMS := durationCeilMilliseconds(o.MinimumPollingInterval)
	maximumMS := int64(o.MaximumPollingInterval / time.Millisecond)
	if minimumMS > maximumMS {
		return fmt.Errorf("polling interval range must contain at least one whole millisecond value")
	}
	o.MinimumPollingInterval = time.Duration(minimumMS) * time.Millisecond
	o.MaximumPollingInterval = time.Duration(maximumMS) * time.Millisecond
	return nil
}

func durationCeilMilliseconds(d time.Duration) int64 {
	ms := int64(d / time.Millisecond)
	if d%time.Millisecond != 0 {
		ms++
	}
	return ms
}

type taskRequestParams struct {
	mcp.ParamsBase
	TaskID         string               `json:"taskId"`
	InputResponses mcp.InputResponseMap `json:"inputResponses,omitempty"`
}

type taskRequestResult struct {
	mcp.ResultBase
	TaskID         string              `json:"taskId"`
	Status         string              `json:"status"`
	StatusMessage  string              `json:"statusMessage,omitempty"`
	PollIntervalMS *int64              `json:"pollIntervalMs,omitempty"`
	InputRequests  mcp.InputRequestMap `json:"inputRequests,omitempty"`
	Result         json.RawMessage     `json:"result,omitempty"`
	Error          json.RawMessage     `json:"error,omitempty"`
}

type taskAck struct {
	mcp.ResultBase
}

type createdTask struct {
	TaskID         string `json:"taskId"`
	PollIntervalMS *int64 `json:"pollIntervalMs,omitempty"`
}

func registerTaskMethods(client *mcp.Client) error {
	if err := mcp.AddSendingCustomMethod[*taskRequestParams, *taskRequestResult](client, taskGetMethod); err != nil {
		return err
	}
	if err := mcp.AddSendingCustomMethod[*taskRequestParams, *taskAck](client, taskUpdateMethod); err != nil {
		return err
	}
	return mcp.AddSendingCustomMethod[*taskRequestParams, *taskAck](client, taskCancelMethod)
}

type taskTransport struct {
	mcp.Transport
}

func newTaskTransport(transport mcp.Transport) mcp.Transport {
	return &taskTransport{Transport: transport}
}

func (t *taskTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &taskConnection{
		Connection: connection,
		pending:    make(map[string]bool),
	}, nil
}

type taskConnection struct {
	mcp.Connection
	mu      sync.Mutex
	pending map[string]bool
}

func (c *taskConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	request, ok := message.(*jsonrpc.Request)
	if !ok || request.Method != taskCreateMethod {
		return c.Connection.Write(ctx, message)
	}

	params, err := decodeJSONObject(request.Params)
	if err != nil {
		return fmt.Errorf("decoding task-aware tools/call parameters: %w", err)
	}
	meta, err := decodeJSONObject(params["_meta"])
	if err != nil {
		return fmt.Errorf("decoding task-aware tools/call metadata: %w", err)
	}
	var taskCall bool
	if err := json.Unmarshal(meta[taskCallMarkerKey], &taskCall); err != nil && len(meta[taskCallMarkerKey]) != 0 {
		return fmt.Errorf("decoding task-aware tools/call marker: %w", err)
	}
	if !taskCall {
		return c.Connection.Write(ctx, message)
	}

	delete(meta, taskCallMarkerKey)
	var protocolVersion string
	if err := json.Unmarshal(meta[mcp.MetaKeyProtocolVersion], &protocolVersion); err != nil && len(meta[mcp.MetaKeyProtocolVersion]) != 0 {
		return fmt.Errorf("decoding task-aware tools/call protocol version: %w", err)
	}

	copyRequest := *request
	if protocolVersion >= taskProtocol {
		capabilities, err := decodeJSONObject(meta[mcp.MetaKeyClientCapabilities])
		if err != nil {
			return fmt.Errorf("decoding task-aware client capabilities: %w", err)
		}
		extensions, err := decodeJSONObject(capabilities["extensions"])
		if err != nil {
			return fmt.Errorf("decoding task-aware client extensions: %w", err)
		}
		if _, exists := extensions[taskExtensionID]; !exists {
			extensions[taskExtensionID] = json.RawMessage(`{}`)
		}
		capabilities["extensions"], err = json.Marshal(extensions)
		if err != nil {
			return fmt.Errorf("encoding task-aware client extensions: %w", err)
		}
		meta[mcp.MetaKeyClientCapabilities], err = json.Marshal(capabilities)
		if err != nil {
			return fmt.Errorf("encoding task-aware client capabilities: %w", err)
		}
		params["_meta"], err = json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("encoding task-aware tools/call metadata: %w", err)
		}
		copyRequest.Params, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encoding task-aware tools/call parameters: %w", err)
		}
		if request.ID.IsValid() {
			key, err := requestIDKey(request.ID)
			if err != nil {
				return err
			}
			c.mu.Lock()
			c.pending[key] = true
			c.mu.Unlock()
		}
	} else {
		params["_meta"], err = json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("encoding task-aware tools/call metadata: %w", err)
		}
		copyRequest.Params, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encoding task-aware tools/call parameters: %w", err)
		}
	}

	if err := c.Connection.Write(ctx, &copyRequest); err != nil {
		if request.ID.IsValid() {
			if key, keyErr := requestIDKey(request.ID); keyErr == nil {
				c.mu.Lock()
				delete(c.pending, key)
				c.mu.Unlock()
			}
		}
		return err
	}
	return nil
}

func (c *taskConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.Connection.Read(ctx)
	if err != nil {
		return nil, err
	}
	response, ok := message.(*jsonrpc.Response)
	if !ok || !response.ID.IsValid() {
		return message, nil
	}

	key, err := requestIDKey(response.ID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	taskCall := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if !taskCall || response.Error != nil {
		return message, nil
	}

	result, err := decodeJSONObject(response.Result)
	if err != nil {
		return nil, fmt.Errorf("decoding task-aware tools/call result: %w", err)
	}
	var resultType string
	if err := json.Unmarshal(result["resultType"], &resultType); err != nil && len(result["resultType"]) != 0 {
		return nil, fmt.Errorf("decoding task-aware tools/call result type: %w", err)
	}
	if resultType != "task" {
		return message, nil
	}

	var task createdTask
	if err := json.Unmarshal(response.Result, &task); err != nil {
		return nil, fmt.Errorf("decoding created MCP task: %w", err)
	}
	if task.TaskID == "" {
		return nil, fmt.Errorf("MCP task creation returned an empty taskId")
	}
	meta, err := decodeJSONObject(result["_meta"])
	if err != nil {
		return nil, fmt.Errorf("decoding created MCP task metadata: %w", err)
	}
	meta[taskResultMetaKey], err = json.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("encoding created MCP task metadata: %w", err)
	}
	result["_meta"], err = json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("encoding created MCP task metadata: %w", err)
	}
	response.Result, err = json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encoding created MCP task result: %w", err)
	}
	return response, nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return make(map[string]json.RawMessage), nil
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return make(map[string]json.RawMessage), nil
	}
	return result, nil
}

func requestIDKey(id jsonrpc.ID) (string, error) {
	raw, err := json.Marshal(id.Raw())
	if err != nil {
		return "", fmt.Errorf("encoding JSON-RPC request ID: %w", err)
	}
	return string(raw), nil
}

func taskFromMeta(meta mcp.Meta) (createdTask, bool, error) {
	raw, ok := meta[taskResultMetaKey]
	if !ok {
		return createdTask{}, false, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return createdTask{}, true, fmt.Errorf("encoding created MCP task metadata: %w", err)
	}
	var task createdTask
	if err := json.Unmarshal(data, &task); err != nil {
		return createdTask{}, true, fmt.Errorf("decoding created MCP task metadata: %w", err)
	}
	if task.TaskID == "" {
		return createdTask{}, true, fmt.Errorf("MCP task creation returned an empty taskId")
	}
	return task, true, nil
}

func pollTaskToCompletion(ctx context.Context, session *mcp.ClientSession, task createdTask, options TaskOptions) (result *mcp.CallToolResult, err error) {
	terminal := false
	pollIntervalMS := defaultPollMS
	if task.PollIntervalMS != nil {
		pollIntervalMS = *task.PollIntervalMS
	} else {
		pollIntervalMS = clampPollInterval(pollIntervalMS, options)
	}
	observedInputRequests := make(map[string]struct{})
	consecutiveStuckPolls := 0
	firstPoll := true

	defer func() {
		if err == nil || terminal {
			return
		}
		if ctx.Err() != nil && options.DisableCancelRemoteTaskOnLocalCancellation {
			return
		}
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), options.RemoteCancellationTimeout)
		defer cancel()
		if cancelErr := cancelTask(cancelCtx, session, task.TaskID); cancelErr != nil {
			slog.WarnContext(ctx, "best-effort MCP task cancellation failed", "task_id", task.TaskID, "error", cancelErr)
		}
	}()

	if task.PollIntervalMS != nil {
		if _, err := validatedPollDelay(pollIntervalMS, options); err != nil {
			return nil, err
		}
	}

	for {
		if !firstPoll {
			delay, delayErr := validatedPollDelay(pollIntervalMS, options)
			if delayErr != nil {
				return nil, delayErr
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		firstPoll = false

		taskResult, getErr := getTask(ctx, session, task.TaskID)
		if getErr != nil {
			return nil, fmt.Errorf("MCP tasks/get failed: %w", getErr)
		}
		if taskResult.PollIntervalMS != nil {
			pollIntervalMS = *taskResult.PollIntervalMS
		}

		switch taskResult.Status {
		case "working":
			consecutiveStuckPolls = 0
		case "input_required":
			newRequests := make(mcp.InputRequestMap)
			remaining := options.MaxTotalInputRequests - len(observedInputRequests)
			for id, input := range taskResult.InputRequests {
				if _, exists := observedInputRequests[id]; exists {
					continue
				}
				if len(newRequests) >= remaining {
					return nil, fmt.Errorf("MCP task %q exceeded the limit of %d unique input requests", task.TaskID, options.MaxTotalInputRequests)
				}
				newRequests[id] = input
			}
			if len(newRequests) == 0 {
				consecutiveStuckPolls++
				if consecutiveStuckPolls >= options.MaxConsecutiveStuckPolls {
					return nil, fmt.Errorf("MCP task %q remained in input_required for %d consecutive polls without new input requests", task.TaskID, options.MaxConsecutiveStuckPolls)
				}
				continue
			}
			if options.InputHandler == nil {
				return nil, fmt.Errorf("MCP task %q requires input but no task input handler is configured", task.TaskID)
			}
			inputResponses := make(mcp.InputResponseMap, len(newRequests))
			for id, input := range newRequests {
				response, handleErr := options.InputHandler(ctx, input)
				if handleErr != nil {
					return nil, handleErr
				}
				if response == nil {
					return nil, fmt.Errorf("MCP task input handler returned a nil response for request %q", id)
				}
				inputResponses[id] = response
			}
			for id := range newRequests {
				observedInputRequests[id] = struct{}{}
			}
			if updateErr := updateTask(ctx, session, task.TaskID, inputResponses); updateErr != nil {
				return nil, fmt.Errorf("MCP tasks/update failed: %w", updateErr)
			}
			consecutiveStuckPolls = 0
		case "completed":
			terminal = true
			if len(taskResult.Result) == 0 {
				return nil, fmt.Errorf("MCP task %q completed without a result", task.TaskID)
			}
			var completedResult mcp.CallToolResult
			if err := json.Unmarshal(taskResult.Result, &completedResult); err != nil {
				return nil, fmt.Errorf("decoding completed MCP task %q result: %w", task.TaskID, err)
			}
			return &completedResult, nil
		case "failed":
			terminal = true
			return nil, fmt.Errorf("MCP task %q failed: %s", task.TaskID, taskErrorMessage(taskResult.Error))
		case "cancelled":
			terminal = true
			return nil, fmt.Errorf("MCP task %q was cancelled by the server: %w", task.TaskID, context.Canceled)
		default:
			return nil, fmt.Errorf("MCP task %q returned unexpected status %q", task.TaskID, taskResult.Status)
		}
	}
}

func clampPollInterval(ms int64, options TaskOptions) int64 {
	minimum := int64(options.MinimumPollingInterval / time.Millisecond)
	maximum := int64(options.MaximumPollingInterval / time.Millisecond)
	if ms < minimum {
		return minimum
	}
	if ms > maximum {
		return maximum
	}
	return ms
}

func validatedPollDelay(ms int64, options TaskOptions) (time.Duration, error) {
	minimum := int64(options.MinimumPollingInterval / time.Millisecond)
	maximum := int64(options.MaximumPollingInterval / time.Millisecond)
	if ms < minimum || ms > maximum {
		return 0, fmt.Errorf("MCP task returned an unusable pollIntervalMs of %d; configured range is %d through %d milliseconds", ms, minimum, maximum)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func taskErrorMessage(raw json.RawMessage) string {
	var detail struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &detail); err == nil && detail.Message != "" {
		return detail.Message
	}
	if len(raw) == 0 {
		return "unknown task error"
	}
	return string(raw)
}

func getTask(ctx context.Context, session *mcp.ClientSession, taskID string) (*taskRequestResult, error) {
	result, err := mcp.CallCustomMethod[*taskRequestParams, *taskRequestResult](
		ctx, session, taskGetMethod, &taskRequestParams{TaskID: taskID},
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func updateTask(ctx context.Context, session *mcp.ClientSession, taskID string, responses mcp.InputResponseMap) error {
	_, err := mcp.CallCustomMethod[*taskRequestParams, *taskAck](
		ctx, session, taskUpdateMethod, &taskRequestParams{TaskID: taskID, InputResponses: responses},
	)
	return err
}

func cancelTask(ctx context.Context, session *mcp.ClientSession, taskID string) error {
	_, err := mcp.CallCustomMethod[*taskRequestParams, *taskAck](
		ctx, session, taskCancelMethod, &taskRequestParams{TaskID: taskID},
	)
	return err
}

func withTaskHTTPRouting(transport mcp.Transport) mcp.Transport {
	streamable, ok := transport.(*mcp.StreamableClientTransport)
	if !ok {
		return transport
	}

	client := new(http.Client)
	if streamable.HTTPClient != nil {
		*client = *streamable.HTTPClient
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = taskRoutingRoundTripper{base: base}
	return &mcp.StreamableClientTransport{
		Endpoint:             streamable.Endpoint,
		HTTPClient:           client,
		MaxRetries:           streamable.MaxRetries,
		DisableStandaloneSSE: streamable.DisableStandaloneSSE,
		OAuthHandler:         streamable.OAuthHandler,
		MaxEventSize:         streamable.MaxEventSize,
	}
}

type taskRoutingRoundTripper struct {
	base http.RoundTripper
}

func (t taskRoutingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body == nil {
		return t.base.RoundTrip(request)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("reading MCP HTTP request for task routing: %w", err)
	}
	if err := request.Body.Close(); err != nil {
		return nil, fmt.Errorf("closing MCP HTTP request body for task routing: %w", err)
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))

	var rpcRequest struct {
		Method string `json:"method"`
		Params struct {
			TaskID string `json:"taskId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &rpcRequest); err != nil {
		return nil, fmt.Errorf("decoding MCP HTTP request for task routing: %w", err)
	}
	if strings.HasPrefix(rpcRequest.Method, "tasks/") && rpcRequest.Params.TaskID != "" {
		request.Header.Set("Mcp-Name", rpcRequest.Params.TaskID)
	}
	return t.base.RoundTrip(request)
}
