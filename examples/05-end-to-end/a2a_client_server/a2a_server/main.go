// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/a2ahosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

type Product struct {
	Name     string  `json:"name"`
	Quantity int     `json:"quantity"`
	Price    float64 `json:"price"`
}

type Invoice struct {
	TransactionID string    `json:"transactionId"`
	InvoiceID     string    `json:"invoiceId"`
	CompanyName   string    `json:"companyName"`
	InvoiceDate   time.Time `json:"invoiceDate"`
	Products      []Product `json:"products"`
}

type InvoiceQuery struct {
	invoices []Invoice
}

func newInvoiceQuery() *InvoiceQuery {
	r := rand.New(rand.NewSource(42))
	randDate := func() time.Time {
		end := time.Now().UTC()
		start := end.AddDate(0, -2, 0)
		delta := int(end.Sub(start).Hours() / 24)
		return start.AddDate(0, 0, r.Intn(delta+1))
	}
	return &InvoiceQuery{invoices: []Invoice{
		{TransactionID: "TICKET-XYZ987", InvoiceID: "INV789", CompanyName: "Contoso", InvoiceDate: randDate(), Products: []Product{{"T-Shirts", 150, 10}, {"Hats", 200, 15}, {"Glasses", 300, 5}}},
		{TransactionID: "TICKET-XYZ111", InvoiceID: "INV111", CompanyName: "XStore", InvoiceDate: randDate(), Products: []Product{{"T-Shirts", 2500, 12}, {"Hats", 1500, 8}, {"Glasses", 200, 20}}},
		{TransactionID: "TICKET-XYZ222", InvoiceID: "INV222", CompanyName: "Cymbal Direct", InvoiceDate: randDate(), Products: []Product{{"T-Shirts", 1200, 14}, {"Hats", 800, 7}, {"Glasses", 500, 25}}},
		{TransactionID: "TICKET-XYZ333", InvoiceID: "INV333", CompanyName: "Contoso", InvoiceDate: randDate(), Products: []Product{{"T-Shirts", 400, 11}, {"Hats", 600, 15}, {"Glasses", 700, 5}}},
	}}
}

func (q *InvoiceQuery) QueryInvoices(companyName string) []Invoice {
	matches := make([]Invoice, 0)
	for _, inv := range q.invoices {
		if strings.EqualFold(inv.CompanyName, companyName) {
			matches = append(matches, inv)
		}
	}
	return matches
}

func (q *InvoiceQuery) QueryByTransactionID(transactionID string) []Invoice {
	matches := make([]Invoice, 0)
	for _, inv := range q.invoices {
		if strings.EqualFold(inv.TransactionID, transactionID) {
			matches = append(matches, inv)
		}
	}
	return matches
}

func (q *InvoiceQuery) QueryByInvoiceID(invoiceID string) []Invoice {
	matches := make([]Invoice, 0)
	for _, inv := range q.invoices {
		if strings.EqualFold(inv.InvoiceID, invoiceID) {
			matches = append(matches, inv)
		}
	}
	return matches
}

func main() {
	agentType := flag.String("agentType", "invoice", "Agent type: invoice|policy|logistics")
	port := flag.Int("port", 5000, "Port to listen on")
	flag.Parse()

	deployment := cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion := cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	demo.CheckAzureEndpoint(endpoint)

	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		demo.Panicf("failed to create Azure credential: %v", err)
	}

	url := fmt.Sprintf("http://localhost:%d", *port)

	logger := demo.NewLogger(
		"A2A Server",
		"Hosts one specialized agent via A2A JSON-RPC.",
		"AgentType", *agentType,
		"Model", deployment,
		"URL", url,
	)

	cfg, card := buildAgent(*agentType, deployment)
	cfg.Client = openai.NewClient(
		azure.WithEndpoint(endpoint, apiVersion),
		azure.WithTokenCredential(token),
	)
	cfg.Agent.Middlewares = append(cfg.Agent.Middlewares, logger)
	hostAgent := openaichatagent.New(cfg)

	card.URL = url
	card.PreferredTransport = a2a.TransportProtocolJSONRPC
	card.AdditionalInterfaces = []a2a.AgentInterface{{
		Transport: a2a.TransportProtocolJSONRPC,
		URL:       url,
	}}
	mux := http.NewServeMux()
	mux.Handle("/", a2ahosting.NewHTTPHandler(a2ahosting.ExecutorConfig{
		Agent: hostAgent,
	}, a2asrv.WithExtendedAgentCard(card)))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	log.Printf("A2A server listening on :%d for agentType=%s", *port, strings.ToLower(*agentType))
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		demo.Panicf("server failed: %v", err)
	}
}

