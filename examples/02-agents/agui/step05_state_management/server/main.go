// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"iter"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/aguihosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

func main() {
	stateSnapshotMiddleware := middleware.Func(func(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			for update, err := range next(ctx, messages, opts...) {
				if err != nil {
					yield(nil, err)
					return
				}
				if update != nil {
					for _, c := range update.Contents {
						text, ok := c.(*message.TextContent)
						if !ok {
							continue
						}
						trimmed := strings.TrimSpace(text.Text)
						if !strings.HasPrefix(trimmed, "{") {
							continue
						}
						var snapshot any
						if json.Unmarshal([]byte(trimmed), &snapshot) == nil {
							encoded := base64.StdEncoding.EncodeToString([]byte(trimmed))
							if !yield(&message.ResponseUpdate{Role: update.Role, Contents: message.Contents{&message.DataContent{MediaType: "application/json", Data: encoded}}}, nil) {
								return
							}
						}
					}
				}
				if !yield(update, nil) {
					return
				}
			}
		}
	})

	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	deployment := cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	apiVersion := cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")

	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Fatal(err)
	}

	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Name: "RecipeAgent",
				Instructions: `You are a helpful recipe assistant. When users ask for recipes, respond with a JSON object in this shape:
{
  "recipe": {
    "title": "...",
    "cuisine": "...",
    "ingredients": ["..."],
    "steps": ["..."],
    "prep_time_minutes": 10,
    "cook_time_minutes": 20,
    "skill_level": "beginner"
  }
}
Then also provide a concise summary in one sentence.`,
				Middlewares: []middleware.Middleware{stateSnapshotMiddleware},
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguihosting.NewHTTPHandler(aguihosting.HandlerConfig{Agent: a}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
