// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	defaultGroupChatMaximumIterations = 40

	groupChatHostExecutorID       = "agentworkflow.GroupChatHost"
	groupChatHostBufferedStateKey = "agentworkflow.GroupChatHost.Messages"
	groupChatHistoryStateKey      = "_history"
	groupChatCurrentSpeakerKey    = "_currentSpeakerExecutorId"

	groupChatManagerStateKey             = "GroupChatManager"
	groupChatManagerSubclassStateKeyPref = "GroupChatManager_"
	roundRobinGroupChatManagerStateKey   = "next_index"
)

// GroupChatManagerFactory creates the manager used by a group chat workflow run.
// The returned manager must set [GroupChatManager.SelectNextAgent].
type GroupChatManagerFactory func(agents []*agent.Agent) *GroupChatManager

// GroupChatManager manages the flow of a group chat. SelectNextAgent is
// required; all other functions are optional.
type GroupChatManager struct {
	// SelectNextAgent selects the next participant to speak. Returning nil ends
	// the chat and yields the accumulated conversation.
	SelectNextAgent func(ctx context.Context, history []*message.Message) (*agent.Agent, error)

	// UpdateHistory filters the messages broadcast to participants for the current
	// turn. Returning nil preserves the original messages.
	UpdateHistory func(ctx context.Context, messages []*message.Message) ([]*message.Message, error)

	// ShouldTerminate decides whether the chat should end. When nil, the host
	// stops after 40 participant turns.
	ShouldTerminate func(ctx context.Context, history []*message.Message, iterationCount int) (bool, error)

	// Reset clears manager-owned local state when the hosting executor is reset.
	Reset func()

	// OnCheckpoint persists manager-owned state. The supplied context prefixes
	// state keys with "GroupChatManager_" so custom manager state cannot collide
	// with host state.
	OnCheckpoint func(ctx *workflow.Context) error

	// OnCheckpointRestored restores manager-owned state from the same prefixed
	// state context used by OnCheckpoint.
	OnCheckpointRestored func(ctx *workflow.Context) error
}

// RoundRobinGroupChatOptions configures [NewRoundRobinGroupChatManager].
type RoundRobinGroupChatOptions struct {
	// MaximumIterationCount is the maximum number of participant turns before
	// the default termination behavior ends the chat. The zero value is 40.
	MaximumIterationCount int

	// ShouldTerminate can stop the chat before the default iteration-limit check
	// runs.
	ShouldTerminate func(ctx context.Context, history []*message.Message, iterationCount int) (bool, error)
}

type roundRobinGroupChatManager struct {
	manager               GroupChatManager
	agents                []*agent.Agent
	shouldTerminateFunc   func(ctx context.Context, history []*message.Message, iterationCount int) (bool, error)
	nextIndex             int
	maximumIterationCount int
}

// NewRoundRobinGroupChatManager creates a [GroupChatManager] that selects
// agents in a round-robin order.
func NewRoundRobinGroupChatManager(agents []*agent.Agent, opts RoundRobinGroupChatOptions) *GroupChatManager {
	manager := &roundRobinGroupChatManager{
		agents:                slices.Clone(agents),
		shouldTerminateFunc:   opts.ShouldTerminate,
		maximumIterationCount: opts.MaximumIterationCount,
	}
	manager.manager = GroupChatManager{
		SelectNextAgent:      manager.selectNextAgent,
		ShouldTerminate:      manager.shouldTerminate,
		Reset:                manager.reset,
		OnCheckpoint:         manager.onCheckpoint,
		OnCheckpointRestored: manager.onCheckpointRestored,
	}
	return &manager.manager
}

func (manager *roundRobinGroupChatManager) maxIterationCount() int {
	if manager == nil || manager.maximumIterationCount <= 0 {
		return defaultGroupChatMaximumIterations
	}
	return manager.maximumIterationCount
}

func (manager *roundRobinGroupChatManager) selectNextAgent(context.Context, []*message.Message) (*agent.Agent, error) {
	if manager == nil || len(manager.agents) == 0 {
		return nil, nil
	}
	if manager.nextIndex < 0 || manager.nextIndex >= len(manager.agents) {
		manager.nextIndex = 0
	}
	nextAgent := manager.agents[manager.nextIndex]
	manager.nextIndex = (manager.nextIndex + 1) % len(manager.agents)
	return nextAgent, nil
}

