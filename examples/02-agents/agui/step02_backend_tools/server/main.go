// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"log"
	"net/http"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
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

	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant with access to restaurant information.",
			Config: agent.Config{
				Name:  "AGUIAssistant",
				Tools: []tool.Tool{searchRestaurants},
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
