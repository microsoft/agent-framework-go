package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

const topic = "Electric bicycles make city commuting better."

var logger = demo.NewLogger(
	"Multi Service Workflow",
	"This sample coordinates several Azure OpenAI agents with distinct roles in one workflow.",
	"Model", demo.Deployment,
)

func main() {
	wf, err := workflowhosting.BuildSequential("multi-service-workflow", []*agent.Agent{
		demo.NewAzureChatAgent("researcher", "Write a concise three-paragraph overview of the user's topic. Include one claim that should be fact checked.", logger),
		demo.NewAzureChatAgent("fact_checker", "Review the prior essay. Identify supported, questionable, and false claims in concise bullets.", logger),
		demo.NewAzureChatAgent("reporter", "Write a final single-paragraph summary using only claims that survived the fact check.", logger),
	}...)
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	run, err := inproc.Default.RunStreaming(ctx, wf, message.NewText(topic))
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	emitEvents := true
	if err := run.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		demo.Panic(err)
	}

	lastExecutorID := ""
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if update, ok := e.Output.(*agent.ResponseUpdate); ok {
				if e.ExecutorID != lastExecutorID {
					lastExecutorID = e.ExecutorID
					demo.Assistantf("%s", e.ExecutorID)
				}
				if text := update.String(); text != "" {
					demo.Assistantf("%s", text)
				}
			}
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
}
