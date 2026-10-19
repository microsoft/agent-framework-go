// Copyright (c) Microsoft. All rights reserved.

package otelprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/otelx"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/anthropicprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/provider/geminiprovider"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"

	otellib "go.opentelemetry.io/otel"
)

func setupTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	originalProvider := otellib.GetTracerProvider()
	otellib.SetTracerProvider(tp)

	t.Cleanup(func() {
		_ = tp.Shutdown(t.Context())
		otellib.SetTracerProvider(originalProvider)
	})
	return exporter
}

func setupMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := otellib.GetMeterProvider()
	otellib.SetMeterProvider(provider)

	t.Cleanup(func() {
		_ = provider.Shutdown(t.Context())
		otellib.SetMeterProvider(originalProvider)
	})
	return reader
}

func collectMetric(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Aggregation {
	t.Helper()
	resourceMetrics := collectMetrics(t, reader)
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			if m.Name == name {
				return m.Data
			}
		}
	}
	t.Fatalf("metric %q was not recorded", name)
	return nil
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var resourceMetrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resourceMetrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return resourceMetrics
}

func hasMetric(resourceMetrics metricdata.ResourceMetrics, name string) bool {
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}

func metricAttribute(set attribute.Set, key string) (string, bool) {
	value, ok := set.Value(attribute.Key(key))
	return value.AsString(), ok
}

func TestOtel_ModelTracing_Adapters(t *testing.T) {
	sse := func(event, data string) string {
		return "event: " + event + "\ndata: " + data + "\n\n"
	}
	response := `{"id":"response-1","object":"response","created_at":1,"model":"served-model","status":"completed","output":[{"type":"message","id":"message-1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}],"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16}}`
	responseEvents := sse("response.created", `{"type":"response.created","response":{"id":"response-1","model":"served-model","status":"in_progress"}}`) +
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"message-1","role":"assistant","content":[]}}`) +
		sse("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"message-1","output_index":0,"content_index":0,"delta":"done"}`) +
		sse("response.completed", `{"type":"response.completed","response":`+response+`}`)
	for _, tc := range []struct {
		adapter  string
		provider string
		body     string
		events   string
	}{
		{
			adapter: "chat", provider: "openai",
			body:   `{"id":"response-1","object":"chat.completion","created":1,"model":"served-model","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`,
			events: "data: " + `{"id":"response-1","object":"chat.completion.chunk","created":1,"model":"served-model","choices":[{"index":0,"delta":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}` + "\n\ndata: [DONE]\n\n",
		},
		{adapter: "responses", provider: "openai", body: response, events: responseEvents},
		{adapter: "foundry", provider: "microsoft.foundry", body: response, events: responseEvents},
		{
			adapter: "anthropic", provider: "anthropic",
			body: `{"id":"response-1","type":"message","role":"assistant","model":"served-model","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":5}}`,
			events: sse("message_start", `{"type":"message_start","message":{"id":"response-1","type":"message","role":"assistant","model":"served-model","content":[],"usage":{"input_tokens":11,"output_tokens":0}}}`) +
				sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
				sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`) +
				sse("content_block_stop", `{"type":"content_block_stop","index":0}`) +
				sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`) +
				sse("message_stop", `{"type":"message_stop"}`),
		},
		{
			adapter: "gemini", provider: "gcp.gemini",
			body:   `{"responseId":"response-1","modelVersion":"served-model","candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":5,"totalTokenCount":16}}`,
			events: "data: " + `{"responseId":"response-1","modelVersion":"served-model","candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":5,"totalTokenCount":16}}` + "\n\n",
		},
	} {
		for _, stream := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/enabled=%v", tc.adapter, stream, enabled), func(t *testing.T) {
					exporter := setupTracer(t)
					reader := setupMeter(t)
					var requests atomic.Int32
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						if tc.adapter == "gemini" {
							if !strings.Contains(r.URL.Path, "request-model") {
								t.Errorf("model request path = %q", r.URL.Path)
							}
						} else if body["model"] != "request-model" {
							t.Errorf("request model = %v", body["model"])
						}
						contentType, data := "application/json", tc.body
						if stream {
							contentType, data = "text/event-stream", tc.events
						}
						w.Header().Set("Content-Type", contentType)
						if _, err := fmt.Fprint(w, data); err != nil {
							t.Error(err)
						}
					}))
					defer server.Close()
					cfg := agent.Config{
						Name: "ModelTest",
						Middlewares: []agent.Middleware{otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{
							SourceName: "model-test", EnableModelTracing: enabled,
						})},
					}
					a := modelTestAgent(t, tc.adapter, server, cfg)
					options := []agent.Option{agent.Stream(stream)}
					switch tc.adapter {
					case "chat":
						options = append(options, openaiprovider.ChatCompletionNewParams(openai.ChatCompletionNewParams{Model: "request-model"}))
					case "responses", "foundry":
						options = append(options, openaiprovider.ResponsesNewParams(responses.ResponseNewParams{Model: "request-model"}))
					case "anthropic":
						options = append(options, anthropicprovider.MessageNewParams(anthropic.MessageNewParams{Model: "request-model"}))
					}
					run := a.RunText(t.Context(), "Private prompt contents.", options...)
					if requests.Load() != 0 || len(exporter.GetSpans()) != 0 {
						t.Fatal("unconsumed run started model tracing or a request")
					}
					got, err := run.Collect()
					if err != nil || got.String() != "done" {
						t.Fatalf("response = %v, %v", got, err)
					}
					if requests.Load() != 1 || got.Usage().InputTokenCount != 11 || got.Usage().OutputTokenCount != 5 {
						t.Fatalf("requests = %d, usage = %+v", requests.Load(), got.Usage())
					}
					spans := exporter.GetSpans()
					if !enabled {
						if len(spans) != 1 {
							t.Fatalf("default tracing emitted %d spans, want one agent span", len(spans))
						}
						return
					}
					if len(spans) != 2 {
						t.Fatalf("spans = %d, want one agent and one model span", len(spans))
					}
					agentSpan := findSpanByOperation(t, spans, "invoke_agent")
					modelSpan := findSpanByOperation(t, spans, "chat")
					if modelSpan.Name != "chat request-model" || modelSpan.SpanKind != trace.SpanKindClient ||
						modelSpan.Parent.SpanID() != agentSpan.SpanContext.SpanID() ||
						modelSpan.SpanContext.TraceID() != agentSpan.SpanContext.TraceID() ||
						modelSpan.InstrumentationScope.Name != "model-test" {
						t.Fatalf("model span = %+v", modelSpan)
					}
					attrs := modelSpanAttributes(modelSpan)
					for key, want := range map[string]string{
						"gen_ai.provider.name": tc.provider, "gen_ai.request.model": "request-model",
						"gen_ai.response.model": "served-model", "gen_ai.response.id": "response-1",
					} {
						if attrs[key].AsString() != want {
							t.Errorf("%s = %v, want %q", key, attrs[key], want)
						}
					}
					if attrs["gen_ai.usage.input_tokens"].AsInt64() != 11 ||
						attrs["gen_ai.usage.output_tokens"].AsInt64() != 5 ||
						!slices.Equal(attrs["gen_ai.response.finish_reasons"].AsStringSlice(), []string{"stop"}) {
						t.Fatalf("model response attributes = %v", attrs)
					}
					if strings.Contains(fmt.Sprint(modelSpan.Attributes), "Private prompt contents.") {
						t.Fatal("model span captured prompt contents")
					}
					duration := collectMetric(t, reader, "gen_ai.client.operation.duration").(metricdata.Histogram[float64])
					var modelPoints int
					for _, point := range duration.DataPoints {
						if operation, _ := metricAttribute(point.Attributes, "gen_ai.operation.name"); operation == "chat" {
							modelPoints++
							if point.Count != 1 || point.Sum != modelSpan.EndTime.Sub(modelSpan.StartTime).Seconds() {
								t.Errorf("model duration point = %+v", point)
							}
						}
					}
					if modelPoints != 1 {
						t.Fatalf("model duration points = %d, want 1", modelPoints)
					}
					tokens := collectMetric(t, reader, "gen_ai.client.token.usage").(metricdata.Histogram[int64])
					var inputs, outputs int
					for _, point := range tokens.DataPoints {
						if operation, _ := metricAttribute(point.Attributes, "gen_ai.operation.name"); operation != "chat" {
							continue
						}
						switch kind, _ := metricAttribute(point.Attributes, "gen_ai.token.type"); kind {
						case "input":
							inputs++
							if point.Sum != 11 {
								t.Errorf("model input token sum = %d", point.Sum)
							}
						case "output":
							outputs++
							if point.Sum != 5 {
								t.Errorf("model output token sum = %d", point.Sum)
							}
						}
					}
					if inputs != 1 || outputs != 1 {
						t.Fatalf("model input/output token points = %d/%d, want 1/1", inputs, outputs)
					}
				})
			}
		}
	}
}

