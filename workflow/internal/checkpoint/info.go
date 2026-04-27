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

func (e *executorInfo) matchBinding(executor *workflow.ExecutorBinding) bool {
	return e.ExecutorID == executor.ID && e.matchType(executor.ExecutorType)
}

type WorkflowInfo struct {
	executors         map[string]executorInfo
	edges             map[string][]workflow.EdgeInfo
	requestPorts      map[workflow.RequestPortInfo]struct{}
	startExecutorID   string
	outputExecutorIDs map[string]struct{}
}

func (w *WorkflowInfo) Match(wf *workflow.Workflow) bool {
	if wf == nil {
		return false
	}
	if w.startExecutorID != wf.StartExecutorID {
		return false
	}
	if len(wf.ExecutorBindings) != len(w.executors) {
		return false
	}
	// Validate the executors
	for _, eb := range w.executors {
		binding, ok := wf.ExecutorBindings[eb.ExecutorID]
		if !ok || !eb.matchBinding(binding) {
			return false
		}
	}
	// Validate the edges
	if len(wf.Edges) != len(w.edges) {
		return false
	}
	for sourceID, edges := range w.edges {
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

	// Validate the input ports
	if len(wf.Ports) != len(w.requestPorts) {
		return false
	}
	for port := range w.requestPorts {
		other, ok := wf.Ports[port.ID]
		if !ok {
			return false
		}
		if !port.RequestType.Match(other.Request) || !port.ResponseType.Match(other.Response) {
			return false
		}
	}

	// Validate the outputs
	if len(wf.OutputExecutors) != len(w.outputExecutorIDs) {
		return false
	}
	for outputID := range w.outputExecutorIDs {
		if _, ok := wf.OutputExecutors[outputID]; !ok {
			return false
		}
	}
	return true
}
