// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

const (
	azureAIResourceScope       = "https://ai.azure.com/.default"
	foundryDataPlaneAPIVersion = "v1"
)

// AgentConfig contains configuration for Foundry-backed agents.
type AgentConfig struct {
	agent.Config

	// Instructions are provided to Foundry as system instructions for project Responses API agents.
	// They are ignored for server-side Foundry prompt agents, whose instructions are owned by the service.
	Instructions string

	// DisableStoreOutput disables service-side Responses output storage.
	// Use this when local session history providers own conversation state.
	DisableStoreOutput bool

	// OpenAIOptions are appended to the OpenAI-compatible per-agent client options.
	OpenAIOptions []option.RequestOption
}

// AgentTarget identifies which Foundry agent mode [NewAgent] should use.
type AgentTarget interface {
	foundryAgentTarget()
}

// ModelDeployment identifies project Responses API mode for [NewAgent].
type ModelDeployment string

func (ModelDeployment) foundryAgentTarget() {}

// ServerAgent identifies an existing server-side Foundry agent by name for [NewAgent].
type ServerAgent string

func (ServerAgent) foundryAgentTarget() {}

// NewAgent creates an [agent.Agent] backed by Microsoft Foundry.
//
// The endpoint must be the Foundry project endpoint. When target is [ServerAgent],
// NewAgent builds the server-side agent endpoint from the project endpoint and agent name.
func NewAgent(endpoint string, credential azcore.TokenCredential, target AgentTarget, config AgentConfig) *agent.Agent {
	if credential == nil {
		panic("credential is required")
	}
	switch target := target.(type) {
	case ModelDeployment:
		projectEndpoint := normalizeAbsoluteEndpoint(endpoint)
		model := strings.TrimSpace(string(target))
		if model == "" {
			panic("model is required")
		}
		return newFoundryAgent(credential, config, foundryAgentMode{
			baseURL: projectOpenAIBaseURL(projectEndpoint),
			model:   model,
		})
	case ServerAgent:
		projectEndpoint := normalizeAbsoluteEndpoint(endpoint)
		agentName := strings.TrimSpace(string(target))
		if agentName == "" {
			panic("agent name is required")
		}
		agentEndpoint := serverAgentEndpoint(projectEndpoint, agentName)
		return newFoundryAgent(credential, config, foundryAgentMode{
			baseURL:        serverAgentOpenAIBaseURL(agentEndpoint),
			agentName:      agentName,
			requestOptions: []option.RequestOption{option.WithQuery("api-version", foundryDataPlaneAPIVersion)},
		})
	default:
		panic(fmt.Sprintf("unsupported Foundry agent target %T", target))
	}
}

func serverAgentOpenAIBaseURL(agentEndpoint string) string {
	return strings.TrimRight(agentEndpoint, "/") + "/"
}

func projectOpenAIBaseURL(projectEndpoint string) string {
	return strings.TrimRight(projectEndpoint, "/") + "/openai/v1/"
}

func serverAgentEndpoint(projectEndpoint string, agentName string) string {
	endpoint, err := url.JoinPath(projectEndpoint, "agents", url.PathEscape(agentName), "endpoint", "protocols", "openai")
	if err != nil {
		panic(fmt.Sprintf("invalid project endpoint %q: %v", projectEndpoint, err))
	}
	return endpoint
}

type foundryAgentMode struct {
	baseURL        string
	agentName      string
	model          string
	requestOptions []option.RequestOption
}

func newFoundryAgent(credential azcore.TokenCredential, config AgentConfig, mode foundryAgentMode) *agent.Agent {
	if config.ID == "" {
		config.ID = mode.agentName
	}
	if config.Name == "" {
		config.Name = mode.agentName
	}
	instructions := config.Instructions
	if mode.agentName != "" {
		instructions = ""
	}
	openAIOptions := []option.RequestOption{
		option.WithBaseURL(mode.baseURL),
		azure.WithTokenCredential(credential, azure.WithTokenCredentialScopes([]string{azureAIResourceScope})),
	}
	openAIOptions = append(openAIOptions, mode.requestOptions...)
	openAIOptions = append(openAIOptions, config.OpenAIOptions...)
	openAIOptions = append(openAIOptions, clientHeadersRequestOption())
	openAIOptions = append(openAIOptions, servedModelRequestOption())
	config.Middlewares = append([]agent.Middleware{clientHeadersMiddleware{}, servedModelMiddleware{}}, config.Middlewares...)

	return openaiprovider.NewResponsesAgent(openai.NewClient(openAIOptions...), openaiprovider.AgentConfig{
		Config:             config.Config,
		Instructions:       instructions,
		Model:              mode.model,
		DisableStoreOutput: config.DisableStoreOutput,
	})
}

func normalizeAbsoluteEndpoint(rawEndpoint string) string {
	rawEndpoint = strings.TrimSpace(rawEndpoint)
	if rawEndpoint == "" {
		panic("endpoint is required")
	}
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil {
		panic(fmt.Sprintf("invalid endpoint %q: %v", rawEndpoint, err))
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		panic("endpoint must be an absolute URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	endpoint.RawPath = strings.TrimRight(endpoint.EscapedPath(), "/")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String()
}
