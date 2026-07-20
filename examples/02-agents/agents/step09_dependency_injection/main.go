// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to use dependency injection to register an Agent and
// use it from a hosted service with a user input chat loop.
//
// Go favors explicit constructor injection over a runtime DI container: a
// container assembles the shared dependencies once, and each service receives
// what it needs through its constructor instead of reaching for globals. Here a
// container registers a Foundry-backed agent and injects it into a chat service
// that drives a multi-turn conversation.
package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Dependency Injection",
	"Registers an agent in a container and injects it into a hosted chat service.",
	"Model", demo.FoundryModel,
)

// container registers the application's shared dependencies once — the Go
// equivalent of configuring a DI container. Services resolve what they need
// from it instead of constructing their dependencies themselves.
type container struct {
	agent *agent.Agent
}

func newContainer() *container {
	token := demo.FoundryTokenCredential()

	// Register the agent as a shared dependency.
	joker := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)
	return &container{agent: joker}
}

// chatService depends on an agent that is injected through its constructor
// rather than creating one itself. This keeps the service decoupled from how
// the agent is built and makes it easy to substitute a fake in tests.
type chatService struct {
	agent *agent.Agent
}

func newChatService(a *agent.Agent) *chatService {
	return &chatService{agent: a}
}

// run drives a multi-turn chat loop over the injected agent, preserving context
// across turns with a session. A real hosted service would read each prompt
// from user input; here the turns are scripted so the sample is self-contained.
func (s *chatService) run(ctx context.Context, prompts ...string) {
	session, err := s.agent.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for _, prompt := range prompts {
		resp, err := s.agent.RunText(ctx, prompt, agent.WithSession(session)).Collect()
		demo.Response(resp, err)
	}
}

func main() {
	ctx := context.Background()

	// Register dependencies, then resolve and inject the agent into the service.
	c := newContainer()
	service := newChatService(c.agent)

	// Drive the hosted service's chat loop.
	service.run(ctx,
		"Tell me a joke about a pirate.",
		"Now add a twist ending to that joke.",
	)
}
