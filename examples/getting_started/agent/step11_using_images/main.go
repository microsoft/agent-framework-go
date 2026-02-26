// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
)

var logger = demo.NewLogger(
	"Using Images",
	"Demonstrates how to use Image Multi-Modality with an Agent.",
	"Model", "gpt-4o-mini",
)

func main() {
	// Create the agent.
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Instructions: "You are a helpful agent that can analyze images.",
			Name:         "VisionAgent",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		},
	})

	ctx := context.Background()
	msg := message.New(
		&message.TextContent{Text: "What do you see in this image?"},
		&message.URIContent{
			URI:       "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
			MediaType: "image/jpeg",
		},
	)

	resp, err := a.RunMessage(msg).Collect(ctx)
	demo.Response(resp, err)
}
