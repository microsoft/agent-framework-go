// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"slices"

	internalobservability "github.com/microsoft/agent-framework-go/workflow/internal/observability"
	workflowobservability "github.com/microsoft/agent-framework-go/workflow/observability"
)

// TelemetryOptions configures workflow telemetry instrumentation.
type TelemetryOptions struct {
	// EnableSensitiveData includes serialized message inputs, outputs, and
	// message contents in span attributes. It is disabled by default.
	EnableSensitiveData bool

	DisableWorkflowBuild    bool
	DisableWorkflowRun      bool
	DisableExecutorProcess  bool
	DisableEdgeGroupProcess bool
	DisableMessageSend      bool
}

func (o TelemetryOptions) observabilityOptions(tracer workflowobservability.Tracer) internalobservability.Options {
	return internalobservability.Options{
		Tracer:                  tracer,
		EnableSensitiveData:     o.EnableSensitiveData,
		DisableWorkflowBuild:    o.DisableWorkflowBuild,
		DisableWorkflowRun:      o.DisableWorkflowRun,
		DisableExecutorProcess:  o.DisableExecutorProcess,
		DisableEdgeGroupProcess: o.DisableEdgeGroupProcess,
		DisableMessageSend:      o.DisableMessageSend,
	}
}

type workflowTelemetryDefinition struct {
	Executors         map[string]TypeID     `json:"executors"`
	Edges             map[string][]EdgeInfo `json:"edges"`
	RequestPorts      []RequestPortInfo     `json:"requestPorts"`
	StartExecutorID   string                `json:"startExecutorId"`
	OutputExecutorIDs []string              `json:"outputExecutorIds,omitempty"`
}

func workflowTelemetryDefinitionFrom(wf *Workflow) workflowTelemetryDefinition {
	if wf == nil {
		return workflowTelemetryDefinition{}
	}
	executors := make(map[string]TypeID, len(wf.executorBindings))
	for id, binding := range wf.executorBindings {
		executors[id] = NewTypeID(binding.ExecutorType)
	}
	ports := make([]RequestPortInfo, 0, len(wf.ports))
	for _, port := range wf.ports {
		ports = append(ports, NewRequestPortInfo(port))
	}
	slices.SortFunc(ports, func(left, right RequestPortInfo) int {
		if left.PortID < right.PortID {
			return -1
		}
		if left.PortID > right.PortID {
			return 1
		}
		return 0
	})
	outputs := make([]string, 0, len(wf.outputExecutors))
	for id := range wf.outputExecutors {
		outputs = append(outputs, id)
	}
	slices.Sort(outputs)
	return workflowTelemetryDefinition{
		Executors:         executors,
		Edges:             wf.ReflectEdges(),
		RequestPorts:      ports,
		StartExecutorID:   wf.startExecutorID,
		OutputExecutorIDs: outputs,
	}
}

func observabilityMetadata(wf *Workflow, sessionID string) internalobservability.WorkflowMetadata {
	if wf == nil {
		return internalobservability.WorkflowMetadata{SessionID: sessionID}
	}
	return internalobservability.WorkflowMetadata{
		ID:          wf.startExecutorID,
		Name:        wf.name,
		Description: wf.description,
		SessionID:   sessionID,
	}
}
