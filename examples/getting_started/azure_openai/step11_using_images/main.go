// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiKey = os.Getenv("AZURE_OPENAI_API_KEY")

var logger = demo.NewLogger(
	"Using Images",
	"Demonstrates how to use Image Multi-Modality with an Agent.",
	"Model", deployment,
)

func main() {
	// Create Azure OpenAI agent.
	a := openai.NewChatAgentAzure(openai.ClientConfig{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Config{
		Instructions: "You are a helpful agent that can analyze images.",
		Name:         "VisionAgent",
		RunOptions:   []agentopt.RunOption{middleware.With(logger)}, // for logging agent interactions
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
