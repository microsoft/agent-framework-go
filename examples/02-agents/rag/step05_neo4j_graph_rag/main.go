// Copyright (c) Microsoft. All rights reserved.

// This sample retrieves graph-enriched text from a Neo4j full-text index.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

const retrievalQuery = `MATCH (node)-[:FROM_DOCUMENT]->(doc:Document)<-[:FILED]-(company:Company)
OPTIONAL MATCH (company)-[:FACES_RISK]->(risk:RiskFactor)
WITH node, score, company, doc, collect(DISTINCT risk.name)[0..5] AS risks
OPTIONAL MATCH (company)-[:MENTIONS]->(product:Product)
WITH node, score, company, doc, risks, collect(DISTINCT product.name)[0..5] AS products
RETURN
    node.text AS text,
    score,
    company.name AS company,
    company.ticker AS ticker,
    doc.title AS title,
    risks,
    products
ORDER BY score DESC`

var logger = demo.NewLogger(
	"Neo4j Graph RAG",
	"Demonstrates full-text retrieval enriched with knowledge graph context.",
	"Model", demo.FoundryModel,
)

func main() {
	ctx := context.Background()
	uri, password := os.Getenv("NEO4J_URI"), os.Getenv("NEO4J_PASSWORD")
	if uri == "" || password == "" {
		demo.Panicf("NEO4J_URI and NEO4J_PASSWORD must be set")
	}
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(cmp.Or(os.Getenv("NEO4J_USERNAME"), "neo4j"), password, ""))
	if err != nil {
		demo.Panic(err)
	}
	defer func() { err = errors.Join(err, driver.Close(context.WithoutCancel(ctx))) }()
	if err := driver.VerifyConnectivity(ctx); err != nil {
		demo.Panic(err)
	}
	provider := newGraphContextProvider(func(ctx context.Context, query string, parameters map[string]any) ([]*neo4j.Record, error) {
		result, err := neo4j.ExecuteQuery(ctx, driver, query, parameters,
			neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithReadersRouting())
		if err != nil {
			return nil, err
		}
		return result.Records, nil
	}, cmp.Or(os.Getenv("NEO4J_FULLTEXT_INDEX_NAME"), "search_chunks"))

	a := foundryprovider.NewAgent(demo.FoundryProjectEndpoint, demo.FoundryTokenCredential(),
		foundryprovider.ModelDeployment(demo.FoundryModel), foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that answers questions using Neo4j graph context.",
			Config: agent.Config{
				ContextProviders: []agent.ContextProvider{provider},
				Middlewares:      []agent.Middleware{logger},
			},
			DisableStoreOutput: true,
		})

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for i, question := range []string{
		"What products does Microsoft offer?",
		"What risks does Apple face?",
		"Tell me about NVIDIA's AI business and risk factors.",
	} {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf(">> %s\n\n", question)
		response, err := a.RunText(ctx, question, agent.WithSession(session)).Collect()
		if err != nil {
			demo.Panic(err)
		}
		demo.Response(response, nil)
	}
}
