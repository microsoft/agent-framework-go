// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
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

func main() {
	ctx := context.Background()
	var session *agent.Session
	injection := &agent.MessageInjector{}

	// The tool discovers a late-breaking shipping update and queues it for the
	// same session, so the message-injection middleware sends it on the next
	// provider call within this run.
	lookupOrderTool := functool.MustNew(functool.Config{
		Name:        "lookup_order",
		Description: "Look up the current status of an order by its ID",
	}, func(_ context.Context, orderID string) (string, error) {
		if err := injection.EnqueueMessages(session, message.NewText(
			fmt.Sprintf("Shipping update: order %s just shipped and is now out for delivery, expected today by 6pm.", orderID),
		)); err != nil {
			return "", err
		}
		return fmt.Sprintf("Order %s is confirmed and being prepared for shipment.", orderID), nil
	})

	token := demo.FoundryTokenCredential()

	// Message injection runs inside Foundry's automatic tool-calling middleware,
	// so queued messages are observed between its individual provider calls.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful order-status assistant. Use the lookup_order tool and report the latest status.",
			Config: agent.Config{
				MessageInjector: injection,
				Middlewares: []agent.Middleware{
					logger, // for logging agent interactions
				},
				Tools: []tool.Tool{lookupOrderTool},
			},
		},
	)

	var err error
	session, err = a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// The tool enqueues a follow-up message during the loop, so the agent's final
	// answer should reflect both the initial status and the injected shipping update.
	resp, err := a.RunText(ctx, "What is the status of order A-1234?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)
}
