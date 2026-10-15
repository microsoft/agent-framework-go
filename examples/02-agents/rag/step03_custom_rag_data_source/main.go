// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to use a text search context provider to add retrieval-augmented
// generation (RAG) to an agent. The mock search function can be replaced with any custom
// search logic that queries an external knowledge source.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/textsearch"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Custom RAG Data Source",
	"Demonstrates retrieval with a custom text search function.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	searchProvider := textsearch.NewContextProvider(mockSearch, textsearch.Config{
		SearchTime:               textsearch.BeforeInvoke,
		RecentMessageMemoryLimit: 6,
	})

	a := foundryprovider.NewAgent(demo.FoundryProjectEndpoint, token, foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful support specialist for Contoso Outdoors. Answer questions using the provided context and cite the source document when available.",
			Config: agent.Config{
				ContextProviders: []agent.ContextProvider{searchProvider},
				Middlewares:      []agent.Middleware{logger},
			},
			DisableStoreOutput: true,
		})

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for i, question := range []struct{ heading, text string }{
		{"Asking about returns", "Hi! I need help understanding the return policy."},
		{"Asking about shipping", "How long does standard shipping usually take?"},
		{"Asking about product care", "What is the best way to maintain the TrailRunner tent fabric?"},
	} {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf(">> %s\n\n", question.heading)
		response, err := a.RunText(ctx, question.text, agent.WithSession(session)).Collect()
		if err != nil {
			demo.Panic(err)
		}
		demo.Response(response, nil)
	}
}

func mockSearch(_ context.Context, query string) ([]textsearch.Result, error) {
	query = strings.ToLower(query)
	var results []textsearch.Result

	if strings.Contains(query, "return") || strings.Contains(query, "refund") {
		results = append(results, textsearch.Result{
			SourceName: "Contoso Outdoors Return Policy",
			SourceLink: "https://contoso.com/policies/returns",
			Text:       "Customers may return any item within 30 days of delivery. Items should be unused and include original packaging. Refunds are issued to the original payment method within 5 business days of inspection.",
		})
	}

	if strings.Contains(query, "shipping") {
		results = append(results, textsearch.Result{
			SourceName: "Contoso Outdoors Shipping Guide",
			SourceLink: "https://contoso.com/help/shipping",
			Text:       "Standard shipping is free on orders over $50 and typically arrives in 3-5 business days within the continental United States. Expedited options are available at checkout.",
		})
	}

	if strings.Contains(query, "tent") || strings.Contains(query, "fabric") {
		results = append(results, textsearch.Result{
			SourceName: "TrailRunner Tent Care Instructions",
			SourceLink: "https://contoso.com/manuals/trailrunner-tent",
			Text:       "Clean the tent fabric with lukewarm water and a non-detergent soap. Allow it to air dry completely before storage and avoid prolonged UV exposure to extend the lifespan of the waterproof coating.",
		})
	}

	return results, nil
}
