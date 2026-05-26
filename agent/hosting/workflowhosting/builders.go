// Copyright (c) Microsoft. All rights reserved.

package workflowhosting

import (
	"context"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	aggregateTurnMessagesStateKey = "workflowhosting.AggregateTurnMessagesExecutor.State"
	concurrentEndExecutorID       = "workflowhosting.ConcurrentEnd"
	outputMessagesExecutorID      = "workflowhosting.OutputMessages"
	outputMessagesStateKey        = "workflowhosting.OutputMessagesExecutor.State"
)

// BuildSequential builds a [workflow.Workflow] with the given name that runs agents in order:
// each agent's output (and incoming messages) is forwarded as input to the
// next, forming a linear pipeline.
//
// The name may be empty.
//
// Agents are hosted with the default [Config], which enables incoming-message
// forwarding and role reassignment so that each agent in the chain sees the
// full conversation in the correct roles.
func BuildSequential(name string, agents ...*agent.Agent) (*workflow.Workflow, error) {
	if err := validateBuilderAgents("BuildSequential", agents); err != nil {
		return nil, err
	}

	// Default Config: message forwarding and role reassignment are both
	// enabled (zero-value booleans = false means "do NOT disable").
	cfg := Config{}
	bindings := make([]workflow.ExecutorBinding, len(agents))
	for index, currentAgent := range agents {
		bindings[index] = New(currentAgent, cfg)
	}

	bld := workflow.NewBuilder(bindings[0]).WithName(name)
	previous := bindings[0]
	for _, next := range bindings[1:] {
		bld = bld.AddEdge(previous, next)
		previous = next
	}
	outputMessages := newOutputMessagesBinding()
	bld = bld.AddEdge(previous, outputMessages).WithOutputFrom(outputMessages)
	return bld.Build()
}

func validateBuilderAgents(builderName string, agents []*agent.Agent) error {
	if len(agents) == 0 {
		return fmt.Errorf("workflowhosting: %s requires at least one agent", builderName)
	}
	for index, currentAgent := range agents {
		if currentAgent == nil {
			return fmt.Errorf("workflowhosting: %s agent at index %d is nil", builderName, index)
		}
	}
	return nil
}

// MessageAggregator combines per-agent message batches into a single batch.
type MessageAggregator func(context.Context, [][]*message.Message) []*message.Message

// BuildConcurrent builds a [workflow.Workflow] with the given name that fans a
// single input out to all agents simultaneously. Each agent runs independently;
// the workflow output is the last message in each non-empty per-agent batch.
//
// The name may be empty.
//
// Agents are hosted with the default [Config], which enables incoming-message
// forwarding and role reassignment.
func BuildConcurrent(name string, agents ...*agent.Agent) (*workflow.Workflow, error) {
	return BuildConcurrentWithAggregator(name, nil, agents...)
}

// BuildConcurrentWithAggregator builds a concurrent agent workflow using
// aggregator to combine each agent's turn-message batch into the final workflow
// output. If aggregator is nil, the default behavior returns the last message
// in each non-empty batch.
func BuildConcurrentWithAggregator(name string, aggregator MessageAggregator, agents ...*agent.Agent) (*workflow.Workflow, error) {
	if err := validateBuilderAgents("BuildConcurrentWithAggregator", agents); err != nil {
		return nil, err
	}

	cfg := Config{}
	bindings := make([]workflow.ExecutorBinding, len(agents))
	for index, currentAgent := range agents {
		bindings[index] = New(currentAgent, cfg)
	}

	start := newMessageForwardingBinding("Start")
	accumulators := make([]workflow.ExecutorBinding, len(bindings))
	for i, binding := range bindings {
		accumulators[i] = newAggregateTurnMessagesBinding("Batcher/" + binding.ID)
	}
	end := newConcurrentEndBinding(len(bindings), aggregator)

	bld := workflow.NewBuilder(start).WithName(name)
	bld = bld.AddFanOutEdge(start, bindings)
	for i, binding := range bindings {
		bld = bld.AddEdge(binding, accumulators[i])
	}
	bld = bld.AddFanInBarrierEdge(accumulators, end)
	bld = bld.WithOutputFrom(end)
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
