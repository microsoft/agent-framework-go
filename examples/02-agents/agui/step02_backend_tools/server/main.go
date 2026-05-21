// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/aguihosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

type restaurantSearchRequest struct {
	Location string `json:"location"`
	Cuisine  string `json:"cuisine"`
}

type restaurantInfo struct {
	Name    string  `json:"name"`
	Cuisine string  `json:"cuisine"`
	Rating  float64 `json:"rating"`
	Address string  `json:"address"`
}

type restaurantSearchResponse struct {
	Location string           `json:"location"`
	Cuisine  string           `json:"cuisine"`
	Results  []restaurantInfo `json:"results"`
}

func main() {
	searchRestaurants := functool.MustNew(functool.Config{
		Name:        "search_restaurants",
		Description: "Search for restaurants in a location.",
	}, func(ctx context.Context, in restaurantSearchRequest) (restaurantSearchResponse, error) {
		cuisine := in.Cuisine
		if cuisine == "" || cuisine == "any" {
			cuisine = "Italian"
		}
		return restaurantSearchResponse{
			Location: in.Location,
			Cuisine:  cuisine,
			Results: []restaurantInfo{
				{Name: "The Golden Fork", Cuisine: cuisine, Rating: 4.5, Address: "123 Main St, " + in.Location},
				{Name: "Spice Haven", Cuisine: "Indian", Rating: 4.7, Address: "456 Oak Ave, " + in.Location},
				{Name: "Green Leaf", Cuisine: "Vegetarian", Rating: 4.3, Address: "789 Elm Rd, " + in.Location},
			},
		}, nil
	})

	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	a := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You are a helpful assistant with access to restaurant information.",
			Config: agent.Config{
				Name:  "AGUIAssistant",
				Tools: []tool.Tool{searchRestaurants},
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
