// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
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

// TaskInputHandler resolves an input request from a running MCP task. It must
// support the capabilities declared in [TaskOptions] and be safe for concurrent
// calls when tools are invoked concurrently.
type TaskInputHandler func(context.Context, mcp.InputRequest) (mcp.InputResponse, error)

// TaskOptions configures the task lifecycle for tools returned by
// [ListToolsWithTasks]. The zero value uses the default lifecycle settings.
// Options and capability declarations are copied when tools are listed.
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

	// InputHandler resolves task input requests. It must be configured together
	// with ElicitationCapabilities, SamplingCapabilities, or both.
	InputHandler TaskInputHandler

	// ElicitationCapabilities declares the elicitation modes supported by
	// InputHandler. Nil declares no support; an empty value declares form
	// elicitation. The declaration is sent only on task-aware requests.
	ElicitationCapabilities *mcp.ElicitationCapabilities

	// SamplingCapabilities declares the sampling features supported by
	// InputHandler. Nil declares no support; an empty value declares basic
	// sampling without context or tools. The declaration is sent only on
	// task-aware requests.
	SamplingCapabilities *mcp.SamplingCapabilities //nolint:staticcheck // MCP sampling remains supported during the SEP-2577 deprecation window.
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
	hasInputCapabilities := o.ElicitationCapabilities != nil || o.SamplingCapabilities != nil
	if hasInputCapabilities && o.InputHandler == nil {
		return fmt.Errorf("input capabilities require an InputHandler")
	}
	if !hasInputCapabilities && o.InputHandler != nil {
		return fmt.Errorf("InputHandler requires ElicitationCapabilities or SamplingCapabilities")
	}
	if o.ElicitationCapabilities != nil {
		o.ElicitationCapabilities = new(*o.ElicitationCapabilities)
		if o.ElicitationCapabilities.Form == nil && o.ElicitationCapabilities.URL == nil {
			o.ElicitationCapabilities.Form = &mcp.FormElicitationCapabilities{}
		}
	}
	if o.SamplingCapabilities != nil {
		o.SamplingCapabilities = new(*o.SamplingCapabilities)
	}
	o.MinimumPollingInterval = time.Duration(minimumMS) * time.Millisecond
	o.MaximumPollingInterval = time.Duration(maximumMS) * time.Millisecond
	return nil
}

func (o *TaskOptions) requestMeta() mcp.Meta {
	capabilities := make(map[string]any)
	if o.ElicitationCapabilities != nil {
		capabilities["elicitation"] = o.ElicitationCapabilities
	}
	if o.SamplingCapabilities != nil {
		capabilities["sampling"] = o.SamplingCapabilities
	}
	// Merge after the SDK supplies its own per-request capabilities.
	return mcp.Meta{taskCallMarkerKey: capabilities}
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
	if streamable, ok := transport.(*mcp.StreamableClientTransport); ok {
		// The SDK's HTTP connection has private session lifecycle hooks that
		// a Connection wrapper cannot forward. Intercept HTTP bodies instead.
		return newTaskHTTPTransport(streamable)
	}
	return &taskTransport{Transport: transport}
}

func (t *taskTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &taskConnection{
		Connection: connection,
		pending:    make(map[jsonrpc.ID]bool),
	}, nil
}

type taskConnection struct {
	mcp.Connection
	mu      sync.Mutex
	pending map[jsonrpc.ID]bool
}

func (c *taskConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	message, taskCall, err := prepareTaskRequest(message)
	if err != nil {
		return err
	}
	var id jsonrpc.ID
	if taskCall {
		id = message.(*jsonrpc.Request).ID
		c.mu.Lock()
		c.pending[id] = true
		c.mu.Unlock()
	}
	if err := c.Connection.Write(ctx, message); err != nil {
		if taskCall {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
		}
		return err
	}
	return nil
}

