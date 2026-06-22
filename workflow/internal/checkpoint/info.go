// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import "github.com/microsoft/agent-framework-go/workflow"

type executorInfo struct {
	ImplementationID string
	ExecutorID       string
}

func (e *executorInfo) matchBinding(executor workflow.ExecutorBinding) bool {
	return e.ExecutorID == executor.ID && e.ImplementationID != "" && e.ImplementationID == executor.ImplementationID
}

type WorkflowInfo struct {
	Executors       map[string]executorInfo
	Edges           map[string][]workflow.EdgeInfo
	RequestPorts    map[string]workflow.RequestPortInfo
	StartExecutorID string
	OutputExecutors map[string][]workflow.OutputTag
}

func NewWorkflowInfo(wf *workflow.Workflow) WorkflowInfo {
	bindings := wf.ReflectExecutors()
	executors := make(map[string]executorInfo, len(bindings))
	for id, binding := range bindings {
		executors[id] = executorInfo{
			ImplementationID: binding.ImplementationID,
			ExecutorID:       binding.ID,
		}
	}

	edges := wf.ReflectEdges()

	ports := wf.RequestPorts()
	requestPorts := make(map[string]workflow.RequestPortInfo, len(ports))
	for _, port := range ports {
		info := workflow.NewRequestPortInfo(port)
		requestPorts[info.PortID] = info
	}

	return WorkflowInfo{
		Executors:       executors,
		Edges:           edges,
		RequestPorts:    requestPorts,
		StartExecutorID: wf.StartExecutorID(),
		OutputExecutors: wf.OutputExecutors(),
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

	otherOutputs := wf.OutputExecutors()
	if len(otherOutputs) != len(w.OutputExecutors) {
		return false
	}
	for id, tags := range w.OutputExecutors {
		otherTags, ok := otherOutputs[id]
		if !ok || !outputTagsEqual(tags, otherTags) {
			return false
		}
	}
	return true
}

func outputTagsEqual(left, right []workflow.OutputTag) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[workflow.OutputTag]int, len(left))
	for _, tag := range left {
		counts[tag]++
	}
	for _, tag := range right {
		counts[tag]--
		if counts[tag] < 0 {
			return false
		}
	}
	return true
}
