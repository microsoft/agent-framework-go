// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
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

var logger = demo.NewLogger(
	"Memory",
	"This sample demonstrates memory using a ContextProvider backed by session state.",
	"Model", deployment,
)

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	a := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You are a friendly assistant.",
			Config: agent.Config{
				Name:             "MemoryAgent",
				Middlewares:      []agent.Middleware{logger}, // for logging agent interactions
				ContextProviders: []*agent.ContextProvider{newUserMemoryProvider()},
			},
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// The provider doesn't know the user yet — it will ask for a name.
	resp, err := a.RunText(ctx, "Hello, what is the square root of 9?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Teach the provider the user's name.
	resp, err = a.RunText(ctx, "My name is Alice", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Subsequent calls are personalized using session state.
	resp, err = a.RunText(ctx, "What is 2 + 2?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Inspect session state to see what the provider stored.
	state := getProviderState(session)
	demo.Assistantf("[Session State] Stored user name: %s", state.UserName)
}

func newUserMemoryProvider() *agent.ContextProvider {
	return &agent.ContextProvider{
		SourceID: userMemorySourceID,
		Provide:  provideUserMemory,
		Store:    storeUserMemory,
	}
}

const userMemorySourceID = "user_memory"

type providerState struct {
	UserName string `json:"user_name,omitempty"`
}

func getProviderState(session agent.Session) providerState {
	if session == nil {
		return providerState{}
	}
	var state providerState
	_, _ = session.Get(userMemorySourceID, &state)
	return state
}

func provideUserMemory(ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	session, _ := agent.GetOption(options, agent.WithSession)
	state := getProviderState(session)
	instructions := "You don't know the user's name yet. Ask for it politely."
	if strings.TrimSpace(state.UserName) != "" {
		instructions = fmt.Sprintf("The user's name is %s. Always address them by name.", state.UserName)
	}
	return messages, append(options, agent.WithInstructions(instructions)), nil
}

func storeUserMemory(ctx context.Context, requestMessages, _ []*message.Message, options ...agent.Option) error {
	session, _ := agent.GetOption(options, agent.WithSession)
	state := getProviderState(session)
	for _, msg := range requestMessages {
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
	session.Set(userMemorySourceID, state)
	return nil
}
