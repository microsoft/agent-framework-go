// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/hosting/aguihosting"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

func main() {
	approveExpense := functool.MustNew(&functool.Func{
		Name:        "approve_expense_report",
		Description: "Approve the expense report.",
	}, func(ctx tool.Context, expenseReportID string) (string, error) {
		return fmt.Sprintf("Expense report %s approved", expenseReportID), nil
	})

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
			Instructions: "You are a helpful assistant in charge of approving expenses.",
			RunOptions: []agentopt.Option{
				agentopt.Tool(tool.ApprovalRequiredFunc(approveExpense)),
			},
		},
	})
	mux := http.NewServeMux()
	mux.Handle("/", aguihosting.NewHandler(aguihosting.HandlerConfig{Agent: a}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
