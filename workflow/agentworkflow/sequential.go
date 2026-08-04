// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	outputMessagesExecutorID = "agentworkflow.OutputMessages"
	outputMessagesStateKey   = "agentworkflow.OutputMessagesExecutor.State"
)

// SequentialWorkflowBuilder fluently builds sequential agent workflows.
type SequentialWorkflowBuilder struct {
	name                    string
	description             string
	agents                  []*agent.Agent
	chainOnlyAgentResponses bool
	outputDesignations      outputDesignations
	err                     error
}

// NewSequentialWorkflowBuilder creates a builder for a sequential agent workflow.
func NewSequentialWorkflowBuilder(agents ...*agent.Agent) *SequentialWorkflowBuilder {
	return &SequentialWorkflowBuilder{agents: slices.Clone(agents)}
}

// WithName sets the workflow name.
func (b *SequentialWorkflowBuilder) WithName(name string) *SequentialWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.name = name
	return b
}

// WithDescription sets the workflow description.
func (b *SequentialWorkflowBuilder) WithDescription(description string) *SequentialWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.description = description
	return b
}

// WithChainOnlyAgentResponses controls whether each agent only forwards its own
// response to the next agent, instead of forwarding the full accumulated
// conversation. The default is false.
func (b *SequentialWorkflowBuilder) WithChainOnlyAgentResponses(enabled bool) *SequentialWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.chainOnlyAgentResponses = enabled
	return b
}

// WithOutputFrom designates agents as terminal workflow output sources.
func (b *SequentialWorkflowBuilder) WithOutputFrom(agents ...*agent.Agent) *SequentialWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withOutputFrom(agents...)
	return b
}

// WithIntermediateOutputFrom designates agents as intermediate workflow output sources.
func (b *SequentialWorkflowBuilder) WithIntermediateOutputFrom(agents ...*agent.Agent) *SequentialWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withIntermediateOutputFrom(agents...)
	return b
}

// Build builds the sequential workflow.
func (b *SequentialWorkflowBuilder) Build() (*workflow.Workflow, error) {
	if b == nil {
		return nil, fmt.Errorf("agentworkflow: SequentialWorkflowBuilder is nil")
	}
	if b.err != nil {
		return nil, b.err
	}
	if err := validateBuilderAgents("SequentialWorkflowBuilder", b.agents); err != nil {
		return nil, err
	}

	cfg := Config{DisableForwardIncomingMessages: b.chainOnlyAgentResponses}
	bindings, bindingsByAgent := newAgentBindings(b.agents, cfg)

	bld := applyBuilderMetadata(workflow.NewBuilder(bindings[0]), b.name, b.description)
	previous := bindings[0]
	for _, next := range bindings[1:] {
		bld = bld.AddEdge(previous, next)
		previous = next
	}
	outputMessages := newOutputMessagesBinding()
	bld = bld.AddEdge(previous, outputMessages)
	var err error
	bld, err = applyOutputDesignations(bld, b.outputDesignations, bindingsByAgent, "sequential", func() {
		bld = bld.WithOutputFrom(outputMessages).WithIntermediateOutputFrom(bindings...)
	})
	if err != nil {
		return nil, err
	}
	return bld.Build()
}

func newOutputMessagesBinding() workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:                                outputMessagesExecutorID,
		SupportsConcurrentSharedExecution: true,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			executor := workflow.Executor{
				ID: outputMessagesExecutorID,
				ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					return pb.YieldsOutputType(reflect.TypeFor[[]*message.Message]()), nil
				},
			}
			messageworkflow.Configure(&executor, &messageworkflow.Options{
				StateKey:                 outputMessagesStateKey,
				DisableAutoSendTurnToken: true,
				TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
					return ctx.YieldOutput(messages)
				},
			})
			return &executor, nil
		},
	}
}
