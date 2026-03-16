// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"log"
	"net/http"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/hosting/aguihosting"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

func main() {
	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	deployment := cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	apiVersion := cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")

	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Fatal(err)
	}

	a := openaichatagent.New(openaichatagent.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		Model: deployment,
		Agent: agent.Config{
			Name:         "AGUIAssistant",
			Instructions: "You are a helpful assistant.",
		},
	})
	mux := http.NewServeMux()
	mux.Handle("/", aguihosting.NewHTTPHandler(aguihosting.HandlerConfig{Agent: a}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
