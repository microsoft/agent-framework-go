// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTaskOptions_DefaultsAreSane(t *testing.T) {
	var options TaskOptions
	if err := options.validate(); err != nil {
		t.Fatal(err)
	}
	if options.DisableCancelRemoteTaskOnLocalCancellation {
		t.Fatal("DisableCancelRemoteTaskOnLocalCancellation = true, want false")
	}
	if options.MaxConsecutiveStuckPolls != 60 {
		t.Fatalf("MaxConsecutiveStuckPolls = %d, want 60", options.MaxConsecutiveStuckPolls)
	}
	if options.MaxTotalInputRequests != 100 {
		t.Fatalf("MaxTotalInputRequests = %d, want 100", options.MaxTotalInputRequests)
	}
	if options.RemoteCancellationTimeout != 5*time.Second {
		t.Fatalf("RemoteCancellationTimeout = %s, want 5s", options.RemoteCancellationTimeout)
	}
	if options.MinimumPollingInterval != 10*time.Millisecond {
		t.Fatalf("MinimumPollingInterval = %s, want 10ms", options.MinimumPollingInterval)
	}
	maximum := time.Duration((1<<63 - 1) / int64(time.Millisecond) * int64(time.Millisecond))
	if options.MaximumPollingInterval != maximum {
		t.Fatalf("MaximumPollingInterval = %s, want %s", options.MaximumPollingInterval, maximum)
	}
}

func TestTaskOptions_ExplicitValuesArePreserved(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(disabled), func(t *testing.T) {
			options := TaskOptions{
				DisableCancelRemoteTaskOnLocalCancellation: disabled,
				MaxConsecutiveStuckPolls:                   2,
				MaxTotalInputRequests:                      3,
				RemoteCancellationTimeout:                  time.Second,
				MinimumPollingInterval:                     time.Millisecond,
				MaximumPollingInterval:                     100 * time.Millisecond,
			}
			want := options
			if err := options.validate(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(options, want) {
				t.Fatalf("options = %+v, want %+v", options, want)
			}
		})
	}
}

func TestTaskHTTPTransportPreservesClientSettings(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	redirectErr := errors.New("redirect disallowed")
	client := &http.Client{
		Transport: http.DefaultTransport,
		Jar:       jar,
		Timeout:   time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return redirectErr
		},
	}
	originalDefault := http.DefaultClient
	http.DefaultClient = client
	t.Cleanup(func() { http.DefaultClient = originalDefault })

	for _, explicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicit), func(t *testing.T) {
			transport := &mcp.StreamableClientTransport{}
			if explicit {
				transport.HTTPClient = client
			}
			wrapped := newTaskTransport(transport).(*mcp.StreamableClientTransport)
			got := wrapped.HTTPClient
			if got == client {
				t.Fatal("HTTP client was not copied")
			}
			if got.Jar != jar || got.Timeout != client.Timeout {
				t.Fatalf("HTTP client settings = %+v, want jar and timeout preserved", got)
			}
			if got.CheckRedirect == nil || !errors.Is(got.CheckRedirect(nil, nil), redirectErr) {
				t.Fatal("HTTP redirect policy was not preserved")
			}
			if client.Transport != http.DefaultTransport {
				t.Fatal("original HTTP client was modified")
			}
		})
	}
}

func TestTaskHTTPResponseBodySSE(t *testing.T) {
	id, err := jsonrpc.MakeID(float64(1))
	if err != nil {
		t.Fatal(err)
	}
	const prefix = ": heartbeat\r\n\r\nevent: prime\nid: resume\nretry: 25\n\n"
	const message = "event: message\r\nid: reply\r\nretry: 50\r\n" +
		"data: {\"jsonrpc\":\"2.0\",\r\n" +
		"data: \"id\":1,\"result\":{\"resultType\":\"task\",\"taskId\":\"task-1\",\"pollIntervalMs\":10}}\r\n\r\n"
	source := io.NopCloser(iotest.OneByteReader(strings.NewReader(prefix + message)))
	body := &taskHTTPResponseBody{
		ReadCloser:   source,
		reader:       bufio.NewReader(source),
		id:           id,
		sse:          true,
		maxEventSize: mcp.DefaultMaxEventSize,
	}
	t.Cleanup(func() {
		if err := body.Close(); err != nil {
			t.Error(err)
		}
	})
	output, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(output), prefix) || !strings.Contains(string(output), "id: reply\r\nretry: 50\r\n") {
		t.Fatalf("SSE control fields were not preserved: %s", output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		response, err := jsonrpc.DecodeMessage([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		var result mcp.CallToolResult
		if err := result.UnmarshalJSON(response.(*jsonrpc.Response).Result); err != nil {
			t.Fatal(err)
		}
		task, found, err := taskFromMeta(result.Meta)
		if err != nil {
			t.Fatal(err)
		}
		if !found || task.TaskID != "task-1" || task.PollIntervalMS == nil || *task.PollIntervalMS != 10 {
			t.Fatalf("task metadata = %+v, found = %t", task, found)
		}
		return
	}
	t.Fatalf("no response event: %s", output)
}

func TestTaskSSEEventSizeLimit(t *testing.T) {
	event := "data: " + strings.Repeat("x", 8192) + "\n\n"
	for _, limit := range []int{len(event), len(event) - 1, -1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			got, err := readTaskEvent(bufio.NewReader(strings.NewReader(event)), limit)
			if limit == len(event)-1 {
				if err == nil || !strings.Contains(err.Error(), "exceeds") {
					t.Fatalf("error = %v, want event size limit error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != event {
				t.Fatal("SSE event was changed or truncated")
			}
		})
	}
}

func TestTaskHTTPResponseBodyCloseUnblocksRead(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	body := &taskHTTPResponseBody{ReadCloser: reader, reader: bufio.NewReader(reader), sse: true}
	done := make(chan error, 1)
	go func() {
		_, err := body.Read(make([]byte, 1))
		done <- err
	}()
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read() error = %v, want io.ErrClosedPipe", err)
	}
}