func modelTestAgent(t *testing.T, adapter string, server *httptest.Server, cfg agent.Config) *agent.Agent {
	t.Helper()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test"), option.WithHTTPClient(server.Client()), option.WithMaxRetries(0))
	switch adapter {
	case "chat":
		return openaiprovider.NewChatCompletionsAgent(client, openaiprovider.AgentConfig{Config: cfg, Model: "default-model"})
	case "responses":
		return openaiprovider.NewResponsesAgent(client, openaiprovider.AgentConfig{Config: cfg, Model: "default-model", DisableStoreOutput: true})
	case "foundry":
		return foundryprovider.NewAgent(server.URL, &azfake.TokenCredential{}, foundryprovider.ModelDeployment("default-model"), foundryprovider.AgentConfig{
			Config: cfg, DisableStoreOutput: true,
			OpenAIOptions: []option.RequestOption{option.WithHTTPClient(server.Client()), option.WithMaxRetries(0)},
		})
	case "anthropic":
		return anthropicprovider.NewAgent(anthropic.NewClient(
			anthropicoption.WithBaseURL(server.URL), anthropicoption.WithAPIKey("test"),
			anthropicoption.WithHTTPClient(server.Client()), anthropicoption.WithMaxRetries(0),
		), anthropicprovider.AgentConfig{Config: cfg, Model: "default-model"})
	case "gemini":
		client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
			Backend: genai.BackendGeminiAPI, APIKey: "test", HTTPClient: server.Client(),
			HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
		})
		if err != nil {
			t.Fatal(err)
		}
		return geminiprovider.NewAgent(client, geminiprovider.AgentConfig{Config: cfg, Model: "request-model"})
	default:
		t.Fatalf("unknown model adapter %q", adapter)
		return nil
	}
}

