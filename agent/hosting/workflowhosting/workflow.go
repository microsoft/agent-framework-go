// Copyright (c) Microsoft. All rights reserved.

// Package workflowhosting hosts an [agent.Agent] as a workflow
// [workflow.Executor], so the agent can participate in graphs alongside
// regular executors and other hosted agents.
package workflowhosting

import (
	"context"
	"reflect"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

const agentSessionStateKey = "agent_session"

// Config configures how an [agent.Agent] is hosted as a workflow
// [workflow.Executor].
type Config struct {
	// EmitUpdateEvents controls whether streaming [workflow.ResponseUpdateEvent]s
	// are emitted as the agent runs. A [workflow.TurnToken] with
	// [workflow.TurnToken.EmitEvents] set overrides this default for that turn.
	EmitUpdateEvents bool

	// EmitResponseEvents controls whether an aggregated [workflow.ResponseEvent]
	// is emitted at the end of each turn.
	EmitResponseEvents bool

	// DisableMessageForwarding disables forwarding of incoming messages
	// downstream before the agent runs. By default (zero value), incoming
	// messages are forwarded so downstream nodes observe the full
	// conversation. Set to true for strict pipelines where each node should
	// only forward its own output.
	DisableMessageForwarding bool

	// DisableRoleReassignment disables rewriting incoming
	// [message.RoleAssistant] messages whose [message.Message.AuthorName]
	// does not match this agent to [message.RoleUser]. By default (zero
	// value), such messages are reassigned so the conversation between
	// agents appears, to each agent, as messages from "the user". Set to
	// true to preserve original roles.
	DisableRoleReassignment bool
}

// agentBindingMarker is an unexported sentinel type used as the
// ExecutorBinding.ExecutorType for bindings created by [New].
//
// Using a private type ensures that bindings produced by this package can be
// distinguished from any third-party ExecutorBinding referencing the same
// agent ID, so the workflow builder will surface a clear "different type"
// error instead of silently merging incompatible bindings.
type agentBindingMarker struct{}

// New creates a workflow [workflow.ExecutorBinding] that hosts the given
// [agent.Agent] using the supplied [Config]. The zero value of [Config] is a
// sensible default.
func New(a *agent.Agent, cfg Config) *workflow.ExecutorBinding {
	return &workflow.ExecutorBinding{
		ID:           descriptiveID(a),
		ExecutorType: reflect.TypeFor[agentBindingMarker](),
		Raw:          a,
		NewExecutor: func(_ string) (*workflow.Executor, error) {
			return newExecutor(a, cfg), nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}

func newExecutor(a *agent.Agent, opts Config) *workflow.Executor {
	var session agent.Session
	ensureSession := func(ctx context.Context) (agent.Session, error) {
		if session == nil {
			var err error
			session, err = a.CreateSession(ctx)
			if err != nil {
				return nil, err
			}
		}
		return session, nil
	}
	id := descriptiveID(a)
	selfName := a.Name()
	ex := &workflow.Executor{
		ID: id,
		Config: []*workflow.ExecutorConfig{
			{
				OnCheckpoint: func(wctx *workflow.Context) error {
					if session == nil {
						return nil
					}
					data, err := a.MarshalSession(wctx, session)
					if err != nil {
						return err
					}
					return wctx.QueueStateUpdate(agentSessionStateKey, "", data)
				},
				OnCheckpointRestored: func(wctx *workflow.Context) error {
					data, err := wctx.ReadState(agentSessionStateKey, "")
					if err != nil {
						return err
					}
					if data == nil {
						return nil
					}
					session, err = a.UnmarshalSession(wctx, data.([]byte))
					return err
				},
			},
		},
	}
	ex.Config = append(ex.Config, messageworkflow.NewExecutorConfig(&messageworkflow.Options{
		StateKey: "agent_messages",
		TakeTurnHandler: func(ctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error {
			emitUpdates := token.EmitEventsOr(opts.EmitUpdateEvents)

			// Forward the incoming messages downstream before the agent runs, so that
			// downstream nodes (e.g. other agents) observe the full conversation.
			if !opts.DisableMessageForwarding && len(messages) > 0 {
				if err := ctx.SendMessage("", messages); err != nil {
					return err
				}
			}

			// Optionally reassign non-self assistant messages to user role before
			// passing them to the agent.
			agentInput := messages
			if !opts.DisableRoleReassignment {
				agentInput = reassignOtherAgentsAsUsers(messages, selfName)
			}

			session, err := ensureSession(ctx)
			if err != nil {
				return err
			}
			runOpts := []agent.Option{
				agent.WithSession(session),
				// Run the agent in streaming mode only when update events are to be emitted.
				agent.Stream(emitUpdates),
			}
			var resp message.Response
			for update, err := range a.Run(ctx, agentInput, runOpts...) {
				if err != nil {
					return err
				}
				if emitUpdates {
					if err := ctx.AddEvent(workflow.ResponseUpdateEvent{ExecutorID: id, Update: update}); err != nil {
						return err
					}
				}
				resp.Update(update)
			}
			resp.Coalesce()
			// Stamp this hosting executor's identity on every aggregated
			// message, so downstream nodes can identify which hosted agent
			// produced them (and so the role-reassignment logic in receiving
			// hosts works correctly).
			for _, m := range resp.Messages {
				m.AuthorID = a.ID()
				m.AuthorName = selfName
			}
			if opts.EmitResponseEvents {
				if err := ctx.AddEvent(workflow.ResponseEvent{ExecutorID: id, Response: &resp}); err != nil {
					return err
				}
			}
			return ctx.SendMessage("", resp.Messages)
		},
	}))
	return ex
}

// reassignOtherAgentsAsUsers returns a copy of msgs in which any RoleAssistant
// message whose AuthorName does not match selfName is rewritten with RoleUser.
// Messages that are unchanged are returned by the original pointer (no copy).
func reassignOtherAgentsAsUsers(msgs []*message.Message, selfName string) []*message.Message {
	var out []*message.Message
	for i, m := range msgs {
		if m == nil || m.Role != message.RoleAssistant || m.AuthorName == selfName {
			if out != nil {
				out = append(out, m)
			}
			continue
		}
		if out == nil {
			out = make([]*message.Message, 0, len(msgs))
			out = append(out, msgs[:i]...)
		}
		clone := m.Clone()
		clone.Role = message.RoleUser
		out = append(out, clone)
	}
	if out == nil {
		return msgs
	}
	return out
}

func descriptiveID(a *agent.Agent) string {
	if a.Name() != "" {
		return a.Name() + "_" + a.ID()
	}
	return a.ID()
}
