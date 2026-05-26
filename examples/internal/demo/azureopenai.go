// Copyright (c) Microsoft. All rights reserved.

package demo

import (
	"cmp"
	"context"
	"os"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

const (
	DefaultAPIVersion = "2025-01-01-preview"
	DefaultDeployment = "gpt-4o-mini"
)

var (
	Endpoint   = strings.TrimSpace(os.Getenv("AZURE_OPENAI_ENDPOINT"))
	APIVersion = cmp.Or(strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_VERSION")), DefaultAPIVersion)
	Deployment = cmp.Or(strings.TrimSpace(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")), DefaultDeployment)
)

func NewAzureOpenAIClient() openai.Client {
	token := AzureTokenCredential()
	return openai.NewClient(
		azure.WithEndpoint(Endpoint, APIVersion),
		azure.WithTokenCredential(token),
	)
}

func NewAzureChatAgent(name string, instructions string, middlewares ...agent.Middleware) *agent.Agent {
	return openaiagent.NewChatCompletions(
		NewAzureOpenAIClient(),
		openaiagent.Config{
			Model:        Deployment,
			Instructions: instructions,
			Config: agent.Config{
				Name:        name,
				Middlewares: slices.Clone(middlewares),
			},
		},
	)
}

func AgentText(ctx context.Context, a *agent.Agent, prompt string, opts ...agent.Option) (string, error) {
	resp, err := a.RunText(ctx, prompt, opts...).Collect()
	if err != nil {
		return "", err
	}
	return resp.String(), nil
}