func (manager *roundRobinGroupChatManager) shouldTerminate(ctx context.Context, history []*message.Message, iterationCount int) (bool, error) {
	if manager != nil && manager.shouldTerminateFunc != nil {
		terminate, err := manager.shouldTerminateFunc(ctx, history, iterationCount)
		if err != nil || terminate {
			return terminate, err
		}
	}
	return iterationCount >= manager.maxIterationCount(), nil
}

func (manager *roundRobinGroupChatManager) reset() {
	if manager == nil {
		return
	}
	manager.nextIndex = 0
}

func (manager *roundRobinGroupChatManager) onCheckpoint(ctx *workflow.Context) error {
	return ctx.QueueStateUpdate(roundRobinGroupChatManagerStateKey, "", roundRobinGroupChatManagerState{NextIndex: manager.nextIndex})
}

func (manager *roundRobinGroupChatManager) onCheckpointRestored(ctx *workflow.Context) error {
	value, err := ctx.ReadState(roundRobinGroupChatManagerStateKey, "")
	if err != nil {
		return err
	}
	state, err := roundRobinGroupChatManagerStateFromAny(value)
	if err != nil {
		return err
	}
	manager.nextIndex = 0
	if state != nil {
		manager.nextIndex = state.NextIndex
	}
	if manager.nextIndex < 0 || manager.nextIndex >= len(manager.agents) {
		manager.nextIndex = 0
	}
	return nil
}

// GroupChatWorkflowBuilder fluently builds group chat workflows.
type GroupChatWorkflowBuilder struct {
	name               string
	description        string
	managerFactory     GroupChatManagerFactory
	agents             []*agent.Agent
	outputDesignations outputDesignations
	err                error
}

// NewGroupChatWorkflowBuilder creates a builder for a group chat workflow.
func NewGroupChatWorkflowBuilder(managerFactory GroupChatManagerFactory, agents ...*agent.Agent) *GroupChatWorkflowBuilder {
	return &GroupChatWorkflowBuilder{managerFactory: managerFactory, agents: slices.Clone(agents)}
}

// WithName sets the workflow name.
func (b *GroupChatWorkflowBuilder) WithName(name string) *GroupChatWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.name = name
	return b
}

// WithDescription sets the workflow description.
func (b *GroupChatWorkflowBuilder) WithDescription(description string) *GroupChatWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.description = description
	return b
}

// WithOutputFrom designates agents as terminal workflow output sources.
func (b *GroupChatWorkflowBuilder) WithOutputFrom(agents ...*agent.Agent) *GroupChatWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withOutputFrom(agents...)
	return b
}

// WithIntermediateOutputFrom designates agents as intermediate workflow output sources.
func (b *GroupChatWorkflowBuilder) WithIntermediateOutputFrom(agents ...*agent.Agent) *GroupChatWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withIntermediateOutputFrom(agents...)
	return b
}

// Build builds the group chat workflow.
func (b *GroupChatWorkflowBuilder) Build() (*workflow.Workflow, error) {
	if b == nil {
		return nil, fmt.Errorf("agentworkflow: GroupChatWorkflowBuilder is nil")
	}
	if b.err != nil {
		return nil, b.err
	}
	if err := validateBuilderAgents("GroupChatWorkflowBuilder", b.agents); err != nil {
		return nil, err
	}
	if b.managerFactory == nil {
		return nil, fmt.Errorf("agentworkflow: GroupChatWorkflowBuilder manager factory is nil")
	}

	participants := b.agents
	participantBindings := make([]workflow.ExecutorBinding, len(participants))
	bindingsByAgent := make(map[*agent.Agent]workflow.ExecutorBinding, len(participants))
	bindingsByAgentID := make(map[string]workflow.ExecutorBinding, len(participants))
	participantConfig := Config{DisableForwardIncomingMessages: true}
	for index, currentAgent := range participants {
		binding := New(currentAgent, participantConfig)
		participantBindings[index] = binding
		bindingsByAgent[currentAgent] = binding
		bindingsByAgentID[currentAgent.ID()] = binding
	}

	host := newGroupChatHostBinding(participants, participantBindings, bindingsByAgent, bindingsByAgentID, b.managerFactory)
	builder := applyBuilderMetadata(workflow.NewBuilder(host), b.name, b.description)
	for _, participant := range participantBindings {
		builder = builder.AddEdge(host, participant).AddEdge(participant, host)
	}
	var err error
	builder, err = applyOutputDesignations(builder, b.outputDesignations, bindingsByAgent, "group chat", func() {
		builder = builder.WithOutputFrom(host).WithIntermediateOutputFrom(participantBindings...)
	})
	if err != nil {
		return nil, err
	}
	return builder.Build()
}