func TestOtel_ModelTracing_Lifecycle(t *testing.T) {
	failure := errors.New("model failed")
	for _, tc := range []struct {
		name      string
		stopEarly bool
		cancel    bool
		err       error
		usage     *message.UsageDetails
	}{
		{name: "empty"},
		{name: "no usage"},
		{name: "zero usage", usage: &message.UsageDetails{}},
		{name: "usage", usage: &message.UsageDetails{InputTokenCount: 7, OutputTokenCount: 3, CachedInputTokenCount: 2, ReasoningTokenCount: 1}},
		{name: "failure", err: failure},
		{name: "cancelled", cancel: true, err: context.Canceled},
		{name: "failure wins over cancellation", cancel: true, err: failure},
		{name: "early stop", stopEarly: true},
		{name: "cancelled early stop", stopEarly: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exporter := setupTracer(t)
			reader := setupMeter(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var released bool
			update := &agent.ResponseUpdate{Contents: message.NewText("partial").Contents}
			if tc.usage != nil {
				update.Contents = append(update.Contents, &message.UsageContent{Details: *tc.usage})
			}
			a := agent.New(agent.ProviderConfig{
				ProviderName: "test-provider",
				Run: otelx.WrapModelRun(func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						defer func() { released = true }()
						if tc.cancel {
							cancel()
						}
						if tc.err != nil {
							yield(nil, tc.err)
							return
						}
						if tc.name == "empty" {
							return
						}
						otelx.RecordModelResponse(ctx, "served-model", "response-id")
						if !yield(update, nil) {
							return
						}
						if tc.stopEarly {
							t.Error("provider continued after early stop")
						}
					}
				}, func([]agent.Option) string { return "request-model" }),
			}, agent.Config{
				Middlewares: []agent.Middleware{otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{EnableModelTracing: true})},
			})
			var gotErr error
			for got, err := range a.RunText(ctx, "request", agent.Stream(true)) {
				if err != nil {
					gotErr = err
					break
				}
				if got != nil && got != update {
					t.Error("tracing replaced the provider's response update")
				}
				if tc.stopEarly {
					break
				}
			}
			if !errors.Is(gotErr, tc.err) || !released {
				t.Fatalf("error = %v, want %v; provider released = %v", gotErr, tc.err, released)
			}
			modelSpan := findSpanByOperation(t, exporter.GetSpans(), "chat")
			spanErr := tc.err
			if spanErr == nil && tc.cancel {
				spanErr = context.Canceled
			}
			if modelSpan.EndTime.IsZero() || (modelSpan.Status.Code == codes.Error) != (spanErr != nil) {
				t.Fatalf("span status = %v, want error = %v", modelSpan.Status, spanErr)
			}
			attrs := modelSpanAttributes(modelSpan)
			if tc.usage == nil {
				if _, ok := attrs["gen_ai.usage.input_tokens"]; ok {
					t.Error("missing usage was reported as zero usage")
				}
			} else {
				for key, want := range map[string]int64{
					"gen_ai.usage.input_tokens": tc.usage.InputTokenCount, "gen_ai.usage.output_tokens": tc.usage.OutputTokenCount,
				} {
					value, ok := attrs[key]
					if !ok || value.AsInt64() != want {
						t.Errorf("%s = %v, present = %v, want %d", key, value, ok, want)
					}
				}
				if tc.usage.CachedInputTokenCount > 0 && attrs["gen_ai.usage.cache_read.input_tokens"].AsInt64() != 2 {
					t.Errorf("cached input usage = %v", attrs)
				}
				if tc.usage.ReasoningTokenCount > 0 && attrs["gen_ai.usage.reasoning.output_tokens"].AsInt64() != 1 {
					t.Errorf("reasoning usage = %v", attrs)
				}
			}
			duration := collectMetric(t, reader, "gen_ai.client.operation.duration").(metricdata.Histogram[float64])
			var modelCalls uint64
			for _, point := range duration.DataPoints {
				if operation, _ := metricAttribute(point.Attributes, "gen_ai.operation.name"); operation == "chat" {
					modelCalls += point.Count
					if spanErr != nil {
						value, ok := metricAttribute(point.Attributes, "error.type")
						if !ok || value != otelx.ErrorTypeName(spanErr) {
							t.Errorf("metric error type = %q, present = %v", value, ok)
						}
					}
				}
			}
			if modelCalls != 1 {
				t.Fatalf("recorded model calls = %d, want 1", modelCalls)
			}
		})
	}
}

func TestOtel_ModelTracing_AdapterFailures(t *testing.T) {
	for _, adapter := range []string{"chat", "responses", "foundry", "anthropic", "gemini"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", adapter, stream), func(t *testing.T) {
				exporter := setupTracer(t)
				var requests atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					if _, err := fmt.Fprint(w, `{"error":{"message":"model request failed"}}`); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				a := modelTestAgent(t, adapter, server, agent.Config{
					Middlewares: []agent.Middleware{otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{EnableModelTracing: true})},
				})
				response, err := a.RunText(t.Context(), "request", agent.Stream(stream)).Collect()
				if err == nil || response != nil || requests.Load() != 1 {
					t.Fatalf("response = %v, error = %v, requests = %d", response, err, requests.Load())
				}
				span := findSpanByOperation(t, exporter.GetSpans(), "chat")
				attrs := modelSpanAttributes(span)
				if span.Status.Code != codes.Error || span.EndTime.IsZero() ||
					attrs["error.type"].AsString() != otelx.ErrorTypeName(err) {
					t.Fatalf("model failure span = %+v", span)
				}
				if _, ok := attrs["gen_ai.usage.input_tokens"]; ok {
					t.Fatal("failed request fabricated usage")
				}
			})
		}
	}
}

func TestOtel_ModelTracing_CancellationAfterSuccessfulResponse(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			exporter := setupTracer(t)
			reader := setupMeter(t)
			data := `{"id":"response-1","object":"chat.completion","created":1,"model":"served-model","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`
			if stream {
				data = "data: " + `{"id":"response-1","object":"chat.completion.chunk","created":1,"model":"served-model","choices":[{"index":0,"delta":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}` +
					"\n\ndata: [DONE]\n\n"
			}
			body := &modelTestBody{Reader: strings.NewReader(data)}
			client := &http.Client{Transport: modelTestTransport(func(r *http.Request) (*http.Response, error) {
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{contentType}},
					Body:       body,
					Request:    r,
				}, nil
			})}
			a := openaiprovider.NewChatCompletionsAgent(openai.NewClient(
				option.WithAPIKey("test"), option.WithHTTPClient(client), option.WithMaxRetries(0),
			), openaiprovider.AgentConfig{
				Model:        "request-model",
				ToolAutoCall: &toolautocall.Config{MaximumIterationsPerRequest: new(0)},
				Config: agent.Config{Middlewares: []agent.Middleware{
					otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{EnableModelTracing: true}),
				}},
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var response agent.Response
			for update, err := range a.RunText(ctx, "request", agent.Stream(stream)) {
				if err != nil {
					t.Fatal(err)
				}
				response.Update(update)
				cancel()
			}
			if response.String() != "done" || !body.closed.Load() || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("response = %q, body closed = %v, context error = %v", response.String(), body.closed.Load(), ctx.Err())
			}
			span := findSpanByOperation(t, exporter.GetSpans(), "chat")
			if span.Status.Code == codes.Error || span.EndTime.IsZero() {
				t.Errorf("successful response was recorded as an error: %+v", span.Status)
			}
			if _, ok := modelSpanAttributes(span)["error.type"]; ok {
				t.Error("successful model span has an error.type attribute")
			}
			duration := collectMetric(t, reader, "gen_ai.client.operation.duration").(metricdata.Histogram[float64])
			var modelCalls uint64
			for _, point := range duration.DataPoints {
				if operation, _ := metricAttribute(point.Attributes, "gen_ai.operation.name"); operation == "chat" {
					modelCalls += point.Count
					if _, ok := metricAttribute(point.Attributes, "error.type"); ok {
						t.Error("successful model duration metric has an error.type attribute")
					}
				}
			}
			if modelCalls != 1 {
				t.Fatalf("recorded model calls = %d, want 1", modelCalls)
			}
		})
	}
}

