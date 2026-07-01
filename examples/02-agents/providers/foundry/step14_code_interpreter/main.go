// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
)

var logger = demo.NewLogger(
	"Foundry Code Interpreter",
	"Demonstrates the hosted code interpreter tool with a Foundry agent.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that can solve problems with code.",
			Config: agent.Config{
				Name:        "CodeInterpreterAgent",
				Middlewares: []agent.Middleware{logger},
				Tools:       []tool.Tool{&hostedtool.CodeInterpreter{}},
			},
		},
	)

	resp, err := a.RunText(context.Background(), "I need to solve the equation sin(x) + x^2 = 42.").Collect()
	demo.Response(resp, err)
}
