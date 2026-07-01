// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var deployment = demo.FoundryModel

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

	token := demo.FoundryTokenCredential()

	url := fmt.Sprintf("http://localhost:%d", *port)

	logger := demo.NewLogger(
		"A2A Server",
		"Hosts one specialized agent via A2A JSON-RPC.",
		"AgentType", *agentType,
		"Model", deployment,
		"URL", url,
	)

	cfg, card := buildAgent(*agentType, deployment)
	cfg.Middlewares = append(cfg.Middlewares, logger)
	hostAgent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(deployment),
		cfg,
	)

	card.SupportedInterfaces = []*a2a.AgentInterface{
		a2a.NewAgentInterface(url, a2a.TransportProtocolJSONRPC),
	}
	mux := http.NewServeMux()
	requestHandler := a2asrv.NewHandler(
		a2aprovider.NewExecutor(hostAgent, a2aprovider.ExecutorConfig{}),
		a2asrv.WithExtendedAgentCard(card),
	)
	mux.Handle("/", a2asrv.NewJSONRPCHandler(requestHandler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	log.Printf("A2A server listening on :%d for agentType=%s", *port, strings.ToLower(*agentType))
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		demo.Panicf("server failed: %v", err)
	}
}

func buildAgent(agentType, model string) (foundryprovider.AgentConfig, *a2a.AgentCard) {
	t := strings.ToUpper(strings.TrimSpace(agentType))
	cfg := foundryprovider.AgentConfig{}
	card := &a2a.AgentCard{
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
		queryInvoices := functool.MustNew(functool.Config{Name: "query_invoices", Description: "Retrieves invoices for a company"}, func(_ context.Context, companyName string) ([]Invoice, error) {
			return q.QueryInvoices(companyName), nil
		})
		queryByTransactionID := functool.MustNew(functool.Config{Name: "query_by_transaction_id", Description: "Retrieves invoices by transaction id"}, func(_ context.Context, transactionID string) ([]Invoice, error) {
			return q.QueryByTransactionID(transactionID), nil
		})
		queryByInvoiceID := functool.MustNew(functool.Config{Name: "query_by_invoice_id", Description: "Retrieves invoices by invoice id"}, func(_ context.Context, invoiceID string) ([]Invoice, error) {
			return q.QueryByInvoiceID(invoiceID), nil
		})

		cfg.Instructions = "You specialize in handling queries related to invoices."
		cfg.Config = agent.Config{
			Name:        "InvoiceAgent",
			Description: "Handles requests relating to invoices.",
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
		cfg.Instructions = `You specialize in handling queries related to policies and customer communications.

Always reply with exactly this text:

Policy: Short Shipment Dispute Handling Policy V2.1

Summary: "For short shipments reported by customers, first verify internal shipment records
(SAP) and physical logistics scan data (BigQuery). If discrepancy is confirmed and logistics data
shows fewer items packed than invoiced, issue a credit for the missing items. Document the
resolution in SAP CRM and notify the customer via email within 2 business days, referencing the
original invoice and the credit memo number. Use the 'Formal Credit Notification' email
template."`
		cfg.Config = agent.Config{
			Name:        "PolicyAgent",
			Description: "Handles requests relating to policies and customer communications.",
		}
		card.Name = "PolicyAgent"
		card.Description = cfg.Description
		card.Skills = []a2a.AgentSkill{{
			ID:          "id_policy_agent",
			Name:        "PolicyAgent",
			Description: cfg.Description,
			Tags:        []string{"policy", "agent-framework-go"},
			Examples:    []string{"What is the policy for short shipments?"},
		}}
	case "LOGISTICS":
		cfg.Instructions = `You specialize in handling queries related to logistics.

Always reply with exactly:

Shipment number: SHPMT-SAP-001
Item: TSHIRT-RED-L
Quantity: 900`
		cfg.Config = agent.Config{
			Name:        "LogisticsAgent",
			Description: "Handles requests relating to logistics.",
		}
		card.Name = "LogisticsAgent"
		card.Description = cfg.Description
		card.Skills = []a2a.AgentSkill{{
			ID:          "id_logistics_agent",
			Name:        "LogisticsQuery",
			Description: cfg.Description,
			Tags:        []string{"logistics", "agent-framework-go"},
			Examples:    []string{"What is the status for SHPMT-SAP-001"},
		}}
	default:
		demo.Panicf("unsupported --agentType: %s (expected invoice|policy|logistics)", agentType)
	}

	return cfg, card
}