func TestOtel_ModelTracing_EarlyStopClosesAdapterResponse(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			exporter := setupTracer(t)
			body := &modelTestBody{Reader: strings.NewReader("data: " +
				`{"id":"response-1","object":"chat.completion.chunk","created":1,"model":"served-model","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}` +
				"\n\ndata: [DONE]\n\n")}
			client := &http.Client{Transport: modelTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       body,
					Request:    r,
				}, nil
			})}
			a := openaiprovider.NewChatCompletionsAgent(openai.NewClient(
				option.WithAPIKey("test"), option.WithHTTPClient(client), option.WithMaxRetries(0),
			), openaiprovider.AgentConfig{
				Model: "request-model",
				Config: agent.Config{Middlewares: []agent.Middleware{
					otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{EnableModelTracing: true}),
				}},
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var text string
			for update, err := range a.RunText(ctx, "request", agent.Stream(true)) {
				if err != nil {
					t.Fatal(err)
				}
				if update != nil && update.String() != "" {
					text = update.String()
					if cancelled {
						cancel()
					}
					break
				}
			}
			if text != "partial" || !body.closed.Load() {
				t.Fatalf("stream text = %q, response body closed = %v", text, body.closed.Load())
			}
			span := findSpanByOperation(t, exporter.GetSpans(), "chat")
			if span.EndTime.IsZero() || (span.Status.Code == codes.Error) != cancelled {
				t.Fatalf("model span after early stop = %+v", span)
			}
		})
	}
}

func TestOtel_ModelTracing_ConcurrentCallsKeepMetadataIsolated(t *testing.T) {
	exporter := setupTracer(t)
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: otelx.WrapModelRun(func(ctx context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				model, _ := agent.GetOption(options, agent.WithInstructions)
				otelx.RecordModelResponse(ctx, "served-"+model, "response-"+model)
				yield(&agent.ResponseUpdate{Contents: message.NewText("done").Contents}, nil)
			}
		}, func(options []agent.Option) string {
			model, _ := agent.GetOption(options, agent.WithInstructions)
			return model
		}),
	}, agent.Config{Middlewares: []agent.Middleware{otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{EnableModelTracing: true})}})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if _, err := a.RunText(t.Context(), "request", agent.WithInstructions(fmt.Sprintf("model-%d", i))).Collect(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var calls int
	for _, span := range exporter.GetSpans() {
		attrs := modelSpanAttributes(span)
		if attrs["gen_ai.operation.name"].AsString() != "chat" {
			continue
		}
		calls++
		request := attrs["gen_ai.request.model"].AsString()
		if attrs["gen_ai.response.model"].AsString() != "served-"+request || attrs["gen_ai.response.id"].AsString() != "response-"+request {
			t.Errorf("model metadata leaked between calls: %v", attrs)
		}
	}
	if calls != 20 {
		t.Fatalf("model spans = %d, want 20", calls)
	}
}

func modelSpanAttributes(span tracetest.SpanStub) map[string]attribute.Value {
	attrs := make(map[string]attribute.Value, len(span.Attributes))
	for _, entry := range span.Attributes {
		attrs[string(entry.Key)] = entry.Value
	}
	return attrs
}

type modelTestTransport func(*http.Request) (*http.Response, error)

func (f modelTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type modelTestBody struct {
	*strings.Reader
	closed atomic.Bool
}

func (b *modelTestBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestOtel_Run_RecordsOperationDurationMetric(t *testing.T) {
	exporter := setupTracer(t)
	reader := setupMeter(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{Name: "test-agent", Middlewares: []agent.Middleware{mw}})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	var resourceMetrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resourceMetrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			if m.Name == "gen_ai.client.operation.duration" {
				histogram, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("operation duration metric has type %T, want float64 histogram", m.Data)
				}
				if len(histogram.DataPoints) != 1 {
					t.Fatalf("operation duration data points = %d, want 1", len(histogram.DataPoints))
				}
				dataPoint := histogram.DataPoints[0]
				wantBounds := []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
				if !slices.Equal(dataPoint.Bounds, wantBounds) {
					t.Errorf("operation duration bounds = %v, want %v", dataPoint.Bounds, wantBounds)
				}
				if operation, _ := metricAttribute(dataPoint.Attributes, "gen_ai.operation.name"); operation != "invoke_agent" {
					t.Errorf("gen_ai.operation.name = %q, want invoke_agent", operation)
				}
				if provider, _ := metricAttribute(dataPoint.Attributes, "gen_ai.provider.name"); provider != "test-provider" {
					t.Errorf("gen_ai.provider.name = %q, want test-provider", provider)
				}
				for _, key := range []string{"gen_ai.agent.name", "gen_ai.agent.id"} {
					if _, ok := metricAttribute(dataPoint.Attributes, key); ok {
						t.Errorf("operation duration data point unexpectedly contains %s", key)
					}
				}
				spans := exporter.GetSpans()
				if len(spans) != 1 {
					t.Fatalf("spans = %d, want 1", len(spans))
				}
				if want := spans[0].EndTime.Sub(spans[0].StartTime).Seconds(); dataPoint.Sum != want {
					t.Errorf("operation duration metric = %v, span duration = %v", dataPoint.Sum, want)
				}
				return
			}
		}
	}
	t.Fatal("gen_ai.client.operation.duration metric was not recorded")
}

