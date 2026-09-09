// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
	"github.com/openai/openai-go/v3"
)

// vectorStoreID identifies the OpenAI vector store the file-search tool queries.
// Create one via the OpenAI dashboard or API, upload your documents to it, then
// export its identifier before running this example:
//
//	export VECTOR_STORE_ID=vs_...
var vectorStoreID = strings.TrimSpace(os.Getenv("VECTOR_STORE_ID"))

var logger = demo.NewLogger(
	"OpenAI File Search",
	"Demonstrates the hosted file-search (vector store) tool with an OpenAI Responses agent.",
	"Model", "gpt-4o-mini",
)

func main() {
	if vectorStoreID == "" {
		demo.Assistant("Set VECTOR_STORE_ID to a vector store you own to run this example.")
		demo.Assistant("  export VECTOR_STORE_ID=vs_...")
		return
	}

	// Create an OpenAI Responses agent with the hosted file-search tool. The tool
	// retrieves grounding context from the referenced vector store and caps the
	// number of returned chunks with MaximumResultCount.
	a := openaiprovider.NewAgent(
		openai.NewClient(),
		openaiprovider.AgentConfig{
			Model:        "gpt-4o-mini",
			Instructions: "You are a helpful assistant. Answer using the documents in the vector store.",
			Config: agent.Config{
				Name:        "FileSearchAgent",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
				Tools: []tool.Tool{
					&hostedtool.FileSearch{
						MaximumResultCount: 5,
						Inputs: []message.Content{
							&message.HostedVectorStoreContent{VectorStoreID: vectorStoreID},
						},
					},
				},
			},
		},
	)

	// Invoke the agent with a retrieval-style query and output the text result.
	resp, err := a.RunText(context.Background(), "What do the documents say? Summarize the key points.").Collect()
	demo.Response(resp, err)
}
