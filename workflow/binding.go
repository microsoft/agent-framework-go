// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
	"fmt"
)

// ExecutorBinding is the graph and session registration for an executor ID. It
// records the workflow address, implementation identity, and how a runner
// obtains an executable [Executor] instance for a workflow session.
//
// An Executor contains executable behavior: routes, lifecycle hooks, and local
// state. An ExecutorBinding is the stable handle that builders store on graph
// edges and runners use to create or reuse an Executor. A binding may wrap a
// shared Executor instance, create a fresh one per session, or act as a
// placeholder while a graph is being built.
type ExecutorBinding struct {
	// ID is the workflow-unique identifier for the executor. It is also the
	// address used by edges and messages inside the workflow graph.
	ID string

	// ImplementationID identifies the binding implementation or semantic executor source for
	// diagnostics, validation, and workflow metadata.
	ImplementationID string

	// RawValue optionally carries the comparable source value behind this binding.
	// When the builder sees another binding with the same [ID] and [ImplementationID],
	// it compares RawValue to catch accidental reuse of the [ID] for a different
	// source value. RawValue must be nil or comparable; the builder rejects
	// non-comparable values. Leave it nil for sources such as function values.
	RawValue any

	// SharedInstance reports whether [NewExecutorFunc] returns a shared executor
	// instance rather than creating an independent instance for each session.
	// Shared instances participate in workflow reset checks through [Reset]. Keep
	// this value consistent with [NewExecutorFunc]; setting it to false for a
	// shared executor opts out of reset checks.
	SharedInstance bool

	// SupportsConcurrentSharedExecution reports whether this binding may be used
	// by concurrent workflow runs. Bindings produced by [Executor.Bind] copy this
	// value from [Executor.CrossRunShareable]. Factory bindings set it directly.
	SupportsConcurrentSharedExecution bool

	// Ports lists [RequestPort]s that this binding exposes, either as the
	// workflow boundary port created by [RequestPort.Bind] or as additional ports
	// an executor uses to raise [ExternalRequest]s via [Context.PostRequest]. The
	// builder registers them so they appear in [Workflow.ReflectPorts] metadata.
	Ports []RequestPort

	// NewExecutorFunc creates the executor instance for a workflow session. The
	// returned executor must have the same ID as this binding; [CreateInstance]
	// validates that contract and records this binding's implementation ID on the
	// instance when it does not already have one.
	NewExecutorFunc func(sessionID string) (*Executor, error)

	// ResetFunc restores shared resources to their initial state. It is only called
	// for bindings marked as [SharedInstance]. A false return value means the
	// workflow could not be reset safely.
	ResetFunc func() bool
}

func bindingImplementationID(name string) string {
	if name == "" {
		return "<unknown>"
	}
	return name
}

// String returns a compact representation of the binding for diagnostics.
func (eb ExecutorBinding) String() string {
	if eb.isPlaceholder() {
		return eb.ID + ":<unbound>"
	}
	return eb.ID + ":" + bindingImplementationID(eb.inferredImplementationID())
}

// isPlaceholder reports whether this binding only reserves an executor ID.
func (eb ExecutorBinding) isPlaceholder() bool {
	return eb.NewExecutorFunc == nil
}

// TryReset resets this binding if it wraps a shared executor instance.
// Non-shared bindings are already isolated per session and therefore report
// success without invoking [ExecutorBinding.Reset].
func (eb ExecutorBinding) TryReset() bool {
	if !eb.SharedInstance {
		// Non-shared instances do not need resetting
		return true
	}
	if eb.ResetFunc == nil {
		return false
	}
	return eb.ResetFunc()
}

