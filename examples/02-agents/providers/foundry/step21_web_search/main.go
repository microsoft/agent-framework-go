// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
)

var logger = demo.NewLogger(
	"Foundry Web Search",
	"Demonstrates the hosted web search tool with a Foundry agent.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that can use web search for current information.",
			Config: agent.Config{
				Name:        "WebSearchAgent",
				Middlewares: []agent.Middleware{logger},
				Tools:       []tool.Tool{&hostedtool.WebSearch{}},
			},
		},
	)

	resp, err := a.RunText(context.Background(), "What's the weather today in Seattle?").Collect()
	demo.Response(resp, err)
	if resp != nil {
		for content := range resp.Contents() {
			for _, annotation := range content.Header().Annotations {
				citation, ok := annotation.(*message.CitationAnnotation)
				if !ok || citation.URL == "" {
					continue
				}
				demo.Assistant("Citation:")
				if citation.Title != "" {
					demo.Assistant("  Title: " + citation.Title)
				}
				demo.Assistant("  URL: " + citation.URL)
				if citation.Snippet != "" {
					demo.Assistant("  Snippet: " + citation.Snippet)
				}
			}
		}
	}
}
