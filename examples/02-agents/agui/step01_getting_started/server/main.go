// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"log"
	"net/http"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
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
				Name: "AGUIAssistant",
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