func buildAgent(agentType, model string) (openaichatagent.Config, *a2a.AgentCard) {
	t := strings.ToUpper(strings.TrimSpace(agentType))
	cfg := openaichatagent.Config{Model: model}
	card := &a2a.AgentCard{
		ProtocolVersion:    "0.3.0",
		Version:            "1.0.0",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities: a2a.AgentCapabilities{
			Streaming: false,
		},
	}

	switch t {
	case "INVOICE":
		q := newInvoiceQuery()
		queryInvoices := functool.MustNew(&functool.Func{Name: "query_invoices", Description: "Retrieves invoices for a company"}, func(_ tool.Context, companyName string) ([]Invoice, error) {
			return q.QueryInvoices(companyName), nil
		})
		queryByTransactionID := functool.MustNew(&functool.Func{Name: "query_by_transaction_id", Description: "Retrieves invoices by transaction id"}, func(_ tool.Context, transactionID string) ([]Invoice, error) {
			return q.QueryByTransactionID(transactionID), nil
		})
		queryByInvoiceID := functool.MustNew(&functool.Func{Name: "query_by_invoice_id", Description: "Retrieves invoices by invoice id"}, func(_ tool.Context, invoiceID string) ([]Invoice, error) {
			return q.QueryByInvoiceID(invoiceID), nil
		})

		cfg.Agent = agent.Config{
			Name:         "InvoiceAgent",
			Description:  "Handles requests relating to invoices.",
			Instructions: "You specialize in handling queries related to invoices.",
			Tools: []tool.Tool{
				queryInvoices,
				queryByTransactionID,
				queryByInvoiceID,
			},
		}
		card.Name = "InvoiceAgent"
		card.Description = "Handles requests relating to invoices."
		card.Skills = []a2a.AgentSkill{{
			ID:          "id_invoice_agent",
			Name:        "InvoiceQuery",
			Description: "Handles requests relating to invoices.",
			Tags:        []string{"invoice", "agent-framework-go"},
			Examples:    []string{"List the latest invoices for Contoso."},
		}}
	case "POLICY":
		cfg.Agent = agent.Config{
			Name:        "PolicyAgent",
			Description: "Handles requests relating to policies and customer communications.",
			Instructions: `You specialize in handling queries related to policies and customer communications.

Always reply with exactly this text:

Policy: Short Shipment Dispute Handling Policy V2.1

Summary: "For short shipments reported by customers, first verify internal shipment records
(SAP) and physical logistics scan data (BigQuery). If discrepancy is confirmed and logistics data
shows fewer items packed than invoiced, issue a credit for the missing items. Document the
resolution in SAP CRM and notify the customer via email within 2 business days, referencing the
original invoice and the credit memo number. Use the 'Formal Credit Notification' email
template."`,
		}
		card.Name = "PolicyAgent"
		card.Description = cfg.Agent.Description
		card.Skills = []a2a.AgentSkill{{
			ID:          "id_policy_agent",
			Name:        "PolicyAgent",
			Description: cfg.Agent.Description,
			Tags:        []string{"policy", "agent-framework-go"},
			Examples:    []string{"What is the policy for short shipments?"},
		}}
	case "LOGISTICS":
		cfg.Agent = agent.Config{
			Name:        "LogisticsAgent",
			Description: "Handles requests relating to logistics.",
			Instructions: `You specialize in handling queries related to logistics.

Always reply with exactly:

Shipment number: SHPMT-SAP-001
Item: TSHIRT-RED-L
Quantity: 900`,
		}
		card.Name = "LogisticsAgent"
		card.Description = cfg.Agent.Description
		card.Skills = []a2a.AgentSkill{{
			ID:          "id_logistics_agent",
			Name:        "LogisticsQuery",
			Description: cfg.Agent.Description,
			Tags:        []string{"logistics", "agent-framework-go"},
			Examples:    []string{"What is the status for SHPMT-SAP-001"},
		}}
	default:
		demo.Panicf("unsupported --agentType: %s (expected invoice|policy|logistics)", agentType)
	}

	return cfg, card

}
