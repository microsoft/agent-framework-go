// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// Workflow represents a graph-based orchestration of agents and executors.
// It runs a directed graph of executors connected via edges using a Pregel-like model.
type Workflow interface {
	// ID returns the unique identifier.
	ID() string

	// Name returns the name.
	Name() string

	// Description returns the description.
	Description() string

	// Execute runs the workflow with the given input and returns output.
	Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)

	// ExecuteStream runs the workflow and yields updates via a channel.
	ExecuteStream(ctx context.Context, input map[string]interface{}) <-chan WorkflowUpdate

	// Validate checks if the workflow is properly configured.
	Validate() error

	// GetStartExecutor returns the start executor for the workflow.
	GetStartExecutor() Executor

	// GetExecutor returns an executor by ID.
	GetExecutor(id string) (Executor, error)

	// GetExecutors returns all executors in the workflow.
	GetExecutors() map[string]Executor

	// GetEdges returns all edges in the workflow.
	GetEdges() []Edge
}

// WorkflowImpl is the concrete implementation of Workflow.
type WorkflowImpl struct {
	id              string
	name            string
	description     string
	startExecutorID string
	executors       map[string]Executor
	edges           []Edge
	maxIterations   int
	mu              sync.RWMutex
}

// WorkflowUpdate represents a single update from workflow execution.
type WorkflowUpdate struct {
	Output map[string]interface{}
	Error  error
	Done   bool
}

// WorkflowBuilder helps construct workflows.
type WorkflowBuilder interface {
	// AddExecutor adds an executor node to the workflow.
	AddExecutor(id string, executor Executor) WorkflowBuilder

	// AddEdge adds a connection between executors.
	AddEdge(edge Edge) WorkflowBuilder

	// SetEntryPoint sets the starting executor.
	SetEntryPoint(executorID string) WorkflowBuilder

	// SetName sets the name of the workflow.
	SetName(name string) WorkflowBuilder

	// SetDescription sets the description of the workflow.
	SetDescription(description string) WorkflowBuilder

	// SetMaxIterations sets the maximum number of iterations.
	SetMaxIterations(max int) WorkflowBuilder

	// Build creates the workflow.
	Build() (Workflow, error)
}

// WorkflowBuilderImpl is the concrete implementation of WorkflowBuilder.
type WorkflowBuilderImpl struct {
	name            string
	description     string
	startExecutorID string
	executors       map[string]Executor
	edges           []Edge
	maxIterations   int
}

// Executor represents a node in the workflow graph.
type Executor interface {
	// ID returns the unique identifier.
	ID() string

	// Name returns the name.
	Name() string

	// Execute processes the input and returns output.
	// The context should contain WorkflowContext in its values.
	Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)
}

// Edge represents a connection between workflow nodes.
type Edge struct {
	// From is the source executor ID.
	From string

	// To is the target executor ID.
	To string

	// Condition is an optional function that determines if the edge should be followed.
	// If nil, the edge is always followed.
	Condition func(output map[string]interface{}) bool
}

// CheckpointStorage persists workflow state for resumption.
type CheckpointStorage interface {
	// Save persists the workflow state.
	Save(ctx context.Context, workflowID string, state map[string]interface{}) error

	// Load retrieves the workflow state.
	Load(ctx context.Context, workflowID string) (map[string]interface{}, error)

	// Delete removes the workflow state.
	Delete(ctx context.Context, workflowID string) error
}

// WorkflowContext contains execution context for a workflow.
type WorkflowContext struct {
	WorkflowID  string
	ExecutorID  string
	State       map[string]interface{}
	SharedState map[string]interface{}
	Iteration   int
	mu          sync.RWMutex
}

// SetState sets a value in the workflow context state.
func (wc *WorkflowContext) SetState(key string, value interface{}) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	wc.State[key] = value
}

// GetState gets a value from the workflow context state.
func (wc *WorkflowContext) GetState(key string) (interface{}, bool) {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	val, ok := wc.State[key]
	return val, ok
}

