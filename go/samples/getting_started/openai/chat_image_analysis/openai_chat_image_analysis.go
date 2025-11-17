package main

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Chat Agent Image Analysis Example

This sample demonstrates using OpenAI Chat Agent for image analysis and vision tasks,
showing multi-modal content handling with text and images.
*/

func main() {
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-5-nano",
		Name:               "VisionAgent",
		SystemInstructions: "You are a helpful agent that can analyze images.",
	})

	fmt.Println("Result: ", must(
		ag.Run(nil, &message.Message{Role: message.RoleUser, Contents: []message.Content{
			&message.TextContent{Text: "Describe the content of this image."},
			&message.URIContent{
				URI:       "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
				MediaType: "image/jpeg",
			}}}),
	))
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
