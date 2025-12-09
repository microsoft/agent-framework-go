package main

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Chat Agent Image Analysis Example

This sample demonstrates using OpenAI Chat Agent for image analysis and vision tasks,
showing multi-modal content handling with text and images.
*/

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, &chatagent.Options{
		Name:         "VisionAgent",
		Instructions: "You are a helpful agent that can analyze images.",
	})

	resp, err := agent.Run(a,
		agent.WithMessage(message.NewText("Describe the content of this image.")),
		agent.WithMessage(message.New(&message.URIContent{
			URI:       "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
			MediaType: "image/jpeg",
		})),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp)
}
