// Copyright (c) Microsoft. All rights reserved.

package skillhelpers

import (
	"cmp"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var Deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-5.4-mini")
var Endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var APIVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")

// MustNewChatAgent creates an Azure OpenAI-backed chat agent configured for the skills samples.
func MustNewChatAgent(name, instructions string, logger middleware.Middleware, providers ...*memory.ContextProvider) *agent.Agent {
	demo.CheckAzureEndpoint(Endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	middlewares := []middleware.Middleware{}
	if logger != nil {
		middlewares = append(middlewares, logger)
	}

	return openaichatagent.New(openaichatagent.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(Endpoint, APIVersion),
			azure.WithTokenCredential(token),
		),
		Model: Deployment,
		Agent: agent.Config{
			Name:             name,
			Instructions:     instructions,
			Middlewares:      middlewares,
			ContextProviders: providers,
		},
	})
}
