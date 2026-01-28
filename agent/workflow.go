// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"reflect"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

func newExecutor(a *Agent, emitEvents bool) *workflow.Executor {
	var session memory.Session
	var sessionStateKey string
	ensureSession := func(ctx context.Context) (memory.Session, error) {
		var err error
		if session == nil {
			session, err = a.NewSession(ctx)
		}
		sessionStateKey = reflect.ValueOf(session).String()
		return session, err
	}
	id := agentDescriptiveID(a)
	ex := &workflow.Executor{
		ID: id,
		Config: []*workflow.ExecutorConfig{
			{
				OnCheckpoint: func(wctx *workflow.Context) error {
					if session == nil {
						return nil
					}
					data, err := session.MarshalBinary()
					if err != nil {
						return err
					}
					return wctx.QueueStateUpdate(sessionStateKey, "", data)
				},
				OnCheckpointRestored: func(wctx *workflow.Context) error {
					data, err := wctx.ReadState(sessionStateKey, "")
					if err != nil {
						return err
					}
					if data == nil {
						return nil
					}
					session, err = a.UnmarshalSession(data.([]byte))
					return err
				},
			},
		},
	}
	ex.Config = append(ex.Config, messageworkflow.NewExecutorConfig(&messageworkflow.Options{
		StateKey: "agent_messages",
		TakeTurnHandler: func(ctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error {
			emitEvents := token.EmitEventsOr(emitEvents)
			options := make([]agentopt.RunOption, 0, 1+len(messages))
			session, err := ensureSession(ctx)
			if err != nil {
				return err
			}
			options = append(options, agentopt.Session(session))
			// Run the agent in streaming mode only when agent run update events are to be emitted.
			options = append(options, agentopt.Stream(emitEvents))
			var updates []*message.ResponseUpdate
			for update, err := range a.Run(messages, options...).All(ctx) {
				if err != nil {
					return err
				}
				if emitEvents {
					if err := ctx.AddEvent(workflow.ResponseUpdateEvent{ExecutorID: id, Update: update}); err != nil {
						return err
					}
				}
				updates = append(updates, update)
			}
			msgs := make([]*message.Message, 0, len(updates))
			for _, update := range updates {
				msgs = append(msgs, &message.Message{Role: update.Role, Contents: update.Contents})
			}
			return ctx.SendMessage("", msgs)
		},
	}))
	return ex
}

func (a *Agent) Bind(emitEvents bool) *workflow.ExecutorBinding {
	return &workflow.ExecutorBinding{
		ID:           agentDescriptiveID(a),
		ExecutorType: reflect.TypeOf(a),
		Raw:          a,
		NewExecutor: func(_ string) (*workflow.Executor, error) {
			return newExecutor(a, emitEvents), nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}

func agentDescriptiveID(a *Agent) string {
	if a.Name() != "" {
		return a.Name() + "_" + a.ID()
	}
	return a.ID()
}
