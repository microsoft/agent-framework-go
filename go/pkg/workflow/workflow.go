// Copyright (c) Microsoft. All rights reserved.

package workflow

import "context"

// Workflow represents a graph-based orchestration of agents and executors.
type Workflow interface {
	// ID returns the unique identifier.
	ID() string

	// Name returns the name.
	Name() string

	// Execute runs the workflow with the given input.
	Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)

	// Validate checks if the workflow is properly configured.
	Validate() error
}

// WorkflowBuilder helps construct workflows.
type WorkflowBuilder interface {
	// AddExecutor adds an executor node to the workflow.
	AddExecutor(id string, executor Executor) WorkflowBuilder

	// AddEdge adds a connection between executors.
	AddEdge(edge Edge) WorkflowBuilder

	// SetEntryPoint sets the starting executor.
	SetEntryPoint(executorID string) WorkflowBuilder

	// Build creates the workflow.
	Build() (Workflow, error)
}

// Executor represents a node in the workflow graph.
type Executor interface {
	// ID returns the unique identifier.
	ID() string

	// Name returns the name.
	Name() string

	// Execute processes the input and returns output.
	Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)
}

// Edge represents a connection between workflow nodes.
type Edge struct {
	// From is the source executor ID.
	From string

	// To is the target executor ID.
	To string

	// Condition is an optional function that determines if the edge should be followed.
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
type WorkflowContext[M ~string | any] struct {
	WorkflowID string
	Messages   []M
	State      map[string]interface{}
}
