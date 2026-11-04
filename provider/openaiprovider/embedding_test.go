// Copyright (c) Microsoft. All rights reserved.

package openaiprovider_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func openAIEmbeddingTestClient(t *testing.T, handler http.HandlerFunc) openai.Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL),
		option.WithHTTPClient(server.Client()),
		option.WithMaxRetries(0),
	)
}

func TestOpenAIEmbeddingGenerator_RequestAndResponse(t *testing.T) {
	requests := make(chan []byte, 1)
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "agent-framework-go/") || !strings.Contains(ua, "OpenAI/Go") || strings.Contains(ua, "(feat=") {
			t.Errorf("User-Agent = %q", ua)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"data":[
				{"index":1,"object":"embedding","embedding":[-0.5,0.25,2]},
				{"index":0,"object":"embedding","embedding":[1,2,3]}
			],
			"model":"served-model","object":"list",
			"usage":{"prompt_tokens":42,"total_tokens":42}
		}`)
	})
	values := []string{"first", "second"}
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "text-embedding-3-small"})
	before := time.Now()
	result, err := g.Generate(t.Context(), values, nil)
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	bodyEqual(t, string(<-requests), `{"model":"text-embedding-3-small","input":["first","second"],"encoding_format":"base64"}`)
	if !slices.Equal(values, []string{"first", "second"}) {
		t.Fatal("Generate changed inputs")
	}
	if len(result.Embeddings) != 2 {
		t.Fatalf("embeddings = %d, want 2", len(result.Embeddings))
	}
	for i, want := range []embedding.VectorOf[float32]{{1, 2, 3}, {-0.5, 0.25, 2}} {
		got := result.Embeddings[i]
		if !slices.Equal(got.Vector, want) || got.Dimensions() != 3 || got.ModelID != "served-model" {
			t.Fatalf("embedding[%d] = %+v", i, got)
		}
		if got.CreatedAt.Before(before) || got.CreatedAt.After(after) || got.CreatedAt.Location() != time.UTC {
			t.Errorf("creation time = %v, want local UTC creation time", got.CreatedAt)
		}
	}
	if result.Usage == nil || result.Usage.InputTokenCount != 42 || result.Usage.TotalTokenCount != 42 || result.Usage.OutputTokenCount != 0 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}

func TestOpenAIEmbeddingGenerator_OptionsPrecedence(t *testing.T) {
	for _, test := range []struct {
		name       string
		config     openaiprovider.EmbeddingGeneratorConfig
		options    *embedding.Options
		want       string
		wantNative *openai.EmbeddingNewParams
	}{
		{
			"config",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config", Dimensions: new(7)},
			nil,
			`{"model":"config","dimensions":7,"input":["hello"],"encoding_format":"base64"}`, nil,
		},
		{
			"request",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config", Dimensions: new(7)},
			&embedding.Options{ModelID: new("request"), Dimensions: new(3)},
			`{"model":"request","dimensions":3,"input":["hello"],"encoding_format":"base64"}`, nil,
		},
		{
			"zero",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config", Dimensions: new(7)},
			&embedding.Options{Dimensions: new(0)},
			`{"model":"config","dimensions":0,"input":["hello"],"encoding_format":"base64"}`, nil,
		},
		{
			"config zero",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config", Dimensions: new(0)},
			nil,
			`{"model":"config","dimensions":0,"input":["hello"],"encoding_format":"base64"}`, nil,
		},
		{
			"empty model",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config"},
			&embedding.Options{ModelID: new("")},
			`{"model":"","input":["hello"],"encoding_format":"base64"}`, nil,
		},
		{
			"native pointer",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config", Dimensions: new(7)},
			&embedding.Options{ModelID: new("request"), Dimensions: new(3)},
			`{"model":"native","dimensions":2,"user":"test-user","input":["hello"],"encoding_format":"base64"}`,
			&openai.EmbeddingNewParams{
				Model: "native", Dimensions: openai.Int(2), User: openai.String("test-user"),
				Input: openai.EmbeddingNewParamsInputUnion{OfString: openai.String("ignored")},
			},
		},
		{
			"native null dimensions",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config", Dimensions: new(7)},
			&embedding.Options{Dimensions: new(3)},
			`{"model":"native","dimensions":null,"input":["hello"],"encoding_format":"base64"}`,
			&openai.EmbeddingNewParams{Model: "native", Dimensions: param.Null[int64]()},
		},
		{
			"native empty parameters",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config", Dimensions: new(7)},
			&embedding.Options{ModelID: new("request"), Dimensions: new(3)},
			`{"model":"request","dimensions":3,"input":["hello"],"encoding_format":"base64"}`,
			&openai.EmbeddingNewParams{},
		},
		{
			"native float encoding",
			openaiprovider.EmbeddingGeneratorConfig{Model: "config"},
			&embedding.Options{},
			`{"model":"config","input":["hello"],"encoding_format":"float"}`,
			&openai.EmbeddingNewParams{EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan []byte, 1)
			client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2,3]}],"model":"served"}`)
			})
			options := test.options
			if options != nil {
				options.AdditionalProperties = map[string]any{"not-sent": "value"}
			}
			g := openaiprovider.NewEmbeddingGenerator(client, test.config)
			if test.config.Dimensions != nil {
				*test.config.Dimensions = 999 // The generator owns its default.
			}
			var before []byte
			if test.wantNative != nil {
				var err error
				before, err = json.Marshal(test.wantNative)
				if err != nil {
					t.Fatal(err)
				}
				options.RawRepresentationFactory = func(generator any) any {
					if generator != g {
						t.Error("factory did not receive the concrete generator")
					}
					return test.wantNative
				}
			}
			if _, err := g.Generate(t.Context(), []string{"hello"}, options); err != nil {
				t.Fatal(err)
			}
			bodyEqual(t, string(<-requests), test.want)
			if test.wantNative != nil {
				after, err := json.Marshal(test.wantNative)
				if err != nil || string(after) != string(before) {
					t.Fatalf("Generate changed native parameters: %s -> %s, %v", before, after, err)
				}
			}
			if options != nil && options.AdditionalProperties["not-sent"] != "value" {
				t.Fatal("Generate changed request options")
			}
		})
	}
}

