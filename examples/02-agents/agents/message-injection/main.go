// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Message Injection",
	"Demonstrates how a tool can fold late-arriving context into the same run via message injection.",
	"Model", demo.FoundryModel,
)

// lookupOrderTool returns an order status and, while doing so, discovers a
// late-breaking shipping update. Instead of waiting for another user turn, it
// enqueues that update as a user message via the MessageInjector so the model
// sees it on the very next provider call within the same run — even though the
// tool itself did not request another function call.
//
// This mirrors the .NET MessageInjectingChatClient / EnqueueMessages capability
// (ported from dotnet #176), where a tool can inject additional context into the
// in-flight function-calling loop.
var lookupOrderTool = functool.MustNew(functool.Config{
	Name:        "lookup_order",
	Description: "Look up the current status of an order by its ID",
}, func(ctx context.Context, orderID string) (string, error) {
	// Fold late-breaking context into the current run so the model incorporates
	// it in its final answer without requiring a new user turn.
	if mi := toolautocall.MessageInjectorFromContext(ctx); mi != nil {
		mi.EnqueueMessages(message.NewText(
			fmt.Sprintf("Shipping update: order %s just shipped and is now out for delivery, expected today by 6pm.", orderID),
		))
	}
	return fmt.Sprintf("Order %s is confirmed and being prepared for shipment.", orderID), nil
})

func main() {
	token := demo.FoundryTokenCredential()

	// Create a Foundry agent whose automatic function-calling middleware is
	// configured with message injection enabled. Provider constructors leave
	// EnableMessageInjection unset, so we disable the default auto-call
	// middleware and supply our own toolautocall middleware instead.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful order-status assistant. Use the lookup_order tool and report the latest status.",
			Config: agent.Config{
				DisableFuncAutoCall: true, // supply our own auto-call middleware below
				Middlewares: []agent.Middleware{
					logger, // for logging agent interactions
					toolautocall.New(toolautocall.Config{
						EnableMessageInjection: true,
					}),
				},
				Tools: []tool.Tool{lookupOrderTool},
			},
		},
	)

	ctx := context.Background()

	// The tool enqueues a follow-up message during the loop, so the agent's final
	// answer should reflect both the initial status and the injected shipping update.
	resp, err := a.RunText(ctx, "What is the status of order A-1234?").Collect()
	demo.Response(resp, err)
}
