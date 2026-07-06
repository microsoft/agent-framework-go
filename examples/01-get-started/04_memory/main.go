// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Memory",
	"This sample demonstrates a custom memory ContextProvider backed by session state.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a friendly assistant.",
			Config: agent.Config{
				Name:             "MemoryAgent",
				Middlewares:      []agent.Middleware{logger}, // for logging agent interactions
				ContextProviders: []agent.ContextProvider{newUserMemoryProvider()},
			},
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	fmt.Println(">> Use session with blank memory")
	fmt.Println()

	resp, err := a.RunText(ctx, "Hello, what is the square root of 9?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	resp, err = a.RunText(ctx, "My name is Ruaidhrí", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	resp, err = a.RunText(ctx, "I am 20 years old", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	data, err := json.Marshal(session)
	if err != nil {
		demo.Panic(err)
	}

	fmt.Println(">> Use deserialized session with previously created memories")
	fmt.Println()

	var deserializedSession agent.Session
	if err := json.Unmarshal(data, &deserializedSession); err != nil {
		demo.Panic(err)
	}
	resp, err = a.RunText(ctx, "What is my name and age?", agent.WithSession(&deserializedSession)).Collect()
	demo.Response(resp, err)

	fmt.Println(">> Read memories using memory component")
	fmt.Println()

	state := getProviderState(&deserializedSession)
	fmt.Printf("MEMORY - User Name: %s\n", state.UserName)
	fmt.Printf("MEMORY - User Age: %d\n", state.UserAge)

	fmt.Println()
	fmt.Println(">> Use new session with previously created memories")
	fmt.Println()

	newSession, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	newSession.Set(userMemorySourceID, state)
	resp, err = a.RunText(ctx, "What is my name and age?", agent.WithSession(newSession)).Collect()
	demo.Response(resp, err)
}

func newUserMemoryProvider() agent.ContextProvider {
	return agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: userMemorySourceID,
		Provide:  provideUserMemory,
		Store:    storeUserMemory,
	})
}

const userMemorySourceID = "user_memory"

type providerState struct {
	UserName string `json:"user_name,omitempty"`
	UserAge  int    `json:"user_age,omitempty"`
}

func getProviderState(session *agent.Session) providerState {
	if session == nil {
		return providerState{}
	}
	var state providerState
	_, _ = session.Get(userMemorySourceID, &state)
	return state
}

func provideUserMemory(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	state := getProviderState(session)
	var instructions strings.Builder
	if strings.TrimSpace(state.UserName) != "" {
		fmt.Fprintf(&instructions, "The user's name is %s.\n", state.UserName)
	} else {
		instructions.WriteString("Ask the user for their name and politely decline to answer any questions until they provide it.\n")
	}
	if state.UserAge > 0 {
		fmt.Fprintf(&instructions, "The user's age is %d.\n", state.UserAge)
	} else {
		instructions.WriteString("Ask the user for their age and politely decline to answer any questions until they provide it.\n")
	}
	return nil, []agent.Option{agent.WithInstructions(instructions.String())}, nil
}

func storeUserMemory(ctx context.Context, invoked agent.InvokedContext) error {
	session, _ := agent.GetOption(invoked.Options, agent.WithSession)
	state := getProviderState(session)
	for _, msg := range invoked.RequestMessages {
		if msg == nil || msg.Role != message.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.Contents.Text())
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		if state.UserName == "" {
			if name, ok := extractName(text, lower); ok {
				state.UserName = name
			}
		}
		if state.UserAge == 0 {
			if age, ok := extractAge(lower); ok {
				state.UserAge = age
			}
		}
	}
	session.Set(userMemorySourceID, state)
	return nil
}

func extractName(text, lower string) (string, bool) {
	idx := strings.Index(lower, "my name is")
	if idx < 0 {
		return "", false
	}
	name := strings.TrimSpace(text[idx+len("my name is"):])
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "", false
	}
	return strings.Trim(parts[0], ".,!?"), true
}

func extractAge(lower string) (int, bool) {
	fields := strings.Fields(lower)
	for i, field := range fields {
		value, err := strconv.Atoi(strings.Trim(field, ".,!?"))
		if err != nil || value <= 0 {
			continue
		}
		if i >= 2 && fields[i-2] == "i" && fields[i-1] == "am" && followedByYear(fields, i) {
			return value, true
		}
		if i >= 1 && fields[i-1] == "i'm" && followedByYear(fields, i) {
			return value, true
		}
		if i >= 3 && fields[i-3] == "my" && fields[i-2] == "age" && fields[i-1] == "is" {
			return value, true
		}
	}
	return 0, false
}

func followedByYear(fields []string, numberIndex int) bool {
	if numberIndex+1 >= len(fields) {
		return false
	}
	next := strings.Trim(fields[numberIndex+1], ".,!? ")
	return next == "year" || next == "years"
}
