// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

type embeddingCredential struct {
	scopes []string
	err    error
}

func (c *embeddingCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.scopes = slices.Clone(options.Scopes)
	return azcore.AccessToken{Token: "test-foundry-token", ExpiresOn: time.Now().Add(time.Hour)}, c.err
}

func writeEmbeddingsOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":"AACAPwAAAEAAAEBA"}],"model":"served-model","usage":{"prompt_tokens":4,"total_tokens":4}}`)
}

func TestNewEmbeddingGeneratorResourceRoutingAndAuthentication(t *testing.T) {
	credential := &embeddingCredential{}
	requests := make(chan recordedRequest, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		requests <- recordedRequest{
			Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery,
			Header: r.Header.Clone(), Body: mustReadBody(t, r),
		}
		writeEmbeddingsOK(w)
	}))
	defer server.Close()
	alternateCalls := 0
	alternate := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		alternateCalls++
		writeEmbeddingsOK(w)
	}))
	defer alternate.Close()
	t.Setenv("OPENAI_BASE_URL", alternate.URL)
	t.Setenv("OPENAI_API_KEY", "unused-openai-key")
	g := foundryprovider.NewEmbeddingGenerator(" "+server.URL+"/api/projects/proj%2Fname///?old=query#fragment ", credential,
		foundryprovider.EmbeddingGeneratorConfig{
			Model: " text/embedding ",
			OpenAIOptions: []option.RequestOption{
				option.WithHTTPClient(server.Client()), option.WithMaxRetries(0),
				option.WithBaseURL(alternate.URL),
				option.WithQuery("api-version", "hostile"),
				option.WithHeader("X-Test-Client", "kept"),
			},
		})
	before := time.Now()
	result, err := g.Generate(t.Context(), []string{"hello"}, nil)
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	request := <-requests
	if alternateCalls != 0 || request.Method != http.MethodPost ||
		request.Path != "/openai/v1/embeddings" || request.Query != "" {
		t.Fatalf("request = %+v; alternate calls = %d", request, alternateCalls)
	}
	if request.Header.Get("Authorization") != "Bearer test-foundry-token" ||
		request.Header.Get("X-Test-Client") != "kept" ||
		!strings.Contains(request.Header.Get("User-Agent"), "agent-framework-go/") {
		t.Errorf("request headers = %v", request.Header)
	}
	if !slices.Equal(credential.scopes, []string{"https://ai.azure.com/.default"}) {
		t.Errorf("credential scopes = %v", credential.scopes)
	}
	body := jsonMap(t, request.Body)
	if body["model"] != "text/embedding" || body["encoding_format"] != "base64" ||
		len(body["input"].([]any)) != 1 || body["input"].([]any)[0] != "hello" {
		t.Fatalf("body = %+v", body)
	}
	if _, ok := body["dimensions"]; ok {
		t.Error("unset dimensions should be omitted")
	}
	if len(result.Embeddings) != 1 || result.Usage == nil || result.Usage.InputTokenCount != 4 || result.Usage.TotalTokenCount != 4 {
		t.Fatalf("result = %+v", result)
	}
	e := result.Embeddings[0]
	if !slices.Equal(e.Vector, []float32{1, 2, 3}) || e.ModelID != "served-model" ||
		e.CreatedAt.Before(before) || e.CreatedAt.After(after) {
		t.Fatalf("embedding = %+v", e)
	}
	if _, ok := g.(io.Closer); ok {
		t.Fatal("generator should not claim ownership of the credential or transport")
	}
}

func TestNewEmbeddingGeneratorDelegatesRequestOptions(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/embeddings" || r.URL.Query().Has("api-version") {
			t.Errorf("request route = %s", r.URL)
		}
		requests <- mustReadBody(t, r)
		writeEmbeddingsOK(w)
	}))
	defer server.Close()
	dimensions := 7
	g := foundryprovider.NewEmbeddingGenerator(server.URL+"/projects/test", validCredential, foundryprovider.EmbeddingGeneratorConfig{
		Model: "default", Dimensions: &dimensions,
		OpenAIOptions: []option.RequestOption{option.WithHTTPClient(server.Client()), option.WithMaxRetries(0)},
	})
	dimensions = 99
	raw := &openai.EmbeddingNewParams{
		Model: "native", Dimensions: openai.Int(2), User: openai.String("native-user"),
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
	}
	cases := []struct {
		options    *embedding.Options
		model      string
		dimensions float64
		encoding   string
	}{
		{nil, "default", 7, "base64"},
		{&embedding.Options{ModelID: new("request"), Dimensions: new(0)}, "request", 0, "base64"},
		{&embedding.Options{ModelID: new("request"), Dimensions: new(0), RawRepresentationFactory: func(generator any) any {
			if generator != g {
				t.Error("factory must receive the generator returned by the constructor")
			}
			return raw
		}}, "native", 2, "float"},
		{nil, "default", 7, "base64"},
	}
	for _, tc := range cases {
		if _, err := g.Generate(t.Context(), []string{"hello"}, tc.options); err != nil {
			t.Fatal(err)
		}
		body := jsonMap(t, <-requests)
		if body["model"] != tc.model || body["dimensions"] != tc.dimensions || body["encoding_format"] != tc.encoding {
			t.Fatalf("body = %+v; want model=%s, dimensions=%v, encoding=%s", body, tc.model, tc.dimensions, tc.encoding)
		}
		if tc.model == "native" && body["user"] != "native-user" {
			t.Errorf("native user option missing: %v", body)
		}
	}
	if raw.Model != "native" || raw.Dimensions.Value != 2 || raw.Input.OfArrayOfStrings != nil {
		t.Fatal("generator modified factory-owned parameters")
	}
}

