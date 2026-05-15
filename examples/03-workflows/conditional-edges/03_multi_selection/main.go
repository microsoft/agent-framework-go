// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Multi Selection Workflow",
	"This sample routes one analysis result to multiple workflow executors.",
)

const longEmailThreshold = 100

type SpamDecision int

const (
	NotSpam SpamDecision = iota
	Spam
	Uncertain
)

type AnalysisResult struct {
	Email       string
	Decision    SpamDecision
	Reason      string
	EmailLength int
}

func main() {
	analyze := workflow.BindFunc("EmailAnalysisExecutor", analyzeEmail)
	spam := workflow.BindFunc("HandleSpamExecutor", func(result AnalysisResult) string { return "Email marked as spam: " + result.Reason })
	assistant := workflow.BindFunc("EmailAssistantExecutor", func(result AnalysisResult) string {
		return "Draft response for email length " + fmt.Sprint(result.EmailLength)
	})
	summary := workflow.BindFunc("EmailSummaryExecutor", func(result AnalysisResult) string { return "Summary: " + firstWords(result.Email, 12) })
	uncertain := workflow.BindFunc("HandleUncertainExecutor", func(result AnalysisResult) string { return "Email queued for review: " + result.Reason })
	send := workflow.BindFunc("SendEmailExecutor", func(response string) string { return "Email sent: " + response })
	log := workflow.BindFunc("DatabaseAccessExecutor", func(value string) string { return "Logged: " + value })

	b := workflow.NewBuilder(analyze)
	b.AddFanOutEdge(analyze, []workflow.ExecutorBinding{spam, assistant, summary, uncertain}, workflow.WithEdgeAssigner(routeAnalysis)).
		AddEdge(assistant, send).
		AddEdge(summary, log).
		AddDirectEdge(analyze, log, false, func(msg any) bool { return msg.(AnalysisResult).EmailLength <= longEmailThreshold }).
		WithOutputFrom(spam, send, uncertain, log)

	wf, err := b.Build()
	if err != nil {
		demo.Panic(err)
	}

	email := "Hello, I wanted to share a long project update with timelines, risks, milestones, and a few open questions for next week's planning meeting."
	run, err := inproc.Default.Run(context.Background(), wf, email)
	if err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}

func analyzeEmail(email string) AnalysisResult {
	lower := strings.ToLower(email)
	decision := NotSpam
	reason := "ordinary correspondence"
	if strings.Contains(lower, "wire transfer") || strings.Contains(lower, "prize") {
		decision = Spam
		reason = "matched suspicious wording"
	} else if strings.Contains(lower, "attached") || strings.Contains(lower, "invoice") {
		decision = Uncertain
		reason = "needs manual review"
	}
	return AnalysisResult{Email: email, Decision: decision, Reason: reason, EmailLength: len(email)}
}

func routeAnalysis(_ int, msg any) iter.Seq[int] {
	return func(yield func(int) bool) {
		result := msg.(AnalysisResult)
		switch result.Decision {
		case Spam:
			yield(0)
		case NotSpam:
			if !yield(1) {
				return
			}
			if result.EmailLength > longEmailThreshold {
				yield(2)
			}
		case Uncertain:
			yield(3)
		}
	}
}

func firstWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}