func prepareTaskRequest(message jsonrpc.Message) (jsonrpc.Message, bool, error) {
	request, ok := message.(*jsonrpc.Request)
	if !ok {
		return message, false, nil
	}
	switch request.Method {
	case taskCreateMethod, taskGetMethod, taskUpdateMethod, taskCancelMethod:
	default:
		return message, false, nil
	}

	params, err := decodeJSONObject(request.Params)
	if err != nil {
		return nil, false, fmt.Errorf("decoding task-aware %s parameters: %w", request.Method, err)
	}
	meta, err := decodeJSONObject(params["_meta"])
	if err != nil {
		return nil, false, fmt.Errorf("decoding task-aware %s metadata: %w", request.Method, err)
	}
	inputCapabilities, marked := meta[taskCallMarkerKey]
	if request.Method == taskCreateMethod && !marked {
		return message, false, nil
	}

	delete(meta, taskCallMarkerKey)
	var protocolVersion string
	if err := json.Unmarshal(meta[mcp.MetaKeyProtocolVersion], &protocolVersion); err != nil && len(meta[mcp.MetaKeyProtocolVersion]) != 0 {
		return nil, false, fmt.Errorf("decoding task-aware %s protocol version: %w", request.Method, err)
	}

	if protocolVersion >= taskProtocol {
		capabilities, err := decodeJSONObject(meta[mcp.MetaKeyClientCapabilities])
		if err != nil {
			return nil, false, fmt.Errorf("decoding task-aware client capabilities: %w", err)
		}
		if marked {
			inputs, err := decodeJSONObject(inputCapabilities)
			if err != nil {
				return nil, false, fmt.Errorf("decoding task-aware input capabilities: %w", err)
			}
			for _, name := range []string{"elicitation", "sampling"} {
				delete(capabilities, name)
				if value, ok := inputs[name]; ok {
					capabilities[name] = value
				}
			}
		}
		extensions, err := decodeJSONObject(capabilities["extensions"])
		if err != nil {
			return nil, false, fmt.Errorf("decoding task-aware client extensions: %w", err)
		}
		if _, exists := extensions[taskExtensionID]; !exists {
			extensions[taskExtensionID] = json.RawMessage(`{}`)
		}
		capabilities["extensions"], err = json.Marshal(extensions)
		if err != nil {
			return nil, false, fmt.Errorf("encoding task-aware client extensions: %w", err)
		}
		meta[mcp.MetaKeyClientCapabilities], err = json.Marshal(capabilities)
		if err != nil {
			return nil, false, fmt.Errorf("encoding task-aware client capabilities: %w", err)
		}
	}
	params["_meta"], err = json.Marshal(meta)
	if err != nil {
		return nil, false, fmt.Errorf("encoding task-aware %s metadata: %w", request.Method, err)
	}
	copyRequest := *request
	copyRequest.Params, err = json.Marshal(params)
	if err != nil {
		return nil, false, fmt.Errorf("encoding task-aware %s parameters: %w", request.Method, err)
	}
	taskCall := protocolVersion >= taskProtocol && request.Method == taskCreateMethod && request.ID.IsValid()
	return &copyRequest, taskCall, nil
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

	c.mu.Lock()
	taskCall := c.pending[response.ID]
	delete(c.pending, response.ID)
	c.mu.Unlock()
	if !taskCall || response.Error != nil {
		return message, nil
	}
	return adaptTaskResponse(response)
}

