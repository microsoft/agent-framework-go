// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
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

	// ToolAutoCall configures automatic function-tool invocation. When nil, defaults
	// are used.
	ToolAutoCall *toolautocall.Config

	// Instructions are provided to Foundry as system instructions for project Responses API agents.
	// They are ignored for server-side Foundry prompt agents, whose instructions are owned by the service.
	Instructions string

	// DisableStoreOutput disables service-side Responses output storage.
	// Use this when local session history providers own conversation state.
	// By default, store-disabled runs also request `reasoning.encrypted_content`
	// so reasoning items can be replayed statelessly; use
	// [openaiprovider.ResponsesIncludeReasoningEncryptedContent] in RunOptions to
	// opt out.
	DisableStoreOutput bool

	// OpenAIOptions configure the OpenAI-compatible per-agent client. Foundry-owned
	// endpoint, authentication, and protocol options take precedence.
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
	var baseURL, model string
	var targetOptions []option.RequestOption
	instructions := config.Instructions
	switch target := target.(type) {
	case ModelDeployment:
		projectEndpoint := normalizeAbsoluteEndpoint(endpoint)
		model = strings.TrimSpace(string(target))
		if model == "" {
			panic("model is required")
		}
		baseURL = projectOpenAIBaseURL(projectEndpoint)
		// Foundry project endpoints encode the API version in /openai/v1 and
		// reject the api-version query added by azure.WithEndpoint.
		targetOptions = append(targetOptions, option.WithQueryDel("api-version"))
	case ServerAgent:
		projectEndpoint := normalizeAbsoluteEndpoint(endpoint)
		agentName := strings.TrimSpace(string(target))
		if agentName == "" {
			panic("agent name is required")
		}
		agentEndpoint := serverAgentEndpoint(projectEndpoint, agentName)
		baseURL = serverAgentOpenAIBaseURL(agentEndpoint)
		config.ID = cmp.Or(config.ID, agentName)
		config.Name = cmp.Or(config.Name, agentName)
		targetOptions = append(targetOptions, option.WithQueryDel("api-version"))
		targetOptions = append(targetOptions, option.WithQuery("api-version", foundryDataPlaneAPIVersion))
		instructions = ""
	default:
		panic(fmt.Sprintf("unsupported Foundry agent target %T", target))
	}

	openAIOptions := make([]option.RequestOption, 0, len(config.OpenAIOptions)+len(targetOptions)+6)
	openAIOptions = append(openAIOptions, config.OpenAIOptions...)
	openAIOptions = append(openAIOptions,
		// WithTokenCredential requires the Azure endpoint marker registered by
		// WithEndpoint. WithEndpoint also adds ?api-version=v1 and rewrites a
		// Responses path from /responses to /openai/responses.
		azure.WithEndpoint(baseURL, foundryDataPlaneAPIVersion),
		// Keep the complete Foundry OpenAI-compatible route as the request root.
		option.WithBaseURL(baseURL),
		option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			// Undo WithEndpoint's Azure OpenAI path prefix because baseURL already
			// contains the complete Foundry route. Update RawPath as well so escaped
			// server agent names remain encoded.
			req.URL.Path = strings.TrimPrefix(req.URL.Path, "/openai")
			req.URL.RawPath = strings.TrimPrefix(req.URL.RawPath, "/openai")
			return next(req)
		}),
		// Use the Foundry audience while retaining the SDK's token refresh and
		// authenticated-transport protections.
		azure.WithTokenCredential(credential, azure.WithTokenCredentialScopes([]string{azureAIResourceScope})),
	)
	openAIOptions = append(openAIOptions, targetOptions...)
	openAIOptions = append(openAIOptions, clientHeadersRequestOption())
	openAIOptions = append(openAIOptions, hostedAgentUserIdentityRequestOption())
	openAIOptions = append(openAIOptions, hostedAgentSessionRequestOption())
	openAIOptions = append(openAIOptions, servedModelRequestOption())
	config.Middlewares = append([]agent.Middleware{
		clientHeadersMiddleware{},
		hostedAgentUserIdentityMiddleware{},
		hostedAgentSessionMiddleware{},
		servedModelMiddleware{},
	}, config.Middlewares...)

	return openaiprovider.NewResponsesAgent(openai.NewClient(openAIOptions...), openaiprovider.AgentConfig{
		Config:             config.Config,
		ProviderName:       "microsoft.foundry",
		Instructions:       instructions,
		Model:              model,
		DisableStoreOutput: config.DisableStoreOutput,
		ToolAutoCall:       config.ToolAutoCall,
	})
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
