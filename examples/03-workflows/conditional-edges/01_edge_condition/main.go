// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Edge Condition Workflow",
	"This sample routes email through conditional workflow edges.",
)

type DetectionResult struct {
	Email  string
	IsSpam bool
	Reason string
}

func main() {
	detect := workflow.BindFunc("SpamDetectionExecutor", func(email string) DetectionResult {
		lower := strings.ToLower(email)
		spam := strings.Contains(lower, "wire transfer") || strings.Contains(lower, "prize")
		return DetectionResult{Email: email, IsSpam: spam, Reason: "matched suspicious wording"}
	})
	assistant := workflow.BindFunc("EmailAssistantExecutor", func(result DetectionResult) string {
		return "Draft response: Thanks for your note. I will follow up shortly."
	})
	send := workflow.BindFunc("SendEmailExecutor", func(response string) string {
		return "Email sent: " + response
	})
	spam := workflow.BindFunc("HandleSpamExecutor", func(result DetectionResult) string {
		return "Email marked as spam: " + result.Reason
	})

	wf, err := workflow.NewBuilder(detect).
		AddDirectEdge(detect, assistant, false, func(msg any) bool { return !msg.(DetectionResult).IsSpam }).
		AddEdge(assistant, send).
		AddDirectEdge(detect, spam, false, func(msg any) bool { return msg.(DetectionResult).IsSpam }).
		WithOutputFrom(send, spam).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "Congratulations, you won a prize. Send a wire transfer fee.")
	if err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}