type groupChatHostExecutor struct {
	id                string
	agents            []*agent.Agent
	participants      []workflow.ExecutorBinding
	bindingsByAgent   map[*agent.Agent]workflow.ExecutorBinding
	bindingsByAgentID map[string]workflow.ExecutorBinding
	managerFactory    GroupChatManagerFactory

	manager                  *GroupChatManager
	messageState             *messageworkflow.MessageState
	history                  []*message.Message
	currentSpeakerExecutorID string
	iterationCount           int
}

func newGroupChatHostBinding(
	agents []*agent.Agent,
	participants []workflow.ExecutorBinding,
	bindingsByAgent map[*agent.Agent]workflow.ExecutorBinding,
	bindingsByAgentID map[string]workflow.ExecutorBinding,
	managerFactory GroupChatManagerFactory,
) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:                                groupChatHostExecutorID,
		ImplementationID:                  groupChatHostExecutorID,
		SupportsConcurrentSharedExecution: true,
		NewExecutorFunc: func(string) (*workflow.Executor, error) {
			host := &groupChatHostExecutor{
				id:                groupChatHostExecutorID,
				agents:            agents,
				participants:      participants,
				bindingsByAgent:   bindingsByAgent,
				bindingsByAgentID: bindingsByAgentID,
				managerFactory:    managerFactory,
				messageState:      messageworkflow.NewMessageState(groupChatHostBufferedStateKey, ""),
			}
			return host.executor(), nil
		},
	}
}

func (host *groupChatHostExecutor) executor() *workflow.Executor {
	executor := workflow.Executor{
		ID: host.id,
		ConfigureProtocol: func(protocolBuilder *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			return protocolBuilder.
				SendsMessageType(reflect.TypeFor[[]*message.Message](), reflect.TypeFor[workflow.TurnToken]()).
				YieldsOutputType(reflect.TypeFor[[]*message.Message]()), nil
		},
	}
	messageworkflow.Configure(&executor, &messageworkflow.Options{
		StateKey:                 groupChatHostBufferedStateKey,
		TakeTurnHandler:          host.handleTurn,
		StringMessageRole:        string(message.RoleUser),
		DisableAutoSendTurnToken: true,
		MessageState:             host.messageState,
	})
	executor.Extend(&workflow.Executor{
		ResetFunc:                host.reset,
		OnCheckpointFunc:         host.onCheckpoint,
		OnCheckpointRestoredFunc: host.onCheckpointRestored,
	})
	return &executor
}

