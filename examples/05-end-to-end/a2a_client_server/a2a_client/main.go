// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"cmp"
	"context"
	"os"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/a2aagent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint     = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion   = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment   = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	agentURLsEnv = cmp.Or(os.Getenv("A2A_AGENT_URLS"), "http://localhost:5000;http://localhost:5001;http://localhost:5002")
)

func main() {
	ctx := context.Background()
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	urls := splitURLs(agentURLsEnv)

	logger := demo.NewLogger(
		"A2A Client",
		"Uses remote A2A agents as tools from a host client agent.",
		"Model", deployment,
		"Agents", strings.Join(urls, ", "),
	)

	tools := make([]tool.Tool, 0, len(urls))
	for _, url := range urls {
		card, err := agentcard.DefaultResolver.Resolve(ctx, url)
		if err != nil {
			demo.Panicf("failed to resolve card from %s: %v", url, err)
		}
		client, err := a2aclient.NewFromCard(ctx, card)
		if err != nil {
			demo.Panicf("failed to create A2A client for %s: %v", url, err)
		}

		remoteAgent := a2aagent.New(
			client,
			a2aagent.Config{
				Config: agent.Config{
					Name:        card.Name,
					Description: card.Description,
				},
			},
		)
		tools = append(tools, agenttool.New(remoteAgent, agenttool.Config{}))
	}

	host := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You specialize in handling user queries and using your tools to provide answers.",
			Config: agent.Config{
				Name:        "HostClient",
				Middlewares: []agent.Middleware{logger},
				Tools:       tools,
			},
		},
	)

	session, err := host.CreateSession(ctx)
	if err != nil {
		demo.Panicf("failed to create host session: %v", err)
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		_, _ = os.Stdout.WriteString("\nUser (:q or quit to exit): ")
		line, err := reader.ReadString('\n')
		if err != nil {
			demo.Panicf("failed to read input: %v", err)
		}
		message := strings.TrimSpace(line)
		if message == "" {
			demo.Assistant("Request cannot be empty.")
			continue
		}
		if message == ":q" || strings.EqualFold(message, "quit") {
			break
		}

		resp, runErr := host.RunText(ctx, message, agent.WithSession(session)).Collect()
		demo.Response(resp, runErr)
	}
}

func splitURLs(raw string) []string {
	parts := strings.Split(raw, ";")
	urls := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	if len(urls) == 0 {
		return []string{"http://localhost:5000", "http://localhost:5001", "http://localhost:5002"}
	}
	return urls
}
