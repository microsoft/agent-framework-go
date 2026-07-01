// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	_ "embed" // Embed import required by go:embed for []byte target
	"encoding/base64"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Foundry Images",
	"Demonstrates image input with a Foundry agent.",
	"Model", demo.FoundryModel,
)

//go:embed assets/walkway.jpg
var walkwayImage []byte

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful agent that can analyze images.",
			Config: agent.Config{
				Name:        "VisionAgent",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	msg := message.New(
		&message.TextContent{Text: "What do you see in this image?"},
		&message.DataContent{
			Name:      "walkway.jpg",
			Data:      base64.StdEncoding.EncodeToString(walkwayImage),
			MediaType: "image/jpeg",
		},
	)

	resp, err := a.RunMessage(context.Background(), msg).Collect()
	demo.Response(resp, err)
}
