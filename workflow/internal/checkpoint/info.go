// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"reflect"

	"github.com/microsoft/agent-framework-go/workflow"
)

type executorInfo struct {
	Type       workflow.TypeID
	ExecutorID string
}

func (e *executorInfo) matchType(typ reflect.Type) bool {
	return e.Type.Match(typ)
}

func (e *executorInfo) matchBinding(executor workflow.ExecutorBinding) bool {
	return e.ExecutorID == executor.ID && e.matchType(executor.ExecutorType)
}

type WorkflowInfo struct {
	Executors         map[string]executorInfo
	Edges             map[string][]workflow.EdgeInfo
	RequestPorts      map[string]workflow.RequestPortInfo
	StartExecutorID   string
	OutputExecutorIDs map[string]struct{}
}

func NewWorkflowInfo(wf *workflow.Workflow) WorkflowInfo {
	executors := make(map[string]executorInfo, len(wf.ExecutorBindings))
	for id, binding := range wf.ExecutorBindings {
		executors[id] = executorInfo{
			Type:       workflow.NewTypeID(binding.ExecutorType),
			ExecutorID: binding.ID,
		}
	}

	edges := wf.ReflectEdges()

	requestPorts := make(map[string]workflow.RequestPortInfo, len(wf.Ports))
	for _, port := range wf.Ports {
		info := workflow.NewRequestPortInfo(port)
		requestPorts[info.PortID] = info
	}

	outputs := make(map[string]struct{}, len(wf.OutputExecutors))
	for id := range wf.OutputExecutors {
		outputs[id] = struct{}{}
	}

	return WorkflowInfo{
		Executors:         executors,
		Edges:             edges,
		RequestPorts:      requestPorts,
		StartExecutorID:   wf.StartExecutorID,
		OutputExecutorIDs: outputs,
	}
}

func (w *WorkflowInfo) Match(wf *workflow.Workflow) bool {
	if wf == nil {
		return false
	}
	if w.StartExecutorID != wf.StartExecutorID {
		return false
	}
	if len(wf.ExecutorBindings) != len(w.Executors) {
		return false
	}
	// Validate the executors
	for _, eb := range w.Executors {
		binding, ok := wf.ExecutorBindings[eb.ExecutorID]
		if !ok || !eb.matchBinding(binding) {
			return false
		}
	}
	// Validate the edges
	if len(wf.Edges) != len(w.Edges) {
		return false
	}
	for sourceID, edges := range w.Edges {
		other, ok := wf.Edges[sourceID]
		if !ok || len(other) != len(edges) {
			return false
		}
		for i, edge := range edges {
			if !edge.Match(other[i]) {
				return false
			}
		}
	}

	// Validate the request ports
	if len(wf.Ports) != len(w.RequestPorts) {
		return false
	}
	for _, port := range w.RequestPorts {
		other, ok := wf.Ports[port.PortID]
		if !ok {
			return false
		}
		if !port.RequestType.Match(other.Request) || !port.ResponseType.Match(other.Response) {
			return false
		}
	}

	// Validate the outputs
	if len(wf.OutputExecutors) != len(w.OutputExecutorIDs) {
		return false
	}
	for outputID := range w.OutputExecutorIDs {
		if _, ok := wf.OutputExecutors[outputID]; !ok {
			return false
		}
	}
	return true
}
