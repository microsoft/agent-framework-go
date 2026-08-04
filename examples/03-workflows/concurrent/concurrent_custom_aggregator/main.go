// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Concurrent Workflow With Custom Aggregator",
	"This sample fans a prompt out to several domain experts and folds their answers into a single summary message using a custom fan-in aggregator.",
	"Model", demo.FoundryModel,
)

func main() {
	agents := []*agent.Agent{
		newExpertAgent("Physicist", "physics"),
		newExpertAgent("Chemist", "chemistry"),
		newExpertAgent("Biologist", "biology"),
	}

	// WithAggregator replaces the default aggregator (which keeps each agent's
	// last message unchanged) with a fan-in function that consolidates every
	// agent's final response into one summary message.
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(agents...).
		WithName("Domain Experts With Custom Aggregator").
		WithAggregator(summarize).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	results, err := runWorkflow(context.Background(), wf, []*message.Message{
		message.NewText("Why is the sky blue?"),
	})
	if err != nil {
		demo.Panic(err)
	}

	demo.Assistantf("Aggregated %d message(s):", len(results))
	for _, msg := range results {
		demo.Assistantf("%s", msg.String())
	}
}

// summarize is the custom [agentworkflow.MessageAggregator]. It receives one
// message batch per agent (each batch holding that agent's turn output) and
// folds the last message of every batch into a single assistant summary.
func summarize(_ context.Context, batches [][]*message.Message) []*message.Message {
	var b strings.Builder
	b.WriteString("Combined expert summary:")
	for _, batch := range batches {
		if len(batch) == 0 {
			continue
		}
		last := batch[len(batch)-1]
		fmt.Fprintf(&b, "\n- %s", last.String())
	}
	summary := message.NewText(b.String())
	summary.Role = message.RoleAssistant
	return []*message.Message{summary}
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

	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			return nil, err
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if outputMessages, ok := e.Output.([]*message.Message); ok {
				return outputMessages, nil
			}
		case workflow.ErrorEvent:
			return nil, e.Error
		case workflow.ExecutorFailedEvent:
			return nil, fmt.Errorf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
	return nil, fmt.Errorf("workflow stream ended without producing an output event")
}

func newExpertAgent(name, domain string) *agent.Agent {
	return foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		demo.FoundryTokenCredential(),
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: fmt.Sprintf("You are a %s expert. Answer the question from a %s point of view in a single concise sentence.", domain, domain),
			Config: agent.Config{
				Name: name,
			},
		},
	)
}
