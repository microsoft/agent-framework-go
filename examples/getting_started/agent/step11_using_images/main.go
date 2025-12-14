// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to use Image Multi-Modality with an Aagent.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
)

func main() {
	// Create the agent.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Options{
		Instructions: "You are a helpful agent that can analyze images.",
		Name:         "VisionAgent",
	})

	ctx := context.Background()
	msg := message.New(
		&message.TextContent{Text: "What do you see in this image?"},
		&message.URIContent{
			URI:       "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
			MediaType: "image/jpeg",
		},
	)

	for resp, err := range agent.RunStream(ctx, a, agentopt.Message(msg)) {
		if err != nil {
			panic(err)
		}
		fmt.Println(resp)
	}
}
