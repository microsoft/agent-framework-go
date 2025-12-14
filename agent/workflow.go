// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"reflect"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

type RunUpdateEvent struct {
	ExecutorID string
	Update     *RunResponseUpdate
}

func (e RunUpdateEvent) Data() any {
	return e.Update
}

func newExecutor(a Agent, emitEvents bool) *workflow.Executor {
	var thread memory.Thread
	var threadStateKey string
	ensureThread := func() memory.Thread {
		if thread == nil {
			thread = a.NewThread()
		}
		threadStateKey = reflect.ValueOf(thread).String()
		return thread
	}
	id := agentDescriptiveID(a)
	ex := &workflow.Executor{
		ID: id,
		Config: []*workflow.ExecutorConfig{
			{
				OnCheckpoint: func(wctx *workflow.Context) error {
					if thread == nil {
						return nil
					}
					data, err := json.Marshal(thread)
					if err != nil {
						return err
					}
					return wctx.QueueStateUpdate(threadStateKey, "", data)
				},
				OnCheckpointRestored: func(wctx *workflow.Context) error {
					data, err := wctx.ReadState(threadStateKey, "")
					if err != nil {
						return err
					}
					if data == nil {
						return nil
					}
					thread, err = a.UnmarshalThread(data.([]byte))
					return err
				},
			},
		},
	}
	ex.Config = append(ex.Config, messageworkflow.NewExecutorConfig(&messageworkflow.Options{
		StateKey: "agent_messages",
		TakeTurnHandler: func(ctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error {
			emitEvents := token.EmitEventsOr(emitEvents)
			options := make([]agentopt.Option, 0, 1+len(messages))
			options = append(options, agentopt.Thread(ensureThread()))
			if emitEvents {
				// Run the agent in streaming mode only when agent run update events are to be emitted.
				options = append(options, agentopt.Stream(true))
			}
			for _, msg := range messages {
				options = append(options, agentopt.Message(msg))
			}
			var updates []*RunResponseUpdate
			for update, err := range RunStream(ctx, a, options...) {
				if err != nil {
					return err
				}
				if emitEvents {
					if err := ctx.AddEvent(RunUpdateEvent{id, update}); err != nil {
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

func Bind(ag Agent, emitEvents bool) *workflow.ExecutorBinding {
	return &workflow.ExecutorBinding{
		ID:           agentDescriptiveID(ag),
		ExecutorType: reflect.TypeOf(ag),
		Raw:          ag,
		NewExecutor: func(_ string) (*workflow.Executor, error) {
			return newExecutor(ag, emitEvents), nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}

func agentDescriptiveID(ag Agent) string {
	iden := ag.Identity()
	id := iden.ID()
	name := iden.Name()
	if name != "" {
		return name + "_" + id
	}
	return id
}
