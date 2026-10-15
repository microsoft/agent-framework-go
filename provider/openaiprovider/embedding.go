// Copyright (c) Microsoft. All rights reserved.

package openaiprovider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"time"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
)

// EmbeddingGeneratorConfig configures an OpenAI embedding generator.
type EmbeddingGeneratorConfig struct {
	// Model is the default embedding model or deployment name.
	Model string
	// Dimensions is the default vector size. Nil uses the service default.
	// Explicit zero is forwarded; supported values depend on the service.
	Dimensions *int
}

// NewEmbeddingGenerator creates a text embedding generator using the client's
// Embeddings API. Results preserve the service's response order and contain
// float32 [embedding.VectorOf] vectors, the response model ID, and a local UTC
// creation timestamp for each embedding. Reported prompt and total token usage
// are preserved; omitted counters default to zero and malformed counters return
// an error. Empty batches return an error without an HTTP request.
//
// The [embedding.Options.RawRepresentationFactory] may return an
// [openai.EmbeddingNewParams] value or pointer. Nil or other return types use
// default parameters.
// Native model and dimension settings take precedence over request options,
// which take precedence over config. Other AdditionalProperties are not
// interpreted; use native parameters for provider-specific settings.
// The request input is set from the supplied values. The default encoding is
// base64; native parameters may request float vectors instead.
//
// The generator does not own the client and does not require cleanup.
func NewEmbeddingGenerator(client openai.Client, config EmbeddingGeneratorConfig) embedding.Generator[string, *embedding.Of[float32]] {
	if config.Dimensions != nil {
		config.Dimensions = new(*config.Dimensions)
	}
	return &embeddingGenerator{client: client, config: config}
}

type embeddingGenerator struct {
	client openai.Client
	config EmbeddingGeneratorConfig
}

func (g *embeddingGenerator) Generate(ctx context.Context, values []string, options *embedding.Options) (*embedding.GeneratedOf[float32], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("openaiprovider: input batch must not be empty")
	}
	var params openai.EmbeddingNewParams
	if options != nil && options.RawRepresentationFactory != nil {
		switch raw := options.RawRepresentationFactory(g).(type) {
		case nil:
		case openai.EmbeddingNewParams:
			params = raw
		case *openai.EmbeddingNewParams:
			if raw != nil {
				params = *raw
			}
		}
	}
	model := g.config.Model
	dimensions := g.config.Dimensions
	if options != nil {
		if options.ModelID != nil {
			model = *options.ModelID
		}
		if options.Dimensions != nil {
			dimensions = options.Dimensions
		}
	}
	if params.Model == "" {
		params.Model = model
		if options != nil && options.ModelID != nil && model == "" {
			if _, overridden := params.Overrides(); !overridden {
				extras := params.ExtraFields()
				if _, present := extras["model"]; !present {
					extras = maps.Clone(extras)
					if extras == nil {
						extras = make(map[string]any)
					}
					extras["model"] = ""
					params.SetExtraFields(extras)
				}
			}
		}
	}
	if param.IsOmitted(params.Dimensions) && dimensions != nil {
		params.Dimensions = openai.Int(int64(*dimensions))
	}
	if params.EncodingFormat == "" {
		params.EncodingFormat = openai.EmbeddingNewParamsEncodingFormatBase64
	}
	params.Input = openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: values}

	telemetry.MarkUsed(telemetry.FeatureOpenAI)
	response, err := g.client.Embeddings.New(ctx, params, telemetryRequestOption(""))
	if err != nil {
		return nil, err
	}
	if response == nil || !response.JSON.Data.Valid() && response.JSON.Data.Raw() != "" {
		return nil, fmt.Errorf("openaiprovider: invalid embedding response data")
	}
	result := &embedding.GeneratedOf[float32]{
		Embeddings: make([]*embedding.Of[float32], len(response.Data)),
	}
	for i, item := range response.Data {
		var index int64
		if raw := item.JSON.Index.Raw(); raw != "" &&
			(!item.JSON.Index.Valid() || json.Unmarshal([]byte(raw), &index) != nil) {
			return nil, fmt.Errorf("openaiprovider: invalid embedding response index %d", item.Index)
		}
		vector, err := openAIEmbeddingVector(item)
		if err != nil {
			return nil, fmt.Errorf("openaiprovider: embedding at position %d: %w", i, err)
		}
		result.Embeddings[i] = &embedding.Of[float32]{
			Vector: vector, ModelID: response.Model, CreatedAt: time.Now().UTC(),
		}
	}
	if response.JSON.Usage.Valid() {
		inputTokens, err := openAIEmbeddingTokenCount(response.Usage.JSON.PromptTokens.Raw())
		if err != nil {
			return nil, fmt.Errorf("openaiprovider: invalid prompt token count: %w", err)
		}
		totalTokens, err := openAIEmbeddingTokenCount(response.Usage.JSON.TotalTokens.Raw())
		if err != nil {
			return nil, fmt.Errorf("openaiprovider: invalid total token count: %w", err)
		}
		result.Usage = &message.UsageDetails{
			InputTokenCount: inputTokens,
			TotalTokenCount: totalTokens,
		}
	} else if raw := response.JSON.Usage.Raw(); raw != "" && raw != "null" {
		return nil, fmt.Errorf("openaiprovider: embedding response has invalid token usage")
	}
	return result, nil
}

func openAIEmbeddingVector(item openai.Embedding) (embedding.VectorOf[float32], error) {
	var vector embedding.VectorOf[float32]
	raw := bytes.TrimSpace([]byte(item.JSON.Embedding.Raw()))
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("missing embedding vector")
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, fmt.Errorf("invalid embedding vector: %w", err)
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 embedding vector: %w", err)
		}
		if len(data)%4 != 0 {
			return nil, fmt.Errorf("base64 embedding vector length must be a multiple of 4")
		}
		vector = make(embedding.VectorOf[float32], len(data)/4)
		for i := range vector {
			vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
		}
	} else {
		var elements []*float32
		if err := json.Unmarshal(raw, &elements); err != nil {
			return nil, fmt.Errorf("invalid embedding vector: %w", err)
		}
		vector = make(embedding.VectorOf[float32], len(elements))
		for i, value := range elements {
			if value == nil {
				return nil, fmt.Errorf("embedding vector contains a null element")
			}
			vector[i] = *value
		}
	}
	return vector, nil
}

func openAIEmbeddingTokenCount(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	var count *int64
	if err := json.Unmarshal([]byte(raw), &count); err != nil {
		return 0, err
	}
	if count == nil {
		return 0, fmt.Errorf("token count must be an integer")
	}
	return *count, nil
}
