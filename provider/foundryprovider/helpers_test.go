// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/openai/openai-go/v3/option"
)

const validEndpoint = "https://example.test"

var validCredential azcore.TokenCredential = &azfake.TokenCredential{}

const minimalResponsesJSON = `{
	"id":"resp_test",
	"object":"response",
	"created_at":1741891428,
	"status":"completed",
	"error":null,
	"incomplete_details":null,
	"model":"gpt-4o-mini",
	"output":[{"type":"message","id":"msg_test","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}]
}`

func assertPanics(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	f()
}

func writeResponsesOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, minimalResponsesJSON)
}

func newFoundryAgent(t *testing.T, server *httptest.Server, target foundryprovider.AgentTarget, config foundryprovider.AgentConfig) *agent.Agent {
	t.Helper()
	config.OpenAIOptions = append(config.OpenAIOptions, option.WithHTTPClient(server.Client()))
	return foundryprovider.NewAgent(server.URL+"/projects/proj", validCredential, target, config)
}

func mustReadBody(t *testing.T, r *http.Request) string {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("ReadAll error = %v", err)
	}
	return string(data)
}

func jsonMap(t *testing.T, data string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(data), &body); err != nil {
		t.Fatalf("request body = %s: %v", data, err)
	}
	return body
}

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

type recordingTransport struct {
	mu       sync.Mutex
	requests []recordedRequest
	handle   func(*http.Request, string) (*http.Response, error)
}

func (transport *recordingTransport) Do(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(data)
	}

	transport.mu.Lock()
	transport.requests = append(transport.requests, recordedRequest{
		Method: req.Method,
		Path:   req.URL.Path,
		Query:  req.URL.RawQuery,
		Header: req.Header.Clone(),
		Body:   body,
	})
	transport.mu.Unlock()

	if transport.handle != nil {
		return transport.handle(req, body)
	}
	return jsonResponse(req, http.StatusOK, `{}`), nil
}

func (transport *recordingTransport) Requests() []recordedRequest {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]recordedRequest(nil), transport.requests...)
}

func jsonResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
