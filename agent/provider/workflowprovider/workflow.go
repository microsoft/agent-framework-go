// Copyright (c) Microsoft. All rights reserved.

// Package workflowprovider hosts a [workflow.Workflow] as an [agent.Agent].
//
// On each agent run, a fresh streaming workflow run is started, the supplied
// messages are enqueued, a [workflow.TurnToken] is sent, and workflow events
// are translated back into [message.ResponseUpdate]s.
package workflowprovider

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var messagesSliceType = reflect.TypeFor[[]*message.Message]()

// Config configures a [workflow.Workflow] hosted as an [agent.Agent] via [New].
type Config struct {
	agent.Config

	// Environment is the execution environment used to run the workflow on
	// each agent turn. Defaults to [inproc.Default] when nil.
	Environment *inproc.ExecutionEnvironment

	// IncludeOutputsInResponse, if true, surfaces [workflow.OutputEvent]
	// payloads in the agent response stream when the payload is a
	// [*message.Message] or [[]*message.Message]. By default outputs are
	// observed only via [workflow.ResponseUpdateEvent]s emitted by hosted
	// agents inside the workflow.
	IncludeOutputsInResponse bool

	// IncludeErrorDetails, if true, surfaces the full error message from
	// [workflow.ErrorEvent]s in the agent response stream. When false, a
	// generic message is emitted instead.
	IncludeErrorDetails bool
}

// New wraps a [*workflow.Workflow] as an [*agent.Agent].
//
// The workflow's start executor must accept [[]*message.Message] (typically
// configured via [messageworkflow.NewExecutorConfig]) and emit
// [workflow.ResponseUpdateEvent]s and/or yield outputs that translate to chat
// messages. On each call to the agent's Run, a fresh streaming run is
// started, the supplied messages are enqueued, a [workflow.TurnToken] is
// sent, and the resulting events are translated into agent response updates.
//
// Workflow [workflow.RequestInfoEvent]s and [workflow.OutputEvent]s for
// non-chat payloads are not surfaced to the caller (they cause the run to
// halt without producing further updates).
func New(wf *workflow.Workflow, cfg Config) (*agent.Agent, error) {
	if wf == nil {
		return nil, errors.New("workflow cannot be nil")
	}
	descriptor, err := wf.DescribeProtocol()
	if err != nil {
		return nil, fmt.Errorf("workflow start executor protocol could not be determined: %w", err)
	}
	// Validate that the start executor can accept the messages we'll send.
	if !slices.Contains(descriptor.Accepts, messagesSliceType) {
		return nil, fmt.Errorf("workflow start executor does not accept []*message.Message")
	}

	env := cfg.Environment
	if env == nil {
		if wf.AllowConcurrent() {
			env = inproc.Concurrent
		} else {
			env = inproc.OffThread
		}
	}

	runFn := func(ctx context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			stream, err := env.OpenStream(ctx, wf, "")
			if err != nil {
				yield(nil, err)
				return
			}
			defer stream.Cancel()

			if len(messages) > 0 {
				if err := stream.SendMessage(ctx, messages); err != nil {
					yield(nil, err)
					return
				}
			}
			emit := true
			if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emit}); err != nil {
				yield(nil, err)
				return
			}

			for evt, err := range stream.WatchStream(ctx) {
				if err != nil {
					yield(nil, err)
					return
				}
				switch e := evt.(type) {
				case workflow.ResponseUpdateEvent:
					if !yield(e.Update, nil) {
						return
					}
				case workflow.ResponseEvent:
					// ResponseEvent is the aggregated counterpart to
					// ResponseUpdateEvent; emit each message as a
					// streaming update.
					if e.Response == nil {
						continue
					}
					for _, msg := range e.Response.Messages {
						if !yield(messageToUpdate(msg), nil) {
							return
						}
					}
				case workflow.OutputEvent:
					if !cfg.IncludeOutputsInResponse {
						continue
					}
					switch out := e.Output.(type) {
					case *message.Message:
						if !yield(messageToUpdate(out), nil) {
							return
						}
					case []*message.Message:
						for _, msg := range out {
							if !yield(messageToUpdate(msg), nil) {
								return
							}
						}
					}
				case workflow.ErrorEvent:
					text := "an error occurred while executing the workflow"
					if cfg.IncludeErrorDetails && e.Error != nil {
						text = e.Error.Error()
					}
					update := &message.ResponseUpdate{
						Role: message.RoleAssistant,
						Contents: []message.Content{&message.ErrorContent{
							Message: text,
						}},
					}
					if !yield(update, nil) {
						return
					}
				}
			}
		}
	}

	// The hosted workflow handles its own tool-call loop via its inner
	// executors, so the agent-level autocall middleware would double-process
	// function calls.
	cfg.Config.DisableFuncAutoCall = true
	return agent.New(
		agent.ProviderConfig{
			ProviderName: "workflow",
			Run:          runFn,
		},
		cfg.Config,
	), nil
}

func messageToUpdate(m *message.Message) *message.ResponseUpdate {
	if m == nil {
		return &message.ResponseUpdate{}
	}
	return &message.ResponseUpdate{
		Role:       m.Role,
		Contents:   m.Contents,
		AuthorID:   m.AuthorID,
		AuthorName: m.AuthorName,
		MessageID:  m.ID,
	}
}