func TestOpenAIEmbeddingGenerator_RawOptionsFactory(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  any
		want string
	}{
		{"nil", nil, `{"model":"default","input":["hello"],"encoding_format":"base64"}`},
		{"typed nil", (*openai.EmbeddingNewParams)(nil), `{"model":"default","input":["hello"],"encoding_format":"base64"}`},
		{"value", openai.EmbeddingNewParams{Model: "raw-model", User: openai.String("raw-user")}, `{"model":"raw-model","user":"raw-user","input":["hello"],"encoding_format":"base64"}`},
		{"unrecognized type", struct{}{}, `{"model":"default","input":["hello"],"encoding_format":"base64"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan []byte, 1)
			client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1]}]}`)
			})
			g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
			factoryCalls := 0
			_, err := g.Generate(t.Context(), []string{"hello"}, &embedding.Options{
				RawRepresentationFactory: func(any) any {
					factoryCalls++
					return test.raw
				},
			})
			if err != nil || factoryCalls != 1 {
				t.Fatalf("Generate error = %v; factory calls = %d", err, factoryCalls)
			}
			bodyEqual(t, string(<-requests), test.want)
		})
	}
}

func TestOpenAIEmbeddingGenerator_NativeExtraFields(t *testing.T) {
	requests := make(chan []byte, 1)
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1]}]}`)
	})
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	for _, extras := range []map[string]any{
		{"custom": "value"},
		{"custom": "value", "model": "native"},
	} {
		params := &openai.EmbeddingNewParams{}
		params.SetExtraFields(extras)
		before, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		_, err = g.Generate(t.Context(), []string{"hello"}, &embedding.Options{
			ModelID: new(""), RawRepresentationFactory: func(any) any { return params },
		})
		if err != nil {
			t.Fatal(err)
		}
		model := ""
		if _, ok := extras["model"]; ok {
			model = "native"
		}
		bodyEqual(t, string(<-requests), fmt.Sprintf(`{"custom":"value","model":%q,"input":["hello"],"encoding_format":"base64"}`, model))
		after, err := json.Marshal(params)
		if err != nil || string(after) != string(before) {
			t.Fatalf("Generate changed native extra fields: %s -> %s, %v", before, after, err)
		}
	}
}

func base64Embedding(vector []float32) string {
	data := make([]byte, 4*len(vector))
	for i, value := range vector {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return base64.StdEncoding.EncodeToString(data)
}

func TestOpenAIEmbeddingGenerator_Base64(t *testing.T) {
	want := embedding.VectorOf[float32]{1, -0.5, 0.25}
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		if request["encoding_format"] != "base64" {
			t.Errorf("encoding_format = %v", request["encoding_format"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"index":0,"embedding":%q}],"model":"served","usage":{"prompt_tokens":1,"total_tokens":1}}`, base64Embedding(want))
	})
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	vector, err := embedding.GenerateVector(t.Context(), g, "hello", nil)
	if err != nil || !slices.Equal(vector, want) {
		t.Fatalf("GenerateVector = %v, %v", vector, err)
	}
}

