// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Using Images",
	"Demonstrates how to use Image Multi-Modality with an Agent.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	// Create Microsoft Foundry agent.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful agent that can analyze images.",
			Config: agent.Config{
				Name:        "VisionAgent",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()
	msg := message.New(
		&message.TextContent{Text: "What do you see in this image?"},
		&message.URIContent{
			URI:       "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
			MediaType: "image/jpeg",
		},
	)

	resp, err := a.RunMessage(ctx, msg).Collect()
	demo.Response(resp, err)
}