func TestNewEmbeddingGeneratorPropagatesErrors(t *testing.T) {
	credentialErr := errors.New("credential unavailable")
	credential := &embeddingCredential{err: credentialErr}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid dimensions","type":"invalid_request_error","code":"invalid_dimensions"}}`)
	}))
	defer server.Close()
	g := foundryprovider.NewEmbeddingGenerator(server.URL, credential, foundryprovider.EmbeddingGeneratorConfig{
		Model: "model", OpenAIOptions: []option.RequestOption{option.WithHTTPClient(server.Client()), option.WithMaxRetries(0)},
	})
	if result, err := g.Generate(t.Context(), []string{"hello"}, nil); result != nil || !errors.Is(err, credentialErr) {
		t.Fatalf("Generate = %+v, %v; want credential error", result, err)
	}
	credential.err = nil
	result, err := g.Generate(t.Context(), []string{"hello"}, &embedding.Options{Dimensions: new(0)})
	var apiErr *openai.Error
	if result != nil || !errors.As(err, &apiErr) || apiErr.Code != "invalid_dimensions" {
		t.Fatalf("Generate = %+v, %v; want OpenAI service error", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := g.Generate(ctx, []string{"hello"}, nil); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate = %+v, %v; want cancellation", result, err)
	}
	for _, batch := range [][]string{nil, {}} {
		if result, err := g.Generate(t.Context(), batch, nil); result != nil || err == nil || !strings.Contains(err.Error(), "input batch must not be empty") {
			t.Fatalf("Generate = %+v, %v; want empty batch error", result, err)
		}
	}
}

func TestNewEmbeddingGeneratorWithAPIKey(t *testing.T) {
	for _, withKey := range []bool{false, true} {
		name := "missing authentication"
		if withKey {
			name = "API key"
		}
		t.Run(name, func(t *testing.T) {
			var calls int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/openai/v1/embeddings" || r.URL.Query().Has("api-version") {
					t.Errorf("unexpected request URL: %s", r.URL)
				}
				if r.Header.Get("Api-Key") != "test-foundry-key" || r.Header.Get("Authorization") != "" {
					t.Error("expected only Azure API-key authentication")
				}
				writeEmbeddingsOK(w)
			}))
			defer server.Close()
			options := []option.RequestOption{option.WithHTTPClient(server.Client()), option.WithMaxRetries(0)}
			if withKey {
				options = append(options, azure.WithAPIKey("test-foundry-key"))
			}
			g := foundryprovider.NewEmbeddingGenerator(server.URL+"/api/projects/proj", nil, foundryprovider.EmbeddingGeneratorConfig{
				Model: "model", OpenAIOptions: options,
			})
			result, err := g.Generate(t.Context(), []string{"hello"}, nil)
			if !withKey {
				if err == nil || !strings.Contains(err.Error(), "authentication is required") || calls != 0 {
					t.Fatalf("error = %v, requests = %d; want missing authentication and no requests", err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(result.Embeddings) != 1 || !slices.Equal(result.Embeddings[0].Vector, []float32{1, 2, 3}) {
				t.Fatalf("result = %+v, requests = %d; want one embedding and 1 request", result, calls)
			}
		})
	}
}

func TestNewEmbeddingGeneratorRejectsCrossOriginRedirect(t *testing.T) {
	for _, auth := range []string{"token", "API key"} {
		t.Run(auth, func(t *testing.T) {
			destinationCalls := 0
			destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				destinationCalls++
				writeEmbeddingsOK(w)
			}))
			defer destination.Close()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL+"/embeddings", http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			credential := validCredential
			options := []option.RequestOption{option.WithHTTPClient(server.Client()), option.WithMaxRetries(0)}
			if auth == "API key" {
				credential = nil
				options = append(options, azure.WithAPIKey("test-foundry-key"))
			}
			g := foundryprovider.NewEmbeddingGenerator(server.URL, credential, foundryprovider.EmbeddingGeneratorConfig{
				Model: "test", OpenAIOptions: options,
			})
			if _, err := g.Generate(t.Context(), []string{"hello"}, nil); err == nil {
				t.Fatal("expected cross-origin redirect to fail")
			}
			if destinationCalls != 0 {
				t.Fatalf("credential-bearing redirected requests = %d, want zero", destinationCalls)
			}
		})
	}
}

func TestNewEmbeddingGeneratorRejectsConflictingAuthentication(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("conflicting authentication must not make a request")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	g := foundryprovider.NewEmbeddingGenerator(server.URL, validCredential, foundryprovider.EmbeddingGeneratorConfig{
		Model: "test",
		OpenAIOptions: []option.RequestOption{
			option.WithHTTPClient(server.Client()), option.WithMaxRetries(0), option.WithAPIKey("unused-api-key"),
		},
	})
	if result, err := g.Generate(t.Context(), []string{"hello"}, nil); result != nil || err == nil ||
		!strings.Contains(err.Error(), "Azure authentication cannot be combined") {
		t.Fatalf("Generate = %+v, %v; want conflicting authentication error", result, err)
	}
}

func TestNewEmbeddingGeneratorPanicsWithInvalidArguments(t *testing.T) {
	for _, endpoint := range []string{"", " ", "/projects/test", "://bad"} {
		t.Run(endpoint, func(t *testing.T) {
			assertPanics(t, func() {
				foundryprovider.NewEmbeddingGenerator(endpoint, validCredential, foundryprovider.EmbeddingGeneratorConfig{Model: "model"})
			})
		})
	}
	assertPanics(t, func() {
		foundryprovider.NewEmbeddingGenerator(validEndpoint, validCredential, foundryprovider.EmbeddingGeneratorConfig{Model: " "})
	}, "model is required")
}
