// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const model = "o4-mini"

var logger = demo.NewLogger(
	"Reasoning Run",
	"Demonstrates an OpenAI Responses agent using an o-series reasoning model.",
	"Model", model,
)

func main() {
	// Create an OpenAI Responses agent backed by an o-series reasoning model.
	a := openaiprovider.NewAgent(
		openai.NewClient(),
		openaiprovider.AgentConfig{
			Model:        model,
			Instructions: "You are a careful problem solver. Think step by step before answering.",
			Config: agent.Config{
				Name:        "Reasoner",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Ask the model to reason about the request and to emit a reasoning summary.
	// Reasoning effort and summary are set per run via the ResponsesNewParams
	// escape hatch, which forwards custom parameters to the OpenAI Responses API.
	resp, err := a.RunText(context.Background(),
		"A farmer has chickens and rabbits. Together they have 35 heads and 94 legs. "+
			"How many of each animal does the farmer have?",
		openaiprovider.ResponsesNewParams(responses.ResponseNewParams{
			Reasoning: shared.ReasoningParam{
				Effort:  shared.ReasoningEffortMedium,
				Summary: shared.ReasoningSummaryAuto,
			},
		}),
	).Collect()
	if err != nil {
		demo.Response(resp, err)
		return
	}

	// The logger middleware already emitted the "Assistant:" label (without a
	// trailing newline) in anticipation of the model's output, so print the
	// final answer first to keep that label attached to it. The Responses API
	// also surfaces reasoning summaries as TextReasoningContent, separate from
	// the final answer carried in TextContent; print those as their own block.
	demo.Response(resp, err)

	for content := range resp.Contents() {
		if reasoning, ok := content.(*message.TextReasoningContent); ok && reasoning.Text != "" {
			demo.Assistantf("[reasoning] %s", reasoning.Text)
		}
	}
}