func TestOtel_Run_RecordsTokenUsageMetric(t *testing.T) {
	reader := setupMeter(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				if !yield(&agent.ResponseUpdate{Contents: []message.Content{&message.UsageContent{
					Details: message.UsageDetails{InputTokenCount: 100, OutputTokenCount: 10},
				}}}, nil) {
					return
				}
				yield(&agent.ResponseUpdate{Contents: []message.Content{&message.UsageContent{
					Details: message.UsageDetails{InputTokenCount: 200, OutputTokenCount: 20},
				}}}, nil)
			}
		},
	}, agent.Config{Name: "test-agent", Middlewares: []agent.Middleware{mw}})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	data := collectMetric(t, reader, "gen_ai.client.token.usage")
	histogram, ok := data.(metricdata.Histogram[int64])
	if !ok {
		t.Fatalf("token usage metric has type %T, want int64 histogram", data)
	}
	if len(histogram.DataPoints) != 2 {
		t.Fatalf("token usage data points = %d, want 2", len(histogram.DataPoints))
	}
	counts := make(map[string]int64, 2)
	for _, dataPoint := range histogram.DataPoints {
		wantBounds := []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}
		if !slices.Equal(dataPoint.Bounds, wantBounds) {
			t.Errorf("token usage bounds = %v, want %v", dataPoint.Bounds, wantBounds)
		}
		tokenType, ok := metricAttribute(dataPoint.Attributes, "gen_ai.token.type")
		if !ok {
			t.Fatal("token usage data point is missing gen_ai.token.type")
		}
		counts[tokenType] = dataPoint.Sum
		if operation, _ := metricAttribute(dataPoint.Attributes, "gen_ai.operation.name"); operation != "invoke_agent" {
			t.Errorf("gen_ai.operation.name = %q, want invoke_agent", operation)
		}
		if provider, _ := metricAttribute(dataPoint.Attributes, "gen_ai.provider.name"); provider != "test-provider" {
			t.Errorf("gen_ai.provider.name = %q, want test-provider", provider)
		}
		if _, ok := metricAttribute(dataPoint.Attributes, "gen_ai.agent.name"); ok {
			t.Error("token usage data point unexpectedly contains gen_ai.agent.name")
		}
	}
	if counts["input"] != 300 {
		t.Errorf("input token usage = %d, want 300", counts["input"])
	}
	if counts["output"] != 30 {
		t.Errorf("output token usage = %d, want 30", counts["output"])
	}
}

func TestOtel_Run_DoesNotRecordUnreportedTokenUsage(t *testing.T) {
	reader := setupMeter(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{MessageID: "no-usage"}, nil)
		}
	}

	for _, err := range mw.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
	}

	resourceMetrics := collectMetrics(t, reader)
	if hasMetric(resourceMetrics, "gen_ai.client.token.usage") {
		t.Error("token usage metric was recorded when the provider reported no usage")
	}
	if !hasMetric(resourceMetrics, "gen_ai.client.operation.duration") {
		t.Error("operation duration metric was not recorded")
	}
}

func TestOtel_Run_DoesNotRecordEmptyUsageContent(t *testing.T) {
	reader := setupMeter(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Contents: []message.Content{&message.UsageContent{}}}, nil)
		}
	}

	for _, err := range mw.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
	}

	resourceMetrics := collectMetrics(t, reader)
	if hasMetric(resourceMetrics, "gen_ai.client.token.usage") {
		t.Error("token usage metric was recorded for an empty usage payload")
	}
	if !hasMetric(resourceMetrics, "gen_ai.client.operation.duration") {
		t.Error("operation duration metric was not recorded")
	}
}

func TestOtel_Run_DoesNotRecordTokenMetricForOptionalCountersOnly(t *testing.T) {
	reader := setupMeter(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Contents: []message.Content{&message.UsageContent{
				Details: message.UsageDetails{CachedInputTokenCount: 42},
			}}}, nil)
		}
	}

	for _, err := range mw.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
	}

	resourceMetrics := collectMetrics(t, reader)
	if hasMetric(resourceMetrics, "gen_ai.client.token.usage") {
		t.Error("token usage metric was recorded without input or output counts")
	}
}

func TestOtel_Run_MetricErrorAttributes(t *testing.T) {
	reader := setupMeter(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	testErr := errors.New("boom")
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Contents: []message.Content{&message.UsageContent{
				Details: message.UsageDetails{InputTokenCount: 10, OutputTokenCount: 5},
			}}}, testErr)
		}
	}

	for _, err := range mw.Run(next, t.Context(), nil) {
		if !errors.Is(err, testErr) {
			t.Fatalf("middleware error = %v, want %v", err, testErr)
		}
	}

	resourceMetrics := collectMetrics(t, reader)
	var duration, usage metricdata.Aggregation
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			switch m.Name {
			case "gen_ai.client.operation.duration":
				duration = m.Data
			case "gen_ai.client.token.usage":
				usage = m.Data
			}
		}
	}
	durationHistogram, ok := duration.(metricdata.Histogram[float64])
	if !ok || len(durationHistogram.DataPoints) != 1 {
		t.Fatalf("operation duration metric = %T with %d data points, want one float64 histogram data point", duration, len(durationHistogram.DataPoints))
	}
	if errorType, ok := metricAttribute(durationHistogram.DataPoints[0].Attributes, "error.type"); !ok || errorType != "errorString" {
		t.Errorf("duration error.type = %q, %v; want errorString, true", errorType, ok)
	}
	if operation, _ := metricAttribute(durationHistogram.DataPoints[0].Attributes, "gen_ai.operation.name"); operation != "invoke_agent" {
		t.Errorf("duration gen_ai.operation.name = %q, want invoke_agent", operation)
	}
	usageHistogram, ok := usage.(metricdata.Histogram[int64])
	if !ok {
		t.Fatalf("token usage metric has type %T, want int64 histogram", usage)
	}
	for _, dataPoint := range usageHistogram.DataPoints {
		if _, ok := metricAttribute(dataPoint.Attributes, "error.type"); ok {
			t.Error("token usage data point unexpectedly contains error.type")
		}
	}
}

func TestOtel_Run_RecordsMetricsOnEarlyBreak(t *testing.T) {
	reader := setupMeter(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for range 3 {
				if !yield(&agent.ResponseUpdate{Contents: []message.Content{&message.UsageContent{
					Details: message.UsageDetails{InputTokenCount: 10, OutputTokenCount: 5},
				}}}, nil) {
					return
				}
			}
		}
	}

	for _, err := range mw.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		break
	}

	resourceMetrics := collectMetrics(t, reader)
	if !hasMetric(resourceMetrics, "gen_ai.client.operation.duration") {
		t.Error("operation duration metric was not recorded after an early break")
	}
	if !hasMetric(resourceMetrics, "gen_ai.client.token.usage") {
		t.Error("token usage metric was not recorded after an early break")
	}
}

