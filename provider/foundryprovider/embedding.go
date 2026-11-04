// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3/option"
)

// EmbeddingGeneratorConfig configures a Foundry embedding generator.
type EmbeddingGeneratorConfig struct {
	// Model is the default embedding deployment name. It must not be blank.
	Model string
	// Dimensions is the default vector size. Nil uses the service default.
	// Explicit zero is forwarded; supported values depend on the service.
	Dimensions *int
	// OpenAIOptions configure the OpenAI-compatible client, including API-key
	// authentication when no token credential is supplied; see [NewAgent].
	// Foundry-owned endpoint and protocol options take precedence.
	OpenAIOptions []option.RequestOption
}

// NewEmbeddingGenerator creates a text embedding generator backed by a Foundry
// resource. It derives the resource origin from the supplied project or resource
// endpoint, configures its /openai/v1/ route, and delegates
// generation to [openaiprovider.NewEmbeddingGenerator]. Embeddings are resource
// operations, not project operations.
// Request options may override the model and dimensions, including native OpenAI
// parameters through [embedding.Options.RawRepresentationFactory].
//
// The endpoint must be an absolute Foundry URL and config.Model must not be blank;
// invalid arguments panic. When credential is nil, configure API-key authentication
// in [EmbeddingGeneratorConfig.OpenAIOptions] as described by [NewAgent].
// The Azure SDK requires HTTPS for authenticated requests. The generator does not
// own the credential or transport and does not require cleanup.
func NewEmbeddingGenerator(endpoint string, credential azcore.TokenCredential, config EmbeddingGeneratorConfig) embedding.Generator[string, *embedding.Of[float32]] {
	projectURL, err := url.Parse(normalizeAbsoluteEndpoint(endpoint))
	if err != nil {
		panic(err)
	}
	resourceEndpoint := url.URL{Scheme: projectURL.Scheme, Host: projectURL.Host}
	client := NewOpenAIClient(resourceEndpoint.String(), credential, config.OpenAIOptions...)
	model := strings.TrimSpace(config.Model)
	if model == "" {
		panic("model is required")
	}
	return openaiprovider.NewEmbeddingGenerator(client, openaiprovider.EmbeddingGeneratorConfig{
		Model: model, Dimensions: config.Dimensions,
	})
}
