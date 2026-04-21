// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"log"
	"net/http"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/aguihosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
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
	searchRestaurants := functool.MustNew(&functool.Func{
		Name:        "search_restaurants",
		Description: "Search for restaurants in a location.",
	}, func(ctx tool.Context, in restaurantSearchRequest) (restaurantSearchResponse, error) {
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

	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	deployment := cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	apiVersion := cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")

	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Fatal(err)
	}

	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Name:         "AGUIAssistant",
				Instructions: "You are a helpful assistant with access to restaurant information.",
				Tools:        []tool.Tool{searchRestaurants},
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguihosting.NewHTTPHandler(aguihosting.HandlerConfig{Agent: a}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
