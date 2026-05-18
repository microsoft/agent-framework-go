// Copyright (c) Microsoft. All rights reserved.

package workflowhosting

import (
	"errors"
	"reflect"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

// BuildSequential builds a [workflow.Workflow] that runs agents in order:
// each agent's output (and incoming messages) is forwarded as input to the
// next, forming a linear pipeline.
//
// Agents are hosted with the default [Config], which enables incoming-message
// forwarding and role reassignment so that each agent in the chain sees the
// full conversation in the correct roles.
//
// This matches the behaviour of .NET's AgentWorkflowBuilder.BuildSequential.
func BuildSequential(agents ...*agent.Agent) (*workflow.Workflow, error) {
	if len(agents) == 0 {
		return nil, errors.New("workflowhosting: BuildSequential requires at least one agent")
	}

	// Default Config: message forwarding and role reassignment are both
	// enabled (zero-value booleans = false means "do NOT disable").
	cfg := Config{}
	bindings := make([]workflow.ExecutorBinding, len(agents))
	for i, a := range agents {
		bindings[i] = New(a, cfg)
	}

	bld := workflow.NewBuilder(bindings[0])
	if len(bindings) > 1 {
		bld = bld.AddChain(bindings[0], bindings[1:], false)
	}
	bld = bld.WithOutputFrom(bindings[len(bindings)-1])
	return bld.Build()
}

// BuildConcurrent builds a [workflow.Workflow] that fans a single input out to
// all agents simultaneously. Each agent runs independently; their outputs are
// emitted as workflow [workflow.OutputEvent]s as they are produced.
//
// Agents are hosted with [Config.DisableMessageForwarding] set to true so that
// each agent only forwards its own output rather than re-broadcasting the
// shared input. Role reassignment remains enabled.
//
// This mirrors .NET's AgentWorkflowBuilder.BuildConcurrent. Note that unlike
// the .NET version, Go does not currently batch per-agent outputs via an
// aggregating executor; messages are emitted as they arrive from each agent.
func BuildConcurrent(agents ...*agent.Agent) (*workflow.Workflow, error) {
	if len(agents) == 0 {
		return nil, errors.New("workflowhosting: BuildConcurrent requires at least one agent")
	}

	// Agents must not re-forward the shared input; only their own output should
	// propagate. Role reassignment is kept enabled so each agent sees the other
	// agents' messages as "user" messages.
	cfg := Config{DisableMessageForwarding: true}
	bindings := make([]workflow.ExecutorBinding, len(agents))
	for i, a := range agents {
		bindings[i] = New(a, cfg)
	}

	start := newForwardingBinding("concurrent_start")
	bld := workflow.NewBuilder(start)
	bld = bld.AddFanOutEdge(start, bindings)
	bld = bld.WithOutputFrom(bindings...)
	return bld.Build()
}

// forwardingMarker is a private sentinel type used as the ExecutorType for
// forwardingBinding so it never collides with user-created bindings.
type forwardingMarker struct{}

// newForwardingBinding creates an executor binding that forwards every
// received message to all connected executors unchanged. It is used as the
// fan-out start node in [BuildConcurrent].
func newForwardingBinding(id string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:                                id,
		ExecutorType:                      reflect.TypeFor[forwardingMarker](),
		SupportsConcurrentSharedExecution: true,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: id,
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: true,
					DisableAutoYieldOutputHandlerResultObject: true,
					SendTypes: []reflect.Type{
						reflect.TypeFor[[]*message.Message](),
						reflect.TypeFor[workflow.TurnToken](),
					},
					ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
						return rb.AddCatchAll(func(ctx *workflow.Context, msg workflow.PortableValue) (any, error) {
							return nil, ctx.SendMessage("", msg.Any())
						}), nil
					},
				},
			}, nil
		},
	}
}
