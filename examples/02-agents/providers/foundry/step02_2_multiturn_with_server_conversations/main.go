// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/conversations"
	"github.com/openai/openai-go/v3/option"
)

const azureAIResourceScope = "https://ai.azure.com/.default"

var logger = demo.NewLogger(
	"Foundry Server Conversations",
	"Demonstrates multiturn conversation using a server-side Foundry project conversation.",
	"Model", demo.FoundryModel,
)

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:        "JokerAgent",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	conversationID := createProjectConversation(ctx, demo.FoundryProjectEndpoint, token)
	session, err := a.CreateSession(ctx, agent.WithServiceID(conversationID))
	if err != nil {
		demo.Panic(err)
	}

	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	resp, err = a.RunText(ctx, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	for update, err := range a.RunText(ctx, "Tell me another joke, but about a ninja this time.", agent.WithSession(session), agent.Stream(true)) {
		demo.Response(update, err)
	}
}

// createProjectConversation creates a server-side Foundry project conversation so the demo can use
// service-managed conversation history. Regular apps can create project conversations through the
// OpenAI-compatible project conversations client; this helper uses the OpenAI SDK directly.
// TODO: Move this helper to the Azure SDK once project conversations are supported there.
func createProjectConversation(ctx context.Context, endpoint string, credential azcore.TokenCredential) string {
	client := openai.NewClient(
		option.WithBaseURL(strings.TrimRight(endpoint, "/")+"/openai/v1/"),
		azure.WithTokenCredential(credential, azure.WithTokenCredentialScopes([]string{azureAIResourceScope})),
	)
	conversation, err := client.Conversations.New(
		ctx,
		conversations.ConversationNewParams{},
		option.WithJSONSet("metadata", map[string]any{}),
		option.WithJSONSet("items", []any{}),
	)
	if err != nil {
		demo.Panicf("failed to create project conversation: %v", err)
	}
	if strings.TrimSpace(conversation.ID) == "" {
		demo.Panic("created project conversation did not include an ID")
	}
	return conversation.ID
}
