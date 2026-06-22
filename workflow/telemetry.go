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
	Executors       map[string]string      `json:"executors"`
	Edges           map[string][]EdgeInfo  `json:"edges"`
	RequestPorts    []RequestPortInfo      `json:"requestPorts"`
	StartExecutorID string                 `json:"startExecutorId"`
	OutputExecutors map[string][]OutputTag `json:"outputExecutors,omitempty"`
}

func workflowTelemetryDefinitionFrom(wf *Workflow) workflowTelemetryDefinition {
	if wf == nil {
		return workflowTelemetryDefinition{}
	}
	executors := make(map[string]string, len(wf.executorBindings))
	for id, binding := range wf.executorBindings {
		executors[id] = binding.ImplementationID
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
	outputExecutors := make(map[string][]OutputTag, len(wf.outputExecutors))
	for id, tags := range wf.outputExecutors {
		outputExecutors[id] = outputTagsFromSet(tags)
	}
	return workflowTelemetryDefinition{
		Executors:       executors,
		Edges:           wf.ReflectEdges(),
		RequestPorts:    ports,
		StartExecutorID: wf.startExecutorID,
		OutputExecutors: outputExecutors,
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
