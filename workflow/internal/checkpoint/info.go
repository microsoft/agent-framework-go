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
	bindings := wf.ReflectExecutors()
	executors := make(map[string]executorInfo, len(bindings))
	for id, binding := range bindings {
		executors[id] = executorInfo{
			Type:       workflow.NewTypeID(binding.ExecutorType),
			ExecutorID: binding.ID,
		}
	}

	edges := wf.ReflectEdges()

	ports := wf.RequestPorts()
	requestPorts := make(map[string]workflow.RequestPortInfo, len(ports))
	for _, port := range ports {
		info := workflow.NewRequestPortInfo(port)
		requestPorts[info.PortID] = info
	}

	outputIDs := wf.OutputExecutorIDs()
	outputs := make(map[string]struct{}, len(outputIDs))
	for _, id := range outputIDs {
		outputs[id] = struct{}{}
	}

	return WorkflowInfo{
		Executors:         executors,
		Edges:             edges,
		RequestPorts:      requestPorts,
		StartExecutorID:   wf.StartExecutorID(),
		OutputExecutorIDs: outputs,
	}
}

func (w *WorkflowInfo) Match(wf *workflow.Workflow) bool {
	if wf == nil {
		return false
	}
	if w.StartExecutorID != wf.StartExecutorID() {
		return false
	}
	bindings := wf.ReflectExecutors()
	if len(bindings) != len(w.Executors) {
		return false
	}
	// Validate the executors
	for _, eb := range w.Executors {
		binding, ok := bindings[eb.ExecutorID]
		if !ok || !eb.matchBinding(binding) {
			return false
		}
	}
	// Validate the edges
	workflowEdges := wf.Edges()
	if len(workflowEdges) != len(w.Edges) {
		return false
	}
	for sourceID, edges := range w.Edges {
		other, ok := workflowEdges[sourceID]
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
	ports := wf.RequestPorts()
	if len(ports) != len(w.RequestPorts) {
		return false
	}
	for _, port := range w.RequestPorts {
		other, ok := ports[port.PortID]
		if !ok {
			return false
		}
		if !port.RequestType.Match(other.Request) || !port.ResponseType.Match(other.Response) {
			return false
		}
	}

	// Validate the outputs
	outputIDs := wf.OutputExecutorIDs()
	if len(outputIDs) != len(w.OutputExecutorIDs) {
		return false
	}
	for outputID := range w.OutputExecutorIDs {
		if !wf.HasOutputExecutor(outputID) {
			return false
		}
	}
	return true
}
