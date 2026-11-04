// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var deployment = cmp.Or(os.Getenv("FOUNDRY_MODEL"), "gpt-5.4-mini")

const policyInstructions = `You specialize in handling queries related to policies and customer communications.

Always reply with exactly this text:

Policy: Short Shipment Dispute Handling Policy V2.1

Summary: "For short shipments reported by customers, first verify internal shipment records
(SAP) and physical logistics scan data (BigQuery). If discrepancy is confirmed and logistics data
shows fewer items packed than invoiced, issue a credit for the missing items. Document the
resolution in SAP CRM and notify the customer via email within 2 business days, referencing the
original invoice and the credit memo number. Use the 'Formal Credit Notification' email
template."`

func main() {
	port := flag.Int("port", 5000, "Port to listen on")
	flag.Parse()

	token := demo.FoundryTokenCredential()

	addr := fmt.Sprintf("localhost:%d", *port)
	url := cmp.Or(os.Getenv("A2A_AGENT_URL"), fmt.Sprintf("http://localhost:%d", *port))

	logger := demo.NewLogger(
		"A2A Server",
		"Hosts the policy agent via A2A HTTP+JSON and JSON-RPC.",
		"Model", deployment,
		"URL", url,
	)

	hostAgent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(deployment),
		foundryprovider.AgentConfig{
			Instructions: policyInstructions,
			Config: agent.Config{
				Name:        "PolicyAgent",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	card := &a2a.AgentCard{
		Name:               "PolicyAgent",
		Description:        "Handles requests relating to policies and customer communications.",
		Version:            "1.0.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{{
			ID:          "id_policy_agent",
			Name:        "PolicyAgent",
			Description: "Handles requests relating to policies and customer communications.",
			Tags:        []string{"policy"},
			Examples:    []string{"What is the policy for short shipments?"},
		}},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(url, a2a.TransportProtocolJSONRPC),
			a2a.NewAgentInterface(url, a2a.TransportProtocolHTTPJSON),
		},
	}
	mux := http.NewServeMux()
	requestHandler := a2aprovider.NewHandler(
		hostAgent, a2aprovider.ExecutorConfig{SessionStore: a2aprovider.NewInMemorySessionStore()},
		a2asrv.WithExtendedAgentCard(card),
	)
	mux.Handle("POST /{$}", a2asrv.NewJSONRPCHandler(requestHandler))
	mux.Handle("/", a2asrv.NewRESTHandler(requestHandler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	demo.Assistantf("A2A policy server listening at %s", url)
	if err := http.ListenAndServe(addr, mux); err != nil {
		demo.Panicf("A2A server failed on %s: %v", addr, err)
	}
}