// CreateInstance creates the executor for sessionID and validates that the
// returned executor matches this binding. It returns an error for placeholder
// bindings, nil executors, factory errors, or executor ID mismatches.
func (eb ExecutorBinding) CreateInstance(sessionID string) (*Executor, error) {
	if eb.isPlaceholder() {
		return nil, errors.New("cannot create executor from placeholder binding")
	}
	implementationID := eb.inferredImplementationID()
	ex, err := eb.NewExecutorFunc(sessionID)
	if err != nil {
		return nil, err
	}
	if ex == nil {
		return nil, errors.New("executor binding returned nil executor")
	}
	if ex.ID != eb.ID {
		return nil, fmt.Errorf("Executor ID mismatch: expected %q, but got %q", eb.ID, ex.ID)
	}
	if ex.ImplementationID == "" {
		ex.ImplementationID = implementationID
	} else if ex.ImplementationID != implementationID {
		return nil, fmt.Errorf("Executor implementation ID mismatch: expected %q, but got %q", implementationID, ex.ImplementationID)
	}
	return ex, nil
}

func (eb ExecutorBinding) inferredImplementationID() string {
	if eb.ImplementationID != "" || eb.NewExecutorFunc == nil {
		return eb.ImplementationID
	}
	return bindFuncImplementationID(eb.ID, eb.NewExecutorFunc)
}

func (eb ExecutorBinding) withInferredImplementationID() ExecutorBinding {
	if eb.ImplementationID == "" {
		eb.ImplementationID = eb.inferredImplementationID()
	}
	return eb
}

// Bind returns an [ExecutorBinding] for e.
//
// Calling Bind multiple times on the same executor is fine: each returned
// binding has the same ID and implementation identity and returns the same
// executor instance. Bind does not clone e.
func (e *Executor) Bind() ExecutorBinding {
	if e == nil {
		panic("workflow: cannot bind nil Executor")
	}
	if e.ImplementationID == "" {
		panic("workflow: cannot bind Executor with empty implementation ID")
	}

	binding := ExecutorBinding{
		ID:                                e.ID,
		ImplementationID:                  e.ImplementationID,
		RawValue:                          e,
		SharedInstance:                    true,
		SupportsConcurrentSharedExecution: e.CrossRunShareable,
		NewExecutorFunc: func(string) (*Executor, error) {
			return e, nil
		},
	}
	if e.ResetFunc != nil {
		binding.ResetFunc = func() bool {
			return e.Reset() == nil
		}
	}
	return binding
}

// BindNewExecutorFunc returns an [ExecutorBinding] that creates a new
// [Executor] for each workflow session by calling fn with the session ID and
// executor ID.
// Created executors are validated against id and stamped with the binding's
// implementation identity.
//
// The returned binding is non-shared, so executor-local state does not require
// a reset hook between runs. It is not marked safe for concurrent workflow runs
// by default; set [ExecutorBinding.SupportsConcurrentSharedExecution] to true
// only when the factory and the executors it creates are safe for that use.
func BindNewExecutorFunc(id string, fn func(sessionID string, executorID string) (*Executor, error)) ExecutorBinding {
	if fn == nil {
		panic("workflow: cannot bind nil new executor function")
	}
	implementationID := bindFuncImplementationID(id, fn)
	return ExecutorBinding{
		ID:               id,
		ImplementationID: implementationID,
		NewExecutorFunc: func(sessionID string) (*Executor, error) {
			executor, err := fn(sessionID, id)
			if err != nil {
				return executor, err
			}
			if executor == nil {
				return nil, errors.New("executor binding returned nil executor")
			}
			if executor.ID != id {
				return nil, fmt.Errorf("Executor ID mismatch: expected %q, but got %q", id, executor.ID)
			}
			executor.ImplementationID = implementationID
			return executor, nil
		},
	}
}

// Bind returns an [ExecutorBinding] that exposes p at the workflow boundary.
// The resulting executor accepts messages of p.Request type, raises them as
// [ExternalRequest]s via
// [Context.PostRequest], and forwards the matching [ExternalResponse] data to
// downstream executors.
//
// Calling Bind multiple times on the same port is fine: each returned binding
// has the same ID and port metadata, and creates request-port executor
// instances from that port.
func (p RequestPort) Bind() ExecutorBinding {
	return ExecutorBinding{
		ID:               p.ID,
		ImplementationID: "workflow.RequestPort.Bind",
		RawValue:         p,
		Ports:            []RequestPort{p},
		NewExecutorFunc: func(_ string) (*Executor, error) {
			return newRequestPortExecutor(p), nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}