func adaptTaskResponse(response *jsonrpc.Response) (jsonrpc.Message, error) {
	result, err := decodeJSONObject(response.Result)
	if err != nil {
		return nil, fmt.Errorf("decoding task-aware tools/call result: %w", err)
	}
	var resultType string
	if err := json.Unmarshal(result["resultType"], &resultType); err != nil && len(result["resultType"]) != 0 {
		return nil, fmt.Errorf("decoding task-aware tools/call result type: %w", err)
	}
	if resultType != "task" {
		return response, nil
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
	copyResponse := *response
	copyResponse.Result, err = json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encoding created MCP task result: %w", err)
	}
	return &copyResponse, nil
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
		if cancelErr := cancelTask(cancelCtx, session, task.TaskID, options.requestMeta()); cancelErr != nil {
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

		taskResult, getErr := getTask(ctx, session, task.TaskID, options.requestMeta())
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
			if updateErr := updateTask(ctx, session, task.TaskID, inputResponses, options.requestMeta()); updateErr != nil {
				return nil, fmt.Errorf("MCP tasks/update failed: %w", updateErr)
			}
			consecutiveStuckPolls = 0
		case "completed":
			terminal = true
			if len(taskResult.Result) == 0 || bytes.Equal(bytes.TrimSpace(taskResult.Result), []byte("null")) {
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

func getTask(ctx context.Context, session *mcp.ClientSession, taskID string, meta mcp.Meta) (*taskRequestResult, error) {
	result, err := mcp.CallCustomMethod[*taskRequestParams, *taskRequestResult](
		ctx, session, taskGetMethod, &taskRequestParams{
			ParamsBase: mcp.ParamsBase{Meta: meta},
			TaskID:     taskID,
		},
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func updateTask(ctx context.Context, session *mcp.ClientSession, taskID string, responses mcp.InputResponseMap, meta mcp.Meta) error {
	_, err := mcp.CallCustomMethod[*taskRequestParams, *taskAck](
		ctx, session, taskUpdateMethod, &taskRequestParams{
			ParamsBase:     mcp.ParamsBase{Meta: meta},
			TaskID:         taskID,
			InputResponses: responses,
		},
	)
	return err
}

func cancelTask(ctx context.Context, session *mcp.ClientSession, taskID string, meta mcp.Meta) error {
	_, err := mcp.CallCustomMethod[*taskRequestParams, *taskAck](
		ctx, session, taskCancelMethod, &taskRequestParams{
			ParamsBase: mcp.ParamsBase{Meta: meta},
			TaskID:     taskID,
		},
	)
	return err
}

func newTaskHTTPTransport(streamable *mcp.StreamableClientTransport) *mcp.StreamableClientTransport {
	client := *http.DefaultClient
	if streamable.HTTPClient != nil {
		client = *streamable.HTTPClient
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	maxEventSize := streamable.MaxEventSize
	if maxEventSize == 0 {
		maxEventSize = mcp.DefaultMaxEventSize
	}
	client.Transport = taskHTTPRoundTripper{base: base, maxEventSize: maxEventSize}
	copyTransport := *streamable
	copyTransport.HTTPClient = &client
	return &copyTransport
}

type taskHTTPRoundTripper struct {
	base         http.RoundTripper
	maxEventSize int
}

func (t taskHTTPRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body == nil {
		return t.base.RoundTrip(request)
	}
	body, err := io.ReadAll(request.Body)
	closeErr := request.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading MCP HTTP request for task routing: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing MCP HTTP request body for task routing: %w", closeErr)
	}
	message, err := jsonrpc.DecodeMessage(body)
	if err != nil {
		return nil, fmt.Errorf("decoding MCP HTTP request for task routing: %w", err)
	}
	prepared, taskCall, err := prepareTaskRequest(message)
	if err != nil {
		return nil, err
	}
	if prepared != message {
		body, err = jsonrpc.EncodeMessage(prepared)
		if err != nil {
			return nil, fmt.Errorf("encoding task-aware MCP HTTP request: %w", err)
		}
	}
	copyRequest := request.Clone(request.Context())
	copyRequest.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	copyRequest.Body = io.NopCloser(bytes.NewReader(body))
	copyRequest.ContentLength = int64(len(body))
	if rpcRequest, ok := prepared.(*jsonrpc.Request); ok && strings.HasPrefix(rpcRequest.Method, "tasks/") {
		var params taskRequestParams
		if err := json.Unmarshal(rpcRequest.Params, &params); err != nil {
			return nil, fmt.Errorf("decoding MCP task routing parameters: %w", err)
		}
		if params.TaskID != "" {
			copyRequest.Header.Set("Mcp-Name", params.TaskID)
		}
	}
	response, err := t.base.RoundTrip(copyRequest)
	if err != nil || !taskCall || response.StatusCode != http.StatusOK {
		return response, err
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaType != "application/json" && mediaType != "text/event-stream" {
		return response, nil
	}
	response.Body = &taskHTTPResponseBody{
		ReadCloser:   response.Body,
		reader:       bufio.NewReader(response.Body),
		id:           prepared.(*jsonrpc.Request).ID,
		sse:          mediaType == "text/event-stream",
		maxEventSize: t.maxEventSize,
	}
	response.ContentLength = -1
	response.Header = response.Header.Clone()
	response.Header.Del("Content-Length")
	return response, nil
}

type taskHTTPResponseBody struct {
	io.ReadCloser
	reader       *bufio.Reader
	id           jsonrpc.ID
	sse          bool
	maxEventSize int
	pending      []byte
	done         bool
}

func (b *taskHTTPResponseBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(b.pending) == 0 {
		if b.done {
			return 0, io.EOF
		}
		var err error
		if b.sse {
			var event []byte
			event, err = readTaskEvent(b.reader, b.maxEventSize)
			if err == nil {
				b.pending, err = adaptTaskEvent(event, b.id)
			}
		} else {
			b.done = true
			var body []byte
			body, err = io.ReadAll(b.reader)
			if err == nil {
				b.pending, err = adaptTaskHTTPMessage(body, b.id)
			}
		}
		if err != nil {
			b.done = true
			return 0, err
		}
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func adaptTaskHTTPMessage(data []byte, id jsonrpc.ID) ([]byte, error) {
	message, err := jsonrpc.DecodeMessage(data)
	if err != nil {
		return nil, fmt.Errorf("decoding MCP task HTTP response: %w", err)
	}
	response, ok := message.(*jsonrpc.Response)
	if !ok || response.ID != id || response.Error != nil {
		return data, nil
	}
	adapted, err := adaptTaskResponse(response)
	if err != nil {
		return nil, err
	}
	if adapted == message {
		return data, nil
	}
	return jsonrpc.EncodeMessage(adapted)
}

func readTaskEvent(reader *bufio.Reader, limit int) ([]byte, error) {
	var event []byte
	lineStart := 0
	for {
		part, err := reader.ReadSlice('\n')
		if limit > 0 && len(part) > limit-len(event) {
			return nil, fmt.Errorf("MCP task SSE event exceeds %d bytes", limit)
		}
		event = append(event, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(event) == 0 && errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if len(bytes.TrimRight(event[lineStart:], "\r\n")) == 0 || errors.Is(err, io.EOF) {
			return event, nil
		}
		lineStart = len(event)
	}
}

func adaptTaskEvent(event []byte, id jsonrpc.ID) ([]byte, error) {
	var fields []byte
	var data [][]byte
	var name string
	for _, line := range bytes.SplitAfter(event, []byte("\n")) {
		text := bytes.TrimRight(line, "\r\n")
		if len(text) == 0 {
			continue
		}
		field, value, _ := bytes.Cut(text, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "data":
			data = append(data, value)
			continue
		case "event":
			name = string(value)
		}
		fields = append(fields, line...)
	}
	payload := bytes.Join(data, []byte("\n"))
	if len(payload) == 0 || (name != "" && name != "message") {
		return event, nil
	}
	adapted, err := adaptTaskHTTPMessage(payload, id)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(adapted, payload) {
		return event, nil
	}
	if len(fields) > 0 && fields[len(fields)-1] != '\n' {
		fields = append(fields, '\n')
	}
	fields = append(fields, "data: "...)
	fields = append(fields, adapted...)
	return append(fields, '\n', '\n'), nil
}
