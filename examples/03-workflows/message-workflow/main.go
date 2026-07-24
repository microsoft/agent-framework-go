// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Message Workflow",
	"This sample drives the messageworkflow chat-protocol primitives: a forwarding relay "+
		"accumulates messages until a TurnToken triggers exactly one reply.",
)

const (
	driverID    = "Driver"
	relayID     = "Relay"
	chatID      = "Chat"
	collectorID = "Collector"
)

func main() {
	// Auto-send is the default: after the chat executor takes its turn it
	// forwards the TurnToken downstream, so the collector observes it.
	demo.Assistantf("Scenario 1: TurnToken forwarded downstream (auto-send, the default)")
	runTurn(false)

	// DisableAutoSendTurnToken keeps the token from being forwarded, which is
	// how terminal executors (for example an output collector) end a turn.
	demo.Assistantf("Scenario 2: DisableAutoSendTurnToken keeps the token from being forwarded")
	runTurn(true)
}

// runTurn builds and runs driver -> relay -> chat -> collector once.
//
//   - driver seeds a couple of user messages and a TurnToken.
//   - relay (messageworkflow.ConfigureForwarding) relays them downstream.
//   - chat (messageworkflow.Configure) accumulates the messages and, on the
//     TurnToken, invokes its TakeTurnHandler exactly once to build a reply.
//   - collector records the reply and any forwarded TurnToken.
func runTurn(disableAutoSendTurnToken bool) {
	// State observed after the run. Each call rebuilds fresh executors, so the
	// closures below capture per-run counters.
	turnHandlerCalls := 0
	forwardedTokens := 0

	driver := workflow.NewExecutor(driverID, func(ctx *workflow.Context, messages []*message.Message) error {
		if err := ctx.SendMessage("", messages); err != nil {
			return err
		}
		return ctx.SendMessage("", workflow.TurnToken{})
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[[]*message.Message](), reflect.TypeFor[workflow.TurnToken]())
			return rb, nil
		},
	}).Bind()

	relay := workflow.BindNewExecutorFunc(relayID, func(_ string, executorID string) (*workflow.Executor, error) {
		executor := workflow.Executor{ID: executorID}
		messageworkflow.ConfigureForwarding(&executor, &messageworkflow.ForwardingOptions{
			StringMessageRole: message.RoleUser,
		})
		return &executor, nil
	})

	chat := workflow.BindNewExecutorFunc(chatID, func(_ string, executorID string) (*workflow.Executor, error) {
		executor := workflow.Executor{ID: executorID}
		messageworkflow.Configure(&executor, &messageworkflow.Options{
			StateKey:                 "chat",
			DisableAutoSendTurnToken: disableAutoSendTurnToken,
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
				turnHandlerCalls++
				parts := make([]string, 0, len(messages))
				for _, m := range messages {
					parts = append(parts, m.Contents.Text())
				}
				reply := "You said: " + strings.Join(parts, " | ")
				return ctx.SendMessage("", &message.Message{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.TextContent{Text: reply}},
				})
			},
		})
		executor.Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[*message.Message]())
				return rb, nil
			},
		})
		return &executor, nil
	})

	var replies []string
	collector := workflow.NewExecutor(collectorID, func(_ *workflow.Context, reply *message.Message) error {
		replies = append(replies, reply.Contents.Text())
		return nil
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[workflow.TurnToken](), nil, func(_ *workflow.Context, _ any) (any, error) {
				forwardedTokens++
				return struct{}{}, nil
			})
			rb.YieldsOutputType(reflect.TypeFor[string]())
			return rb, nil
		},
		OnMessageDeliveryFinishedFunc: func(ctx *workflow.Context) error {
			if len(replies) == 0 {
				return nil
			}
			transcript := fmt.Sprintf("assistant reply: %s (forwarded turn tokens: %d)",
				strings.Join(replies, "; "), forwardedTokens)
			replies = nil
			return ctx.YieldOutput(transcript)
		},
	}).Bind()

	wf, err := workflow.NewBuilder(driver).
		AddEdge(driver, relay).
		AddEdge(relay, chat).
		AddEdge(chat, collector).
		WithOutputFrom(collector).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	input := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "how are you?"}}},
	}

	run, err := inproc.Default.Run(context.Background(), wf, input)
	if err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		switch e := evt.(type) {
		case workflow.OutputEvent:
			demo.Assistant(e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
	demo.Assistantf("TakeTurnHandler invocations: %d", turnHandlerCalls)
}