func TestOpenAIEmbeddingGenerator_Base64PreservesFloatBits(t *testing.T) {
	bits := []uint32{0x7fc01234, 0x7f800000, 0xff800000, 0x80000000, 0x3fffffff}
	want := make([]float32, len(bits))
	for i, bit := range bits {
		want[i] = math.Float32frombits(bit)
	}
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encoded := strings.ReplaceAll(base64Embedding(want), "/", `\/`)
		_, _ = fmt.Fprintf(w, `{"data":[{"index":0,"embedding":"%s"}]}`, encoded)
	})
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	vector, err := embedding.GenerateVector(t.Context(), g, "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != len(bits) {
		t.Fatalf("vector length = %d, want %d", len(vector), len(bits))
	}
	for i, value := range vector {
		if got := math.Float32bits(value); got != bits[i] {
			t.Errorf("vector[%d] bits = %08x, want %08x", i, got, bits[i])
		}
	}
}

func TestOpenAIEmbeddingGenerator_EmptyVectorsAndUsage(t *testing.T) {
	for _, test := range []struct {
		name      string
		response  string
		wantUsage bool
	}{
		{"empty vector", `{"data":[{"index":0,"embedding":[]}],"usage":{"prompt_tokens":0,"total_tokens":0}}`, true},
		{"empty base64", `{"data":[{"index":0,"embedding":""}]}`, false},
		{"no usage", `{"data":[{"index":0,"embedding":[1]}]}`, false},
		{"null usage", `{"data":[{"index":0,"embedding":[1]}],"usage":null}`, false},
		{"large usage", `{"data":[{"index":0,"embedding":[1]}],"usage":{"prompt_tokens":1099511627776,"total_tokens":1099511627776}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.response)
			})
			g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
			result, err := g.Generate(t.Context(), []string{"hello"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if (result.Usage != nil) != test.wantUsage {
				t.Fatalf("usage = %+v", result.Usage)
			}
			if test.name == "large usage" && (result.Usage.InputTokenCount != 1<<40 || result.Usage.TotalTokenCount != 1<<40) {
				t.Fatalf("usage truncated: %+v", result.Usage)
			}
			if result.Embeddings[0].ModelID != "" {
				t.Fatal("missing served model should not be replaced with requested model")
			}
			if strings.HasPrefix(test.name, "empty") {
				vector := result.Embeddings[0].Vector
				if vector == nil || len(vector) != 0 {
					t.Fatalf("empty vector = %v", vector)
				}
			}
		})
	}
}

func TestOpenAIEmbeddingGenerator_UsageCounters(t *testing.T) {
	for _, test := range []struct {
		name      string
		usage     string
		input     int64
		total     int64
		wantError bool
	}{
		{"omitted counters", `{}`, 0, 0, false},
		{"omitted total", `{"prompt_tokens":7}`, 7, 0, false},
		{"omitted prompt", `{"total_tokens":8}`, 0, 8, false},
		{"zero", `{"prompt_tokens":0,"total_tokens":0}`, 0, 0, false},
		{"negative counters", `{"prompt_tokens":-1,"total_tokens":-2}`, -1, -2, false},
		{"int64 counters", `{"prompt_tokens":1099511627776,"total_tokens":1099511627777}`, 1 << 40, 1<<40 + 1, false},
		{"fractional prompt", `{"prompt_tokens":1.5,"total_tokens":2}`, 0, 0, true},
		{"fractional total", `{"prompt_tokens":1,"total_tokens":2.75}`, 0, 0, true},
		{"decimal", `{"prompt_tokens":1.0}`, 0, 0, true},
		{"exponent", `{"prompt_tokens":1e1}`, 0, 0, true},
		{"null prompt", `{"prompt_tokens":null}`, 0, 0, true},
		{"null total", `{"total_tokens":null}`, 0, 0, true},
		{"string prompt", `{"prompt_tokens":"1"}`, 0, 0, true},
		{"string total", `{"total_tokens":"1"}`, 0, 0, true},
		{"overflow prompt", `{"prompt_tokens":9223372036854775808}`, 0, 0, true},
		{"overflow total", `{"total_tokens":-9223372036854775809}`, 0, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1]}],"usage":`+test.usage+`}`)
			})
			g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
			result, err := g.Generate(t.Context(), []string{"hello"}, nil)
			if test.wantError {
				if err == nil || result != nil {
					t.Fatalf("Generate = %+v, %v; want invalid token count error", result, err)
				}
			} else if err != nil || result == nil || result.Usage == nil ||
				result.Usage.InputTokenCount != test.input || result.Usage.TotalTokenCount != test.total {
				t.Fatalf("Generate = %+v, %v; want usage %d/%d", result, err, test.input, test.total)
			}
		})
	}
}

