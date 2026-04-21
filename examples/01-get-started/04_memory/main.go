// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")

var logger = demo.NewLogger(
	"Memory",
	"This sample demonstrates memory using a ContextProvider backed by session state.",
	"Model", deployment,
)

func main() {
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions:     "You are a friendly assistant.",
				Name:             "MemoryAgent",
				Middlewares:      []middleware.Middleware{logger}, // for logging agent interactions
				ContextProviders: []*memory.ContextProvider{newUserMemoryProvider()},
			},
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// The provider doesn't know the user yet — it will ask for a name
	resp, err := a.RunText(ctx, "Hello, what is the square root of 9?", agentopt.Session(session)).Collect()
	demo.Response(resp, err)

	// Teach the provider the user's name.
	resp, err = a.RunText(ctx, "My name is Alice", agentopt.Session(session)).Collect()
	demo.Response(resp, err)

	// Subsequent calls are personalized using session state.
	resp, err = a.RunText(ctx, "What is 2 + 2?", agentopt.Session(session)).Collect()
	demo.Response(resp, err)

	// Inspect session state to see what the provider stored.
	state := getProviderState(session)
	demo.Assistantf("[Session State] Stored user name: %s", state.UserName)
}

func newUserMemoryProvider() *memory.ContextProvider {
	return &memory.ContextProvider{
		SourceID: userMemorySourceID,
		Provide:  provideUserMemory,
		Store:    storeUserMemory,
	}
}

const userMemorySourceID = "user_memory"

type providerState struct {
	UserName string `json:"user_name,omitempty"`
}

func getProviderState(session *memory.Session) providerState {
	if session == nil {
		return providerState{}
	}
	var state providerState
	session.Get(userMemorySourceID, &state)
	return state
}

func provideUserMemory(ctx memory.BeforeRunContext) (memory.Context, error) {
	state := getProviderState(ctx.Session)
	instructions := "You don't know the user's name yet. Ask for it politely."
	if strings.TrimSpace(state.UserName) != "" {
		instructions = fmt.Sprintf("The user's name is %s. Always address them by name.", state.UserName)
	}
	return memory.Context{Messages: []*message.Message{{
		Role: message.RoleSystem,
		Contents: []message.Content{
			&message.TextContent{Text: instructions},
		},
	}}}, nil
}

func storeUserMemory(ctx memory.AfterRunContext) error {
	state := getProviderState(ctx.Session)
	for _, msg := range ctx.RequestMessages {
		if msg == nil || msg.Role != message.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.Contents.Text())
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		idx := strings.Index(lower, "my name is")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(text[idx+len("my name is"):])
		if name == "" {
			continue
		}
		parts := strings.Fields(name)
		if len(parts) == 0 {
			continue
		}
		state.UserName = strings.Trim(parts[0], ".,!?")
		break
	}
	ctx.Session.Set(userMemorySourceID, state)
	return nil
}