// SetSharedState sets a value in the shared state.
func (wc *WorkflowContext) SetSharedState(key string, value interface{}) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	wc.SharedState[key] = value
}

// GetSharedState gets a value from the shared state.
func (wc *WorkflowContext) GetSharedState(key string) (interface{}, bool) {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	val, ok := wc.SharedState[key]
	return val, ok
}

// NewWorkflowBuilder creates a new workflow builder.
func NewWorkflowBuilder() WorkflowBuilder {
	return &WorkflowBuilderImpl{
		executors:     make(map[string]Executor),
		edges:         []Edge{},
		maxIterations: 100,
	}
}

// AddExecutor adds an executor node to the workflow.
func (b *WorkflowBuilderImpl) AddExecutor(id string, executor Executor) WorkflowBuilder {
	b.executors[id] = executor
	return b
}

// AddEdge adds a connection between executors.
func (b *WorkflowBuilderImpl) AddEdge(edge Edge) WorkflowBuilder {
	b.edges = append(b.edges, edge)
	return b
}

// SetEntryPoint sets the starting executor.
func (b *WorkflowBuilderImpl) SetEntryPoint(executorID string) WorkflowBuilder {
	b.startExecutorID = executorID
	return b
}

// SetName sets the name of the workflow.
func (b *WorkflowBuilderImpl) SetName(name string) WorkflowBuilder {
	b.name = name
	return b
}

// SetDescription sets the description of the workflow.
func (b *WorkflowBuilderImpl) SetDescription(description string) WorkflowBuilder {
	b.description = description
	return b
}

// SetMaxIterations sets the maximum number of iterations.
func (b *WorkflowBuilderImpl) SetMaxIterations(max int) WorkflowBuilder {
	b.maxIterations = max
	return b
}

// Build creates the workflow.
func (b *WorkflowBuilderImpl) Build() (Workflow, error) {
	// Validate that executors are provided
	if len(b.executors) == 0 {
		return nil, fmt.Errorf("workflow must have at least one executor")
	}

	// Validate that a start executor is specified
	if b.startExecutorID == "" {
		return nil, fmt.Errorf("workflow must have a start executor")
	}

	// Validate that the start executor exists
	if _, ok := b.executors[b.startExecutorID]; !ok {
		return nil, fmt.Errorf("start executor %s not found", b.startExecutorID)
	}

	// Validate edges
	for _, edge := range b.edges {
		if _, ok := b.executors[edge.From]; !ok {
			return nil, fmt.Errorf("source executor %s not found in edge", edge.From)
		}
		if _, ok := b.executors[edge.To]; !ok {
			return nil, fmt.Errorf("target executor %s not found in edge", edge.To)
		}
	}

	return &WorkflowImpl{
		id:              uuid.New().String(),
		name:            b.name,
		description:     b.description,
		startExecutorID: b.startExecutorID,
		executors:       b.executors,
		edges:           b.edges,
		maxIterations:   b.maxIterations,
	}, nil
}

// ID returns the unique identifier.
func (w *WorkflowImpl) ID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.id
}

// Name returns the name.
func (w *WorkflowImpl) Name() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.name
}

// Description returns the description.
func (w *WorkflowImpl) Description() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.description
}

// GetExecutor returns an executor by ID.
func (w *WorkflowImpl) GetExecutor(id string) (Executor, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	executor, ok := w.executors[id]
	if !ok {
		return nil, fmt.Errorf("executor %s not found", id)
	}
	return executor, nil
}

// GetExecutors returns all executors in the workflow.
func (w *WorkflowImpl) GetExecutors() map[string]Executor {
	w.mu.RLock()
	defer w.mu.RUnlock()
	executors := make(map[string]Executor, len(w.executors))
	for k, v := range w.executors {
		executors[k] = v
	}
	return executors
}

// GetEdges returns all edges in the workflow.
func (w *WorkflowImpl) GetEdges() []Edge {
	w.mu.RLock()
	defer w.mu.RUnlock()
	edges := make([]Edge, len(w.edges))
	copy(edges, w.edges)
	return edges
}

