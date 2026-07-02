// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const (
	addr      = ":8888"
	serverURL = "http://localhost:8888"
)

var _ = demo.NewLogger(
	"AG-UI Human-in-the-Loop Server",
	"Serves a Foundry agent with approval-required tools through the AG-UI JSON HTTP handler.",
	"Model", demo.FoundryModel,
	"URL", serverURL,
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

	demo.Assistantf("AG-UI server listening at %s", serverURL)
	demo.Assistantf("Run the matching client with AGUI_SERVER_URL=%s if needed.", serverURL)
	if err := http.ListenAndServe(addr, mux); err != nil {
		demo.Panicf("AG-UI server failed on %s: %v", addr, err)
	}
}