func TestOtel_Run_CreatesSpan(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	nextCalled := false
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		nextCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
		}
	}

	messages := []*message.Message{message.NewText("test message")}

	seq := mw.Run(next, t.Context(), messages)
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
	}

	if !nextCalled {
		t.Error("expected next function to be called")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
}

// The invoke_agent span must carry gen_ai.response.finish_reasons, matching the
// Python/.NET observability which set it from the response finish reason.
func TestOtel_Run_SpanHasFinishReasons(t *testing.T) {
	exporter := setupTracer(t)
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "m1", ResponseID: "r1", FinishReason: "stop"}, nil)
			}
		},
	}, agent.Config{Middlewares: []agent.Middleware{mw}})

	if _, err := a.RunMessage(t.Context(), message.NewText("hi")).Collect(); err != nil {
		t.Fatalf("run: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	span := spans[len(spans)-1]

	var got []string
	found := false
	for _, attr := range span.Attributes {
		if string(attr.Key) == "gen_ai.response.finish_reasons" {
			got = attr.Value.AsStringSlice()
			found = true
		}
	}
	if !found {
		t.Fatal("expected gen_ai.response.finish_reasons attribute to be present")
	}
	if len(got) != 1 || got[0] != "stop" {
		t.Errorf("gen_ai.response.finish_reasons = %v, want [stop]", got)
	}
}

func TestOtel_Run_SpanHasCorrectAttributes(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	var capturedCtx context.Context

	// Override the agent metadata for this test
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				capturedCtx = ctx
				yield(&agent.ResponseUpdate{MessageID: "test-1", ResponseID: "response-1"}, nil)
			}
		},
	}, agent.Config{
		ID:          "test-agent-id",
		Name:        "test-agent",
		Description: "A test agent",
		Middlewares: []agent.Middleware{mw},
	})

	// Run through agent to get metadata in context
	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	_ = capturedCtx // silence unused warning

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}

	span := spans[len(spans)-1]

	// Check span name is "<operation> <target>" per GenAI conventions
	if span.Name != "invoke_agent test-agent" {
		t.Errorf("expected span name 'invoke_agent test-agent', got %s", span.Name)
	}

	// Check attributes
	attrs := make(map[string]string)
	for _, attr := range span.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}

	expectedAttrs := map[string]string{
		"gen_ai.operation.name":    "invoke_agent",
		"gen_ai.provider.name":     "test-provider",
		"gen_ai.agent.id":          "test-agent-id",
		"gen_ai.agent.name":        "test-agent",
		"gen_ai.agent.description": "A test agent",
		"gen_ai.response.id":       "response-1",
	}

	for key, expected := range expectedAttrs {
		if got, ok := attrs[key]; !ok {
			t.Errorf("expected attribute %q to be present", key)
		} else if got != expected {
			t.Errorf("expected attribute %q to be %q, got %q", key, expected, got)
		}
	}
}

func TestOtel_Run_SpanNamePrefixedWithOperation(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{
		Name:        "helper",
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	// GenAI conventions require the span be named "<operation> <target>", matching the
	// sibling execute_tool span and the .NET/Python SDKs.
	if spans[0].Name != "invoke_agent helper" {
		t.Errorf("expected span name %q, got %q", "invoke_agent helper", spans[0].Name)
	}
}

func TestOtel_Run_SpanNameFallsBackToAgentID(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	// Agent with an id but no name: the span target falls back to the agent id,
	// matching .NET/Python.
	a := agent.New(agent.ProviderConfig{
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{
		ID:          "agent-123",
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	// With no name, the target is the agent id.
	if spans[0].Name != "invoke_agent agent-123" {
		t.Errorf("expected span name %q, got %q", "invoke_agent agent-123", spans[0].Name)
	}
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "gen_ai.agent.id" && attr.Value.AsString() != "agent-123" {
			t.Errorf("expected gen_ai.agent.id %q, got %q", "agent-123", attr.Value.AsString())
		}
	}
}

func TestOtel_Run_OmitsEmptyAgentNameAndDescription(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	// Agent with no name and no description: .NET and Python omit these attributes
	// rather than emitting empty strings.
	a := agent.New(agent.ProviderConfig{
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	for _, attr := range spans[0].Attributes {
		if key := string(attr.Key); key == "gen_ai.agent.name" || key == "gen_ai.agent.description" {
			t.Errorf("expected %q to be omitted for an unnamed/undescribed agent", key)
		}
	}
}

func TestOtel_Run_RecordsError(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	testErr := errors.New("test error")
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, testErr)
		}
	}

	messages := []*message.Message{message.NewText("test message")}

	seq := mw.Run(next, t.Context(), messages)
	for _, err := range seq {
		if !errors.Is(err, testErr) {
			t.Fatalf("middleware error = %v, want %v", err, testErr)
		}
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]

	// Check that error was recorded
	hasErrorEvent := false
	for _, event := range span.Events {
		if event.Name == "exception" {
			hasErrorEvent = true
			break
		}
	}

	if !hasErrorEvent {
		t.Error("expected span to have an exception event")
	}

	// Check span status is error
	if span.Status.Code != 1 { // codes.Error = 1 in OTel Go SDK
		t.Errorf("expected span status code to be Error (1), got %d", span.Status.Code)
	}

	if span.Status.Description != "test error" {
		t.Errorf("expected span status description to be 'test error', got %s", span.Status.Description)
	}

	// Check error.type attribute is set (parity with Python capture_exception)
	attrs := make(map[string]string)
	for _, attr := range span.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["error.type"] != "errorString" {
		t.Errorf("expected error.type %q, got %q", "errorString", attrs["error.type"])
	}
}

func TestOtel_Run_CustomSourceName(t *testing.T) {
	exporter := setupTracer(t)

	customSource := "my-custom-source"
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: customSource})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	// The instrumentation scope name should be our custom source
	if spans[0].InstrumentationScope.Name != customSource {
		t.Errorf("expected instrumentation scope name %q, got %q", customSource, spans[0].InstrumentationScope.Name)
	}
}

