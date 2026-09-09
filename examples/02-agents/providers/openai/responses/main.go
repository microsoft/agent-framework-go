// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
)

var model = cmp.Or(strings.TrimSpace(os.Getenv("OPENAI_CHAT_MODEL_NAME")), "gpt-5.4-mini")

var logger = demo.NewLogger(
	"Agent with OpenAI Responses",
	"Demonstrates a simple agent backed by OpenAI Responses.",
	"Model", model,
)

func main() {
	a := openaiprovider.NewResponsesAgent(
		openai.NewClient(),
		openaiprovider.AgentConfig{
			Model:        model,
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	resp, err := a.RunText(context.Background(), "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
