// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/aguihosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

func main() {
	approveExpense := functool.MustNew(functool.Config{
		Name:        "approve_expense_report",
		Description: "Approve the expense report.",
	}, func(ctx tool.Context, expenseReportID string) (string, error) {
		return fmt.Sprintf("Expense report %s approved", expenseReportID), nil
	})

	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	a := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You are a helpful assistant in charge of approving expenses.",
			Config: agent.Config{
				Name:  "AGUIAssistant",
				Tools: []tool.Tool{tool.ApprovalRequiredFunc(approveExpense)},
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