// GetStartExecutor returns the start executor for the workflow.
func (w *WorkflowImpl) GetStartExecutor() Executor {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.executors[w.startExecutorID]
}

// Validate checks if the workflow is properly configured.
func (w *WorkflowImpl) Validate() error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Check executors
	if len(w.executors) == 0 {
		return fmt.Errorf("workflow has no executors")
	}

	// Check start executor
	if w.startExecutorID == "" {
		return fmt.Errorf("workflow has no start executor")
	}

	if _, ok := w.executors[w.startExecutorID]; !ok {
		return fmt.Errorf("start executor %s not found", w.startExecutorID)
	}

	// Check edges reference valid executors
	for _, edge := range w.edges {
		if _, ok := w.executors[edge.From]; !ok {
			return fmt.Errorf("source executor %s not found in edge", edge.From)
		}
		if _, ok := w.executors[edge.To]; !ok {
			return fmt.Errorf("target executor %s not found in edge", edge.To)
		}
	}

	return nil
}

// Execute runs the workflow with the given input and returns output.
func (w *WorkflowImpl) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}

	// Create workflow context
	wfCtx := &WorkflowContext{
		WorkflowID:  w.ID(),
		State:       make(map[string]interface{}),
		SharedState: make(map[string]interface{}),
		Iteration:   0,
	}

	// Start execution with the start executor
	startExecutor := w.GetStartExecutor()
	result := input

	// Build a map of edges for quick lookup
	edgeMap := make(map[string][]string) // From executor ID -> list of To executor IDs
	for _, edge := range w.GetEdges() {
		edgeMap[edge.From] = append(edgeMap[edge.From], edge.To)
	}

	// Execute using a simple iteration model
	currentExecutor := startExecutor
	for iteration := 0; iteration < w.maxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		wfCtx.ExecutorID = currentExecutor.ID()
		wfCtx.Iteration = iteration

		// Create a context with the workflow context
		executorCtx := context.WithValue(ctx, WorkflowContextKey, wfCtx)

		// Execute current executor
		output, err := currentExecutor.Execute(executorCtx, result)
		if err != nil {
			return nil, fmt.Errorf("executor %s failed: %w", currentExecutor.ID(), err)
		}

		result = output

		// Determine next executor based on edges
		nextExecutorID := ""
		if targets, ok := edgeMap[currentExecutor.ID()]; ok && len(targets) > 0 {
			// For now, follow the first edge that satisfies its condition
			for _, targetID := range targets {
				// Find the edge to check its condition
				for _, edge := range w.GetEdges() {
					if edge.From == currentExecutor.ID() && edge.To == targetID {
						if edge.Condition == nil || edge.Condition(result) {
							nextExecutorID = targetID
							break
						}
					}
				}
				if nextExecutorID != "" {
					break
				}
			}
		}

		// If no next executor, we're done
		if nextExecutorID == "" {
			break
		}

		nextExec, err := w.GetExecutor(nextExecutorID)
		if err != nil {
			return nil, err
		}
		currentExecutor = nextExec
	}

	return result, nil
}

// ExecuteStream runs the workflow and yields updates via a channel.
func (w *WorkflowImpl) ExecuteStream(ctx context.Context, input map[string]interface{}) <-chan WorkflowUpdate {
	updates := make(chan WorkflowUpdate)

	go func() {
		defer close(updates)

		result, err := w.Execute(ctx, input)
		if err != nil {
			updates <- WorkflowUpdate{Error: err, Done: true}
			return
		}

		updates <- WorkflowUpdate{Output: result, Done: true}
	}()

	return updates
}

// WorkflowContextKey is the key used to store WorkflowContext in the context.
type contextKey string

const WorkflowContextKey contextKey = "workflow-context"

// GetWorkflowContext retrieves the WorkflowContext from the given context.
func GetWorkflowContext(ctx context.Context) (*WorkflowContext, bool) {
	wfCtx, ok := ctx.Value(WorkflowContextKey).(*WorkflowContext)
	return wfCtx, ok
}
