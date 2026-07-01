// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func main() {
	approveExpense := functool.MustNew(functool.Config{
		Name:        "approve_expense_report",
		Description: "Approve the expense report.",
	}, func(ctx context.Context, expenseReportID string) (string, error) {
		return fmt.Sprintf("Expense report %s approved", expenseReportID), nil
	})

	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant in charge of approving expenses.",
			Config: agent.Config{
				Name:  "AGUIAssistant",
				Tools: []tool.Tool{tool.ApprovalRequiredFunc(approveExpense)},
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