func TestOpenAIEmbeddingGenerator_MalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		inputs   int
	}{
		{"null response", `null`, 1},
		{"null data", `{"data":null}`, 1},
		{"wrong data type", `{"data":"wrong"}`, 1},
		{"missing embedding", `{"data":[{"index":0}]}`, 1},
		{"null embedding", `{"data":[{"index":0,"embedding":null}]}`, 1},
		{"invalid embedding", `{"data":[{"index":0,"embedding":{"wrong":true}}]}`, 1},
		{"invalid numeric element", `{"data":[{"index":0,"embedding":[1,"wrong"]}]}`, 1},
		{"null numeric element", `{"data":[{"index":0,"embedding":[1,null]}]}`, 1},
		{"invalid base64", `{"data":[{"index":0,"embedding":"!"}]}`, 1},
		{"invalid base64 size", `{"data":[{"index":0,"embedding":"AQID"}]}`, 1},
		{"float32 overflow", `{"data":[{"index":0,"embedding":[1e100]}]}`, 1},
		{"null index", `{"data":[{"index":null,"embedding":[1]}]}`, 1},
		{"fractional index", `{"data":[{"index":0.5,"embedding":[1]}]}`, 1},
		{"negative index", `{"data":[{"index":-1,"embedding":[1]}]}`, 1},
		{"out-of-range index", `{"data":[{"index":1,"embedding":[1]}]}`, 1},
		{"duplicate indexes", `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`, 2},
		{"missing batch index", `{"data":[{"embedding":[1]},{"index":1,"embedding":[2]}]}`, 2},
		{"empty data", `{"data":[]}`, 1},
		{"omitted data", `{}`, 1},
		{"too few embeddings", `{"data":[{"index":0,"embedding":[1]}]}`, 2},
		{"too many embeddings", `{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[2]}]}`, 1},
		{"invalid usage", `{"data":[{"index":0,"embedding":[1]}],"usage":"wrong"}`, 1},
		{"invalid total tokens", `{"data":[{"index":0,"embedding":[1]}],"usage":{"prompt_tokens":1,"total_tokens":"wrong"}}`, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.response)
			})
			values := make([]string, test.inputs)
			for i := range values {
				values[i] = "hello"
			}
			g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
			if result, err := g.Generate(t.Context(), values, nil); err == nil || result != nil {
				t.Fatalf("Generate = %+v, %v; want explicit response error", result, err)
			}
		})
	}
}

func TestOpenAIEmbeddingGenerator_GenerateAndZipPreservesInputOrder(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
	}{
		{"numeric", `{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}]}`},
		{"base64", fmt.Sprintf(`{"data":[{"index":1,"embedding":%q},{"index":0,"embedding":%q}]}`,
			base64Embedding([]float32{2}), base64Embedding([]float32{1}))},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.response)
			})
			g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
			values := []string{"first", "second"}
			pairs, err := embedding.GenerateAndZip(t.Context(), g, values, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(pairs) != len(values) {
				t.Fatalf("pairs = %d, want %d", len(pairs), len(values))
			}
			for i, pair := range pairs {
				if pair.Value != values[i] || pair.Embedding == nil ||
					!slices.Equal(pair.Embedding.Vector, []float32{float32(i + 1)}) {
					t.Errorf("pair[%d] = %+v; want input %q with vector [%d]", i, pair, values[i], i+1)
				}
			}
		})
	}
}

func TestOpenAIEmbeddingGenerator_AllowsMissingSingleIndex(t *testing.T) {
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"embedding":[3]}]}`)
	})
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	vector, err := embedding.GenerateVector(t.Context(), g, "hello", nil)
	if err != nil || !slices.Equal(vector, []float32{3}) {
		t.Fatalf("GenerateVector = %v, %v", vector, err)
	}
}

func TestOpenAIEmbeddingGenerator_EmptyBatch(t *testing.T) {
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("empty batch must not make an HTTP request")
		w.WriteHeader(http.StatusInternalServerError)
	})
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	for _, values := range [][]string{nil, {}} {
		result, err := g.Generate(t.Context(), values, &embedding.Options{RawRepresentationFactory: func(any) any {
			t.Error("empty batch should not invoke the options factory")
			return nil
		}})
		if err == nil || result != nil || !strings.Contains(err.Error(), "input batch must not be empty") {
			t.Fatalf("Generate = %+v, %v; want empty batch error", result, err)
		}
	}
}

func TestOpenAIEmbeddingGenerator_ServiceError(t *testing.T) {
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid dimensions","type":"invalid_request_error","code":"invalid_dimensions","param":"dimensions"}}`)
	})
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	result, err := g.Generate(t.Context(), []string{"hello"}, &embedding.Options{Dimensions: new(0)})
	var apiErr *openai.Error
	if result != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || apiErr.Code != "invalid_dimensions" {
		t.Fatalf("Generate = %+v, %v", result, err)
	}
}

