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
	"OpenAI Chat Completions Client",
	"Creates an agent backed explicitly by the OpenAI Chat Completions API.",
	"Model", model,
)

func main() {
	a := openaiprovider.NewChatCompletionsAgent(
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

	ctx := context.Background()
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)

	for update, err := range a.RunText(ctx, "Tell me another joke about a pirate.", agent.Stream(true)) {
		demo.Response(update, err)
	}
}