func (host *groupChatHostExecutor) handleTurn(ctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error {
	manager, err := host.ensureManager()
	if err != nil {
		return err
	}

	if len(messages) > 0 {
		host.history = append(host.history, messages...)
	}

	terminate, err := host.shouldTerminate(ctx, manager)
	if err != nil {
		return err
	}
	if terminate {
		return host.complete(ctx)
	}

	if len(messages) > 0 {
		broadcastMessages, err := host.updateHistory(ctx, manager, messages)
		if err != nil {
			return err
		}
		if len(broadcastMessages) > 0 {
			if err := host.broadcast(ctx, broadcastMessages); err != nil {
				return err
			}
		}
	}

	nextAgent, err := manager.SelectNextAgent(ctx, host.history)
	if err != nil {
		return err
	}
	if nextAgent == nil {
		return host.complete(ctx)
	}
	nextBinding, ok := host.bindingForAgent(nextAgent)
	if !ok {
		// Selecting an agent that is not a participant ends the chat and
		// yields the accumulated conversation, matching .NET GroupChatHost
		// falling through to CompleteAsync on a lookup miss.
		return host.complete(ctx)
	}

	host.iterationCount++
	host.currentSpeakerExecutorID = nextBinding.ID
	return ctx.SendMessage(nextBinding.ID, token)
}

func (host *groupChatHostExecutor) shouldTerminate(ctx context.Context, manager *GroupChatManager) (bool, error) {
	if manager.ShouldTerminate != nil {
		return manager.ShouldTerminate(ctx, host.history, host.iterationCount)
	}
	return host.iterationCount >= defaultGroupChatMaximumIterations, nil
}

func (host *groupChatHostExecutor) updateHistory(ctx context.Context, manager *GroupChatManager, messages []*message.Message) ([]*message.Message, error) {
	if manager.UpdateHistory == nil {
		return messages, nil
	}
	updated, err := manager.UpdateHistory(ctx, messages)
	if err != nil || updated != nil {
		return updated, err
	}
	return messages, nil
}

func (host *groupChatHostExecutor) broadcast(ctx *workflow.Context, messages []*message.Message) error {
	for _, participant := range host.participants {
		if participant.ID == host.currentSpeakerExecutorID {
			continue
		}
		if err := ctx.SendMessage(participant.ID, messages); err != nil {
			return err
		}
	}
	return nil
}

func (host *groupChatHostExecutor) complete(ctx *workflow.Context) error {
	output := host.history
	host.history = nil
	host.currentSpeakerExecutorID = ""
	host.manager = nil
	host.iterationCount = 0
	if err := host.messageState.Reset(); err != nil {
		return err
	}
	return ctx.YieldOutput(output)
}

func (host *groupChatHostExecutor) reset() error {
	if host.manager != nil && host.manager.Reset != nil {
		host.manager.Reset()
	}
	host.manager = nil
	host.history = nil
	host.currentSpeakerExecutorID = ""
	host.iterationCount = 0
	return nil
}

func (host *groupChatHostExecutor) ensureManager() (*GroupChatManager, error) {
	if host.manager != nil {
		return host.manager, nil
	}
	manager := host.managerFactory(slices.Clone(host.agents))
	if manager == nil {
		return nil, fmt.Errorf("agentworkflow: group chat manager factory returned nil")
	}
	if manager.SelectNextAgent == nil {
		return nil, fmt.Errorf("agentworkflow: group chat manager SelectNextAgent is nil")
	}
	host.manager = manager
	return manager, nil
}

func (host *groupChatHostExecutor) bindingForAgent(currentAgent *agent.Agent) (workflow.ExecutorBinding, bool) {
	if currentAgent == nil {
		return workflow.ExecutorBinding{}, false
	}
	if binding, ok := host.bindingsByAgent[currentAgent]; ok {
		return binding, true
	}
	if binding, ok := host.bindingsByAgentID[currentAgent.ID()]; ok {
		return binding, true
	}
	return workflow.ExecutorBinding{}, false
}

func (host *groupChatHostExecutor) onCheckpoint(ctx *workflow.Context) error {
	if err := ctx.QueueStateUpdate(groupChatHistoryStateKey, "", host.history); err != nil {
		return err
	}
	if err := ctx.QueueStateUpdate(groupChatCurrentSpeakerKey, "", host.currentSpeakerExecutorID); err != nil {
		return err
	}
	manager, err := host.ensureManager()
	if err != nil {
		return err
	}
	return checkpointGroupChatManager(ctx, manager, host.iterationCount)
}

func (host *groupChatHostExecutor) onCheckpointRestored(ctx *workflow.Context) error {
	history, err := readMessageSliceState(ctx, groupChatHistoryStateKey)
	if err != nil {
		return err
	}
	host.history = history

	currentSpeaker, err := readStringState(ctx, groupChatCurrentSpeakerKey)
	if err != nil {
		return err
	}
	host.currentSpeakerExecutorID = currentSpeaker

	manager, err := host.ensureManager()
	if err != nil {
		return err
	}
	iterationCount, err := restoreGroupChatManagerCheckpoint(ctx, manager)
	if err != nil {
		return err
	}
	host.iterationCount = iterationCount
	return nil
}

func checkpointGroupChatManager(ctx *workflow.Context, manager *GroupChatManager, iterationCount int) error {
	if err := ctx.QueueStateUpdate(groupChatManagerStateKey, "", groupChatManagerState{IterationCount: iterationCount}); err != nil {
		return err
	}
	if manager.OnCheckpoint != nil {
		return manager.OnCheckpoint(prefixingWorkflowContext(ctx, groupChatManagerSubclassStateKeyPref))
	}
	return nil
}

func restoreGroupChatManagerCheckpoint(ctx *workflow.Context, manager *GroupChatManager) (int, error) {
	value, err := ctx.ReadState(groupChatManagerStateKey, "")
	if err != nil {
		return 0, err
	}
	state, err := groupChatManagerStateFromAny(value)
	if err != nil {
		return 0, err
	}
	if manager.OnCheckpointRestored != nil {
		if err := manager.OnCheckpointRestored(prefixingWorkflowContext(ctx, groupChatManagerSubclassStateKeyPref)); err != nil {
			return 0, err
		}
	}
	if state == nil {
		return 0, nil
	}
	return state.IterationCount, nil
}

func prefixingWorkflowContext(inner *workflow.Context, prefix string) *workflow.Context {
	wrapped := *inner

	if inner.ReadState != nil {
		wrapped.ReadState = func(key string, scope string) (any, error) {
			return inner.ReadState(prefixWorkflowStateKey(prefix, key), scope)
		}
	}
	if inner.ReadOrInitState != nil {
		wrapped.ReadOrInitState = func(key string, scope string, initFunc func(context.Context, string, string) (any, error)) (any, error) {
			return inner.ReadOrInitState(prefixWorkflowStateKey(prefix, key), scope, func(ctx context.Context, wrappedKey string, wrappedScope string) (any, error) {
				if initFunc == nil {
					return nil, nil
				}
				return initFunc(ctx, key, wrappedScope)
			})
		}
	}
	if inner.ReadStateKeys != nil {
		wrapped.ReadStateKeys = func(scope string) iter.Seq2[string, error] {
			return func(yield func(string, error) bool) {
				for key, err := range inner.ReadStateKeys(scope) {
					if err != nil {
						if !yield("", err) {
							return
						}
						continue
					}
					if strings.HasPrefix(key, prefix) {
						if !yield(strings.TrimPrefix(key, prefix), nil) {
							return
						}
					}
				}
			}
		}
	}
	if inner.QueueStateUpdate != nil {
		wrapped.QueueStateUpdate = func(key string, scope string, value any) error {
			return inner.QueueStateUpdate(prefixWorkflowStateKey(prefix, key), scope, value)
		}
	}
	if inner.QueueClearScope != nil && inner.ReadStateKeys != nil && inner.QueueStateUpdate != nil {
		wrapped.QueueClearScope = func(scope string) error {
			for key, err := range inner.ReadStateKeys(scope) {
				if err != nil {
					return err
				}
				if strings.HasPrefix(key, prefix) {
					if err := inner.QueueStateUpdate(key, scope, nil); err != nil {
						return err
					}
				}
			}
			return nil
		}
	}
	return &wrapped
}

func prefixWorkflowStateKey(prefix string, key string) string {
	return prefix + key
}

func readMessageSliceState(ctx *workflow.Context, key string) ([]*message.Message, error) {
	value, err := ctx.ReadState(key, "")
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	if messages, ok := value.([]*message.Message); ok {
		return messages, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var messages []*message.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func readStringState(ctx *workflow.Context, key string) (string, error) {
	value, err := ctx.ReadState(key, "")
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return "", err
	}
	return text, nil
}

type groupChatManagerState struct {
	IterationCount int
}

func groupChatManagerStateFromAny(value any) (*groupChatManagerState, error) {
	if value == nil {
		return nil, nil
	}
	switch state := value.(type) {
	case groupChatManagerState:
		return &state, nil
	case *groupChatManagerState:
		return state, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var state groupChatManagerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

type roundRobinGroupChatManagerState struct {
	NextIndex int
}

func roundRobinGroupChatManagerStateFromAny(value any) (*roundRobinGroupChatManagerState, error) {
	if value == nil {
		return nil, nil
	}
	switch state := value.(type) {
	case roundRobinGroupChatManagerState:
		return &state, nil
	case *roundRobinGroupChatManagerState:
		return state, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var state roundRobinGroupChatManagerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
