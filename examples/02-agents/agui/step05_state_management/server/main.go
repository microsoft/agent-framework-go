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

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/aguihosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

func main() {
	stateSnapshotMiddleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
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
							if !yield(&agent.ResponseUpdate{Role: update.Role, Contents: message.Contents{&message.DataContent{MediaType: "application/json", Data: encoded}}}, nil) {
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

	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	a := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model: deployment,
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
			Config: agent.Config{
				Name:        "RecipeAgent",
				Middlewares: []agent.Middleware{stateSnapshotMiddleware},
			},
		},
	)
	mux := http.NewServeMux()
	mux.Handle("/", aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a}))

	log.Printf("AG-UI server listening on %s", ":8888")
	if err := http.ListenAndServe(":8888", mux); err != nil {
		log.Fatal(err)
	}
}
