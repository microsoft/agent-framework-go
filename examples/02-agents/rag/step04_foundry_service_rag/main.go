// Copyright (c) Microsoft. All rights reserved.

// This sample uploads a knowledge base and creates a Foundry vector store and
// prompt agent version that uses server-side file search.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

//go:embed contoso-outdoors-knowledge-base.md
var knowledgeBase []byte

var logger = demo.NewLogger(
	"Foundry Service RAG",
	"Demonstrates server-side file search using a Foundry vector store and agent version.",
	"Model", demo.FoundryModel,
)

type serviceResources struct {
	fileID, vectorStoreID, agentName, version string
}

func main() {
	ctx := context.Background()
	client := foundryprovider.NewOpenAIClient(demo.FoundryProjectEndpoint, demo.FoundryTokenCredential())
	resources, err := provision(ctx, client, demo.FoundryProjectEndpoint, demo.FoundryModel)
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		err = errors.Join(err, resources.cleanup(ctx, client, demo.FoundryProjectEndpoint))
		if err != nil {
			demo.Panic(err)
		}
	}()

	params := responses.ResponseNewParams{}
	params.SetExtraFields(map[string]any{
		"agent": map[string]any{"type": "agent_reference", "name": resources.agentName, "version": resources.version},
	})
	a := openaiprovider.NewResponsesAgent(client, openaiprovider.AgentConfig{
		ProviderName: "microsoft.foundry",
		Config: agent.Config{
			ID: resources.agentName + ":" + resources.version, Name: resources.agentName,
			RunOptions:  []agent.Option{openaiprovider.ResponsesNewParams(params)},
			Middlewares: []agent.Middleware{logger},
		},
	})

	session, err := a.CreateSession(ctx)
	if err != nil {
		return
	}
	for i, question := range []struct{ heading, text string }{
		{"Asking about returns", "Hi! I need help understanding the return policy."},
		{"Asking about shipping", "How long does standard shipping usually take?"},
		{"Asking about product care", "What is the best way to maintain the TrailRunner tent fabric?"},
	} {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf(">> %s\n\n", question.heading)
		response, runErr := a.RunText(ctx, question.text, agent.WithSession(session)).Collect()
		if runErr != nil {
			err = runErr
			return
		}
		demo.Response(response, nil)
	}
}

func provision(ctx context.Context, client openai.Client, endpoint, model string) (resources serviceResources, err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, resources.cleanup(ctx, client, endpoint))
		}
	}()
	file, err := client.Files.New(ctx, openai.FileNewParams{
		File:    openai.File(bytes.NewReader(knowledgeBase), "contoso-outdoors-knowledge-base.md", "text/markdown"),
		Purpose: openai.FilePurposeAssistants,
	})
	if err != nil {
		return resources, err
	}
	if file == nil || file.ID == "" {
		return resources, fmt.Errorf("file upload returned no file ID")
	}
	resources.fileID = file.ID
	store, err := client.VectorStores.New(ctx, openai.VectorStoreNewParams{
		Name: openai.String("contoso-outdoors-knowledge-base"), FileIDs: []string{file.ID},
	})
	if err != nil {
		return resources, err
	}
	if store == nil || store.ID == "" {
		return resources, fmt.Errorf("vector store creation returned no store ID")
	}
	resources.vectorStoreID = store.ID
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	body := map[string]any{
		"definition": map[string]any{
			"kind": "prompt", "model": model,
			"instructions": "You are a helpful support specialist for Contoso Outdoors. Answer questions using the provided context and cite the source document when available.",
			"tools": []any{map[string]any{
				"type": "file_search", "vector_store_ids": []string{store.ID},
			}},
		},
	}
	err = client.Post(ctx, "agents/AskContoso/versions", body, &version,
		option.WithBaseURL(strings.TrimRight(endpoint, "/")+"/"),
		option.WithQuery("api-version", "v1"),
	)
	if err != nil {
		return resources, err
	}
	if version.Name == "" {
		return resources, fmt.Errorf("agent version creation returned no agent name")
	}
	resources.agentName, resources.version = version.Name, version.Version
	if strings.TrimSpace(resources.version) == "" {
		resources.version = "latest"
	}
	return resources, nil
}

func (r serviceResources) cleanup(ctx context.Context, client openai.Client, endpoint string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var failures []error
	if r.fileID != "" {
		if _, err := client.Files.Delete(ctx, r.fileID); err != nil {
			failures = append(failures, fmt.Errorf("delete file %s: %w", r.fileID, err))
		}
	}
	if r.vectorStoreID != "" {
		if _, err := client.VectorStores.Delete(ctx, r.vectorStoreID); err != nil {
			failures = append(failures, fmt.Errorf("delete vector store %s: %w", r.vectorStoreID, err))
		}
	}
	if r.agentName != "" {
		err := client.Delete(ctx, "agents/"+r.agentName, nil, nil,
			option.WithBaseURL(strings.TrimRight(endpoint, "/")+"/"),
			option.WithQuery("api-version", "v1"),
		)
		if err != nil {
			failures = append(failures, fmt.Errorf("delete agent %s: %w", r.agentName, err))
		}
	}
	return errors.Join(failures...)
}
