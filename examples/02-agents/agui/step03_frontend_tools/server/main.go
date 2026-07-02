// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"net/http"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

const (
	addr      = ":8888"
	serverURL = "http://localhost:8888"
)

var _ = demo.NewLogger(
	"AG-UI Frontend Tools Server",
	"Serves a Foundry agent that delegates tool calls to the AG-UI client.",
	"Model", demo.FoundryModel,
	"URL", serverURL,
)

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant.",
			Config: agent.Config{
				Name:                "AGUIAssistant",
				DisableFuncAutoCall: true,
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{}))

	demo.Assistantf("AG-UI server listening at %s", serverURL)
	demo.Assistantf("Run the matching client with AGUI_SERVER_URL=%s if needed.", serverURL)
	if err := http.ListenAndServe(addr, mux); err != nil {
		demo.Panicf("AG-UI server failed on %s: %v", addr, err)
	}
}
