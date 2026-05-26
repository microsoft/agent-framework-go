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
	"Switch Case Workflow",
	"This sample routes email with switch-style workflow cases.",
)

type SpamDecision int

const (
	NotSpam SpamDecision = iota
	Spam
	Uncertain
)

type DetectionResult struct {
	Email    string
	Decision SpamDecision
	Reason   string
}

func main() {
	detect := workflow.NewExecutor("SpamDetectionExecutor", func(email string) DetectionResult {
		return detectEmail(email)
	}).Bind()

	assistant := workflow.NewExecutor("EmailAssistantExecutor", func(result DetectionResult) string {
		return "Draft response for: " + result.Email
	}).Bind()

	send := workflow.NewExecutor("SendEmailExecutor", func(response string) string { return "Email sent: " + response }).Bind()
	spam := workflow.NewExecutor("HandleSpamExecutor", func(result DetectionResult) string {
		return "Email marked as spam: " + result.Reason
	}).Bind()

	uncertain := workflow.NewExecutor("HandleUncertainExecutor", func(result DetectionResult) string {
		return "Email queued for review: " + result.Reason
	}).Bind()

	b := workflow.NewBuilder(detect)
	b.AddSwitch(detect).
		AddCase(func(msg any) bool { return msg.(DetectionResult).Decision == NotSpam }, assistant).
		AddCase(func(msg any) bool { return msg.(DetectionResult).Decision == Spam }, spam).
		WithDefault(uncertain).
		AddToBuilder(b).
		AddEdge(assistant, send).
		WithOutputFrom(send, spam, uncertain)

	wf, err := b.Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "Can you review the attached invoice when you have time?")
	if err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}

func detectEmail(email string) DetectionResult {
	lower := strings.ToLower(email)
	switch {
	case strings.Contains(lower, "wire transfer") || strings.Contains(lower, "prize"):
		return DetectionResult{Email: email, Decision: Spam, Reason: "matched suspicious wording"}
	case strings.Contains(lower, "attached") || strings.Contains(lower, "invoice"):
		return DetectionResult{Email: email, Decision: Uncertain, Reason: "needs manual invoice review"}
	default:
		return DetectionResult{Email: email, Decision: NotSpam, Reason: "ordinary correspondence"}
	}
}
