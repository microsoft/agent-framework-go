// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var logger = demo.NewLogger(
	"Agent Workflow Patterns",
	"This sample switches between sequential, concurrent, and group chat agent workflow patterns.",
	"Model", demo.FoundryModel,
)

func main() {
	pattern := cmp.Or(os.Getenv("WORKFLOW_PATTERN"), "sequential")
	wf, err := buildPattern(pattern)
	if err != nil {
		demo.Panic(err)
	}

	if _, err := runWorkflow(context.Background(), wf, []*message.Message{message.NewText("Hello, world!")}); err != nil {
		demo.Panic(err)
	}
}

func runWorkflow(ctx context.Context, wf *workflow.Workflow, messages []*message.Message) ([]*message.Message, error) {
	run, err := inproc.Default.RunStreaming(ctx, wf, messages)
	if err != nil {
		return nil, err
	}
	defer func() { _ = run.Close(ctx) }()

	emitEvents := true
	if err := run.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		return nil, err
	}

	lastExecutorID := ""
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			return nil, err
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if update, ok := e.Output.(*agent.ResponseUpdate); ok {
				if e.ExecutorID != lastExecutorID {
					lastExecutorID = e.ExecutorID
					demo.Assistantf("%s", e.ExecutorID)
				}
				if updateText := update.String(); updateText != "" {
					demo.Assistantf("%s", updateText)
				}
				continue
			}
			if outputMessages, ok := e.Output.([]*message.Message); ok {
				return outputMessages, nil
			}
		case workflow.ErrorEvent:
			return nil, e.Error
		case workflow.ExecutorFailedEvent:
			return nil, fmt.Errorf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
	return nil, nil
}

func buildPattern(pattern string) (*workflow.Workflow, error) {
	agents := []*agent.Agent{
		newTranslationAgent("French"),
		newTranslationAgent("Spanish"),
		newTranslationAgent("English"),
	}

	switch pattern {
	case "sequential":
		return agentworkflow.NewSequentialWorkflowBuilder(agents...).Build()
	case "concurrent":
		return agentworkflow.NewConcurrentWorkflowBuilder(agents...).Build()
	case "groupchat":
		return agentworkflow.NewGroupChatWorkflowBuilder(func(agents []*agent.Agent) *agentworkflow.GroupChatManager {
			return agentworkflow.NewRoundRobinGroupChatManager(agents, agentworkflow.RoundRobinGroupChatOptions{MaximumIterationCount: 5})
		}, agents...).
			WithName("Translation Round Robin Workflow").
			WithDescription("A workflow where three translation agents take turns responding in a round-robin fashion.").
			Build()
	default:
		return nil, fmt.Errorf("unknown WORKFLOW_PATTERN %q; use sequential, concurrent, or groupchat", pattern)
	}
}

func newTranslationAgent(language string) *agent.Agent {
	return foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		demo.FoundryTokenCredential(),
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: fmt.Sprintf("You are a translation assistant who only responds in %s. Respond to any input by outputting the name of the input language and then translating the input to %s.", language, language),
			Config: agent.Config{
				Name:        language,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
}