type embeddingRoundTripper func(*http.Request) (*http.Response, error)

func (f embeddingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAIEmbeddingGenerator_Cancellation(t *testing.T) {
	calls := atomic.Int32{}
	entered := make(chan struct{})
	client := openai.NewClient(option.WithAPIKey("test"), option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Transport: embeddingRoundTripper(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			close(entered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}))
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, values := range [][]string{nil, {"hello"}} {
		result, err := g.Generate(ctx, values, nil)
		if result != nil || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
			t.Fatalf("canceled Generate = %+v, %v; requests = %d", result, err, calls.Load())
		}
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := g.Generate(ctx, []string{"hello"}, nil)
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation error = %v", err)
	}
}

func TestOpenAIEmbeddingGenerator_TransportError(t *testing.T) {
	want := errors.New("transport unavailable")
	client := openai.NewClient(option.WithAPIKey("test"), option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Transport: embeddingRoundTripper(func(*http.Request) (*http.Response, error) {
			return nil, want
		})}))
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	if result, err := g.Generate(t.Context(), []string{"hello"}, nil); result != nil || !errors.Is(err, want) {
		t.Fatalf("Generate = %+v, %v; want original transport error", result, err)
	}
}

func TestOpenAIEmbeddingGenerator_ConcurrentCalls(t *testing.T) {
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"index":0,"embedding":[1]}],"model":%q}`, body.Model)
	})
	g := openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "default"})
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			model := fmt.Sprintf("model-%d", i)
			options := &embedding.Options{ModelID: &model}
			e, err := embedding.Generate(t.Context(), g, "hello", options)
			if err != nil || e.ModelID != model || *options.ModelID != model {
				t.Errorf("Generate = %+v, %v; want %s", e, err, model)
			}
		})
	}
	wg.Wait()
}

func TestOpenAIEmbeddingGenerator_OpenTelemetry(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	previousTracer, previousMeter := otel.GetTracerProvider(), otel.GetMeterProvider()
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(t.Context())
		_ = meterProvider.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
	})
	client := openAIEmbeddingTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2,3]}],"model":"served-model","usage":{"prompt_tokens":10,"total_tokens":10}}`)
	})
	g := otelprovider.WrapEmbeddingGenerator(
		openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{Model: "request-model"}),
		otelprovider.EmbeddingGeneratorConfig{ProviderName: "openai", DefaultModelID: "request-model"},
	)
	if vector, err := embedding.GenerateVector(t.Context(), g, "hello", nil); err != nil || !slices.Equal(vector, []float32{1, 2, 3}) {
		t.Fatalf("GenerateVector = %v, %v", vector, err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Unset || spans[0].Name != "embeddings request-model" {
		t.Fatalf("spans = %+v", spans)
	}
	attrs := attribute.NewSet(spans[0].Attributes...)
	model, _ := attrs.Value("gen_ai.response.model")
	tokens, _ := attrs.Value("gen_ai.usage.input_tokens")
	if model.AsString() != "served-model" || tokens.AsInt64() != 10 {
		t.Fatalf("span attributes = %v", spans[0].Attributes)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	var usage int64
	for _, scope := range metrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "gen_ai.client.token.usage" {
				for _, point := range m.Data.(metricdata.Histogram[int64]).DataPoints {
					usage += point.Sum
				}
			}
		}
	}
	if usage != 10 {
		t.Fatalf("usage metric = %d, want 10", usage)
	}
}

func ExampleNewEmbeddingGenerator() {
	generator := openaiprovider.NewEmbeddingGenerator(openai.NewClient(), openaiprovider.EmbeddingGeneratorConfig{
		Model: openai.EmbeddingModelTextEmbedding3Small,
	})
	vector, err := embedding.GenerateVector(context.Background(), generator, "Hello, world!", nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(len(vector))
}
