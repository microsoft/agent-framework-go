// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

type foundryClientTransport func(*http.Request) (*http.Response, error)

func (f foundryClientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewOpenAIClientRoutesAndAuthentication(t *testing.T) {
	for _, projectPath := range []string{"/api/projects/demo%20project", "/api/projects/demo%2Fproject", ""} {
		t.Run(projectPath, func(t *testing.T) {
			credential := &embeddingCredential{}
			var requests []string
			endpoint := "https://project.services.ai.azure.com" + projectPath
			client := foundryprovider.NewOpenAIClient(endpoint+"/?discard=true#fragment", credential,
				option.WithBaseURL("https://ignored.example.test/"),
				option.WithQuery("api-version", "ignored"),
				option.WithHeader("X-Test-Client", "kept"),
				option.WithMaxRetries(0),
				option.WithHTTPClient(&http.Client{Transport: foundryClientTransport(func(req *http.Request) (*http.Response, error) {
					requests = append(requests, req.Method+" "+req.URL.EscapedPath())
					if req.URL.Host != "project.services.ai.azure.com" {
						t.Errorf("host = %q, want the configured Foundry origin", req.URL.Host)
					}
					if req.Header.Get("Authorization") != "Bearer test-foundry-token" || req.Header.Get("X-Test-Client") != "kept" {
						t.Error("missing credential authorization or custom header")
					}
					admin := strings.Contains(req.URL.Path, "/agents/")
					if !admin && req.URL.Query().Has("api-version") || admin && req.URL.Query().Get("api-version") != "v1" {
						t.Errorf("unexpected api-version on %s", req.URL)
					}
					if req.URL.Query().Has("discard") || req.URL.Fragment != "" {
						t.Errorf("endpoint query or fragment leaked into request: %s", req.URL)
					}
					return jsonResponse(req, http.StatusOK, `{}`), nil
				})}),
			)
			if _, err := client.Embeddings.New(t.Context(), openai.EmbeddingNewParams{
				Model: "embedding/model", Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: []string{"hello"}},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
				Model: "chat/model", Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Responses.New(t.Context(), responses.ResponseNewParams{Model: "model"}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Files.New(t.Context(), openai.FileNewParams{
				File: openai.File(strings.NewReader("document"), "document.md", "text/markdown"), Purpose: openai.FilePurposeAssistants,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.VectorStores.New(t.Context(), openai.VectorStoreNewParams{Name: openai.String("store")}); err != nil {
				t.Fatal(err)
			}
			var version map[string]any
			err := client.Post(t.Context(), "agents/test/versions", map[string]any{"definition": map[string]any{"kind": "prompt"}}, &version,
				option.WithBaseURL(endpoint+"/"), option.WithQuery("api-version", "v1"))
			if err != nil {
				t.Fatal(err)
			}
			err = client.Delete(t.Context(), "agents/test", nil, nil,
				option.WithBaseURL(endpoint+"/"), option.WithQuery("api-version", "v1"))
			if err != nil {
				t.Fatal(err)
			}
			// Request-specific administration routing must not alter the next SDK request.
			if _, err := client.Files.Delete(t.Context(), "file-test"); err != nil {
				t.Fatal(err)
			}
			want := []string{
				"POST " + projectPath + "/openai/v1/embeddings",
				"POST " + projectPath + "/openai/v1/chat/completions",
				"POST " + projectPath + "/openai/v1/responses",
				"POST " + projectPath + "/openai/v1/files",
				"POST " + projectPath + "/openai/v1/vector_stores",
				"POST " + projectPath + "/agents/test/versions",
				"DELETE " + projectPath + "/agents/test",
				"DELETE " + projectPath + "/openai/v1/files/file-test",
			}
			if !slices.Equal(requests, want) {
				t.Fatalf("requests = %v, want %v", requests, want)
			}
			if !slices.Equal(credential.scopes, []string{"https://ai.azure.com/.default"}) {
				t.Fatalf("credential scopes = %v", credential.scopes)
			}
		})
	}
}

func TestNewOpenAIClientPanicsWithInvalidArguments(t *testing.T) {
	for _, endpoint := range []string{"", " ", "/projects/test", "://bad"} {
		t.Run(endpoint, func(t *testing.T) {
			assertPanics(t, func() { foundryprovider.NewOpenAIClient(endpoint, validCredential) })
		})
	}
	assertPanics(t, func() { foundryprovider.NewOpenAIClient(validEndpoint, nil) }, "credential is required")
}