func TestOtel_Run_DefaultSourceName(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	expectedSource := "github.com/microsoft/agent-framework-go"
	if spans[0].InstrumentationScope.Name != expectedSource {
		t.Errorf("expected instrumentation scope name %q, got %q", expectedSource, spans[0].InstrumentationScope.Name)
	}
}

func TestOtel_Run_PropagatesContext(t *testing.T) {
	setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	var capturedCtx context.Context
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedCtx = ctx
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
	}

	if capturedCtx == nil {
		t.Fatal("context was not propagated")
	}

	// Check that the context has a span
	span := trace.SpanFromContext(capturedCtx)
	if span == nil {
		t.Error("expected context to have a span")
	}

	// The span should be recording
	if !span.SpanContext().IsValid() {
		t.Error("expected span context to be valid")
	}
}

func TestOtel_Run_HandlesMultipleUpdates(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	updateCount := 0
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for range 5 {
				if !yield(&agent.ResponseUpdate{MessageID: "test"}, nil) {
					return
				}
			}
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		updateCount++
	}

	if updateCount != 5 {
		t.Errorf("expected 5 updates, got %d", updateCount)
	}

	// Should still have only one span
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestOtel_Run_EmitsExecuteToolSpanForAutocall(t *testing.T) {
	exporter := setupTracer(t)

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call-1", Name: "get_weather", Arguments: `{}`},
				},
			}).
			NewTurn().
			AddText("sunny").
			Build(),
	}

	var toolSpanContext trace.SpanContext
	getWeather := functool.MustNew(
		functool.Config{Name: "get_weather", Description: "Returns the current weather"},
		func(ctx context.Context, args struct{}) (string, error) {
			toolSpanContext = trace.SpanContextFromContext(ctx)
			return "sunny", nil
		},
	)

	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		ID:   "test-agent-id",
		Name: "test-agent",
		Middlewares: []agent.Middleware{
			otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: "test-source"}),
			toolautocall.New(toolautocall.Config{}),
		},
		Tools: []tool.Tool{getWeather},
	})

	_, err := a.RunMessage(t.Context(), message.NewText("weather?")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !toolSpanContext.IsValid() {
		t.Fatal("expected tool call to observe a valid span context")
	}

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	invokeSpan := findSpanByOperation(t, spans, "invoke_agent")
	executeToolSpan := findSpanByOperation(t, spans, "execute_tool")

	if executeToolSpan.InstrumentationScope.Name != "test-source" {
		t.Fatalf("expected execute_tool span scope %q, got %q", "test-source", executeToolSpan.InstrumentationScope.Name)
	}
	if executeToolSpan.Parent.SpanID() != invokeSpan.SpanContext.SpanID() {
		t.Fatalf("expected execute_tool parent %s, got %s", invokeSpan.SpanContext.SpanID(), executeToolSpan.Parent.SpanID())
	}
	if executeToolSpan.Name != "execute_tool get_weather" {
		t.Fatalf("expected execute_tool span name %q, got %q", "execute_tool get_weather", executeToolSpan.Name)
	}

	executeToolAttrs := make(map[string]string)
	for _, attr := range executeToolSpan.Attributes {
		executeToolAttrs[string(attr.Key)] = attr.Value.AsString()
	}
	if executeToolAttrs["gen_ai.tool.call.id"] != "call-1" {
		t.Errorf("expected gen_ai.tool.call.id %q, got %q", "call-1", executeToolAttrs["gen_ai.tool.call.id"])
	}
	if executeToolAttrs["gen_ai.tool.type"] != "function" {
		t.Errorf("expected gen_ai.tool.type %q, got %q", "function", executeToolAttrs["gen_ai.tool.type"])
	}
	if executeToolAttrs["gen_ai.tool.description"] != "Returns the current weather" {
		t.Errorf("expected gen_ai.tool.description %q, got %q", "Returns the current weather", executeToolAttrs["gen_ai.tool.description"])
	}
}

func TestOtel_Run_ExecuteToolSpan_EmptyCallIDFallsBackToUnknown(t *testing.T) {
	exporter := setupTracer(t)

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					// No CallID set — simulates an LLM that omits the call ID field.
					&message.FunctionCallContent{Name: "get_weather", Arguments: `{}`},
				},
			}).
			NewTurn().
			AddText("sunny").
			Build(),
	}

	getWeather := functool.MustNew(
		functool.Config{Name: "get_weather"},
		func(ctx context.Context, args struct{}) (string, error) {
			return "sunny", nil
		},
	)

	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		Name: "test-agent",
		Middlewares: []agent.Middleware{
			otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: "test-source"}),
			toolautocall.New(toolautocall.Config{}),
		},
		Tools: []tool.Tool{getWeather},
	})

	_, err := a.RunMessage(t.Context(), message.NewText("weather?")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	executeToolSpan := findSpanByOperation(t, spans, "execute_tool")

	attrs := make(map[string]string)
	for _, attr := range executeToolSpan.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["gen_ai.tool.call.id"] != "unknown" {
		t.Errorf("expected gen_ai.tool.call.id %q when CallID is empty, got %q", "unknown", attrs["gen_ai.tool.call.id"])
	}
	if _, hasDesc := attrs["gen_ai.tool.description"]; hasDesc {
		t.Errorf("expected gen_ai.tool.description to be absent when tool has no description")
	}
}

func TestOtel_Run_ExecuteToolSpan_SetsErrorTypeOnToolError(t *testing.T) {
	exporter := setupTracer(t)

	toolErr := errors.New("tool failure")

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call-err", Name: "failing_tool", Arguments: `{}`},
				},
			}).
			NewTurn().
			AddText("done").
			Build(),
	}

	failingTool := functool.MustNew(
		functool.Config{Name: "failing_tool"},
		func(ctx context.Context, args struct{}) (string, error) {
			return "", toolErr
		},
	)

	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		Name: "test-agent",
		Middlewares: []agent.Middleware{
			otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: "test-source"}),
			toolautocall.New(toolautocall.Config{}),
		},
		Tools: []tool.Tool{failingTool},
	})

	_, err := a.RunMessage(t.Context(), message.NewText("go")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	executeToolSpan := findSpanByOperation(t, spans, "execute_tool")

	attrs := make(map[string]string)
	for _, attr := range executeToolSpan.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["error.type"] != "errorString" {
		t.Errorf("expected error.type %q, got %q", "errorString", attrs["error.type"])
	}

	hasErrorEvent := false
	for _, event := range executeToolSpan.Events {
		if event.Name == "exception" {
			hasErrorEvent = true
			break
		}
	}
	if !hasErrorEvent {
		t.Error("expected execute_tool span to have an exception event")
	}
}

