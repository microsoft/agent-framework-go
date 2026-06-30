// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	aggregateTurnMessagesStateKey = "agentworkflow.AggregateTurnMessagesExecutor.State"
	concurrentEndExecutorID       = "agentworkflow.ConcurrentEnd"
)

// MessageAggregator combines per-agent message batches into a single batch.
type MessageAggregator func(context.Context, [][]*message.Message) []*message.Message

// ConcurrentWorkflowBuilder fluently builds concurrent agent workflows.
type ConcurrentWorkflowBuilder struct {
	name               string
	description        string
	agents             []*agent.Agent
	aggregator         MessageAggregator
	outputDesignations outputDesignations
	err                error
}

// NewConcurrentWorkflowBuilder creates a builder for a concurrent agent workflow.
func NewConcurrentWorkflowBuilder(agents ...*agent.Agent) *ConcurrentWorkflowBuilder {
	return &ConcurrentWorkflowBuilder{agents: slices.Clone(agents)}
}

// WithName sets the workflow name.
func (b *ConcurrentWorkflowBuilder) WithName(name string) *ConcurrentWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.name = name
	return b
}

// WithDescription sets the workflow description.
func (b *ConcurrentWorkflowBuilder) WithDescription(description string) *ConcurrentWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.description = description
	return b
}

// WithAggregator sets the concurrent output aggregator. Nil keeps the default aggregator.
func (b *ConcurrentWorkflowBuilder) WithAggregator(aggregator MessageAggregator) *ConcurrentWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.aggregator = aggregator
	return b
}

// WithOutputFrom designates agents as terminal workflow output sources.
func (b *ConcurrentWorkflowBuilder) WithOutputFrom(agents ...*agent.Agent) *ConcurrentWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withOutputFrom(agents...)
	return b
}

// WithIntermediateOutputFrom designates agents as intermediate workflow output sources.
func (b *ConcurrentWorkflowBuilder) WithIntermediateOutputFrom(agents ...*agent.Agent) *ConcurrentWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withIntermediateOutputFrom(agents...)
	return b
}

// Build builds the concurrent workflow.
func (b *ConcurrentWorkflowBuilder) Build() (*workflow.Workflow, error) {
	if b == nil {
		return nil, fmt.Errorf("agentworkflow: ConcurrentWorkflowBuilder is nil")
	}
	if b.err != nil {
		return nil, b.err
	}
	if err := validateBuilderAgents("ConcurrentWorkflowBuilder", b.agents); err != nil {
		return nil, err
	}

	cfg := Config{}
	bindings := make([]workflow.ExecutorBinding, len(b.agents))
	bindingsByAgent := make(map[*agent.Agent]workflow.ExecutorBinding, len(b.agents))
	for index, currentAgent := range b.agents {
		binding := New(currentAgent, cfg)
		bindings[index] = binding
		bindingsByAgent[currentAgent] = binding
	}

	start := newMessageForwardingBinding("Start")
	accumulators := make([]workflow.ExecutorBinding, len(bindings))
	for i, binding := range bindings {
		accumulators[i] = newAggregateTurnMessagesBinding("Batcher/" + binding.ID)
	}
	end := newConcurrentEndBinding(len(bindings), b.aggregator)

	bld := applyBuilderMetadata(workflow.NewBuilder(start), b.name, b.description)
	bld = bld.AddFanOutEdge(start, bindings)
	for i, binding := range bindings {
		bld = bld.AddEdge(binding, accumulators[i])
	}
	bld = bld.AddFanInBarrierEdge(accumulators, end)
	var err error
	bld, err = applyOutputDesignations(bld, b.outputDesignations, bindingsByAgent, "concurrent", func() {
		intermediateOutputs := make([]workflow.ExecutorBinding, 0, len(bindings)+len(accumulators))
		intermediateOutputs = append(intermediateOutputs, bindings...)
		intermediateOutputs = append(intermediateOutputs, accumulators...)
		bld = bld.WithOutputFrom(end).WithIntermediateOutputFrom(intermediateOutputs...)
	})
	if err != nil {
		return nil, err
	}
	return bld.Build()
}

func newMessageForwardingBinding(id string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:                                id,
		SupportsConcurrentSharedExecution: true,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			executor := workflow.Executor{ID: id}
			messageworkflow.ConfigureForwarding(&executor, nil)
			return &executor, nil
		},
	}
}

func newAggregateTurnMessagesBinding(id string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:                                id,
		SupportsConcurrentSharedExecution: true,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			executor := workflow.Executor{
				ID: id,
				ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					return pb.SendsMessageType(reflect.TypeFor[[]*message.Message]()), nil
				},
			}
			messageworkflow.Configure(&executor, &messageworkflow.Options{
				StateKey:                 aggregateTurnMessagesStateKey,
				DisableAutoSendTurnToken: true,
				TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
					return ctx.SendMessage("", messages)
				},
			})
			return &executor, nil
		},
	}
}

func newConcurrentEndBinding(expectedInputs int, aggregator MessageAggregator) workflow.ExecutorBinding {
	if aggregator == nil {
		aggregator = defaultConcurrentMessageAggregator
	}
	return workflow.ExecutorBinding{
		ID:                                concurrentEndExecutorID,
		SupportsConcurrentSharedExecution: true,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			allResults := make([][]*message.Message, 0, expectedInputs)
			remaining := expectedInputs
			reset := func() {
				allResults = make([][]*message.Message, 0, expectedInputs)
				remaining = expectedInputs
			}
			return &workflow.Executor{
				ID: concurrentEndExecutorID,
				ResetFunc: func() error {
					reset()
					return nil
				},
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.YieldsOutputType(reflect.TypeFor[[]*message.Message]())
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						allResults = append(allResults, msg.([]*message.Message))
						remaining--
						if remaining == 0 {
							results := allResults
							reset()
							if err := ctx.YieldOutput(aggregator(ctx, results)); err != nil {
								return nil, err
							}
						}
						return struct{}{}, nil
					})
					return rb, nil
				},
			}, nil
		},
	}
}

func defaultConcurrentMessageAggregator(_ context.Context, lists [][]*message.Message) []*message.Message {
	results := make([]*message.Message, 0, len(lists))
	for _, list := range lists {
		if len(list) > 0 {
			results = append(results, list[len(list)-1])
		}
	}
	return results
}