func findSpanByOperation(t *testing.T, spans []tracetest.SpanStub, operation string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		for _, attr := range span.Attributes {
			if string(attr.Key) == "gen_ai.operation.name" && attr.Value.AsString() == operation {
				return span
			}
		}
	}
	t.Fatalf("expected span with operation %q", operation)
	return tracetest.SpanStub{}
}

func TestOtel_Run_HandlesEarlyBreak(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for range 10 {
				if !yield(&agent.ResponseUpdate{MessageID: "test"}, nil) {
					return
				}
			}
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})

	count := 0
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count >= 3 {
			break
		}
	}

	if count != 3 {
		t.Errorf("expected 3 updates before break, got %d", count)
	}

	// Span should still be ended
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestOtel_Run_UnknownProviderWhenNoMetadata(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	// Use context without metadata
	seq := mw.Run(next, t.Context(), []*message.Message{})
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	attrs := make(map[string]string)
	for _, attr := range spans[0].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}

	// When no metadata, provider should be "unknown"
	if attrs["gen_ai.provider.name"] != "unknown" {
		t.Errorf("expected provider name to be 'unknown', got %s", attrs["gen_ai.provider.name"])
	}
}

func TestOtel_Run_RecordsMultipleErrors(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	err1 := errors.New("first error")
	err2 := errors.New("second error")

	callCount := 0
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{}, err1) {
				return
			}
			yield(&agent.ResponseUpdate{}, err2)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	wantErrors := []error{err1, err2}
	for _, err := range seq {
		if callCount >= len(wantErrors) {
			t.Fatalf("received more than %d errors", len(wantErrors))
		}
		if !errors.Is(err, wantErrors[callCount]) {
			t.Errorf("error %d = %v, want %v", callCount, err, wantErrors[callCount])
		}
		callCount++
	}

	if callCount != 2 {
		t.Errorf("expected 2 iterations, got %d", callCount)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	// Check both errors were recorded
	errorCount := 0
	for _, event := range spans[0].Events {
		if event.Name == "exception" {
			errorCount++
		}
	}

	if errorCount != 2 {
		t.Errorf("expected 2 exception events, got %d", errorCount)
	}
}

func TestOtel_Run_RecordsTokenUsage(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	// Two updates, each carrying usage -- an agent that makes several LLM round-trips
	// emits one UsageContent per round-trip. The span must report the SUM, not the last
	// one, or a multi-turn agent under-reports what it actually spent.
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				if !yield(&agent.ResponseUpdate{
					MessageID: "turn-1",
					Contents: []message.Content{&message.UsageContent{
						Details: message.UsageDetails{
							InputTokenCount:       100,
							OutputTokenCount:      10,
							TotalTokenCount:       110,
							CachedInputTokenCount: 5,
						},
					}},
				}, nil) {
					return
				}
				yield(&agent.ResponseUpdate{
					MessageID: "turn-2",
					Contents: []message.Content{&message.UsageContent{
						Details: message.UsageDetails{
							InputTokenCount:  200,
							OutputTokenCount: 20,
							TotalTokenCount:  220,
						},
					}},
				}, nil)
			}
		},
	}, agent.Config{
		ID:          "test-agent-id",
		Name:        "test-agent",
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	attrs := map[string]any{}
	for _, attr := range spans[len(spans)-1].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}

	for key, want := range map[string]int64{
		"gen_ai.usage.input_tokens":            300, // 100 + 200
		"gen_ai.usage.output_tokens":           30,  // 10 + 20
		"gen_ai.usage.cache_read.input_tokens": 5,
	} {
		got, ok := attrs[key]
		if !ok {
			t.Fatalf("span is missing %s", key)
		}
		if got != want {
			t.Fatalf("%s = %v, want %d", key, got, want)
		}
	}

	// A provider that reports no reasoning tokens must not get a zero-valued attribute:
	// "absent" and "zero" mean different things, and an always-present always-zero
	// attribute trains people to ignore it.
	if _, ok := attrs["gen_ai.usage.reasoning.output_tokens"]; ok {
		t.Fatal("reasoning.output_tokens should be omitted when the provider reports none")
	}
}

func TestOtel_Run_NoUsageAttributesWhenProviderReportsNone(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "no-usage"}, nil)
			}
		},
	}, agent.Config{ID: "id", Name: "n", Middlewares: []agent.Middleware{mw}})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	for _, attr := range spans[len(spans)-1].Attributes {
		if strings.HasPrefix(string(attr.Key), "gen_ai.usage.") {
			t.Fatalf("unexpected usage attribute %s on a run with no usage reported", attr.Key)
		}
	}
}

// Regression for the guard bug caught in review: setUsage originally returned early when
// input/output/total were all zero, which silently dropped the ENTIRE attribute set for a
// provider that reported only cached (or only reasoning) tokens. "Did we see any usage?"
// has to consider every counter, not just the three required ones.
func TestOtel_Run_RecordsUsageWhenOnlyOptionalCountersReported(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{
					MessageID: "cached-only",
					Contents: []message.Content{&message.UsageContent{
						// Only a cached count. input/output/total all zero.
						Details: message.UsageDetails{CachedInputTokenCount: 42},
					}},
				}, nil)
			}
		},
	}, agent.Config{ID: "id", Name: "n", Middlewares: []agent.Middleware{mw}})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	attrs := map[string]any{}
	for _, attr := range spans[len(spans)-1].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}

	got, ok := attrs["gen_ai.usage.cache_read.input_tokens"]
	if !ok {
		t.Fatal("cache_read.input_tokens was dropped when it was the only counter reported")
	}
	if got != int64(42) {
		t.Fatalf("cache_read.input_tokens = %v, want 42", got)
	}
}
