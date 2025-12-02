// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"fmt"
	"maps"
	"slices"
)

type Builder struct {
	startExecutorId string
	name            string
	description     string

	err error

	edgeCount                int
	executorsBindings        map[string]*ExecutorBinding
	edges                    map[string][]Edge
	unboundExecutors         map[string]struct{}
	conditionlessConnections []EdgeConnection
	inputPorts               map[string]RequestPort
	outputExecutors          map[string]struct{}
}

func NewBuilder(start *ExecutorBinding) *Builder {
	bld := &Builder{
		startExecutorId: start.ID,
		edges:           make(map[string][]Edge),
	}
	return bld
}

func (wb *Builder) WithName(name string) *Builder {
	if wb.err != nil {
		return wb
	}
	wb.name = name
	return wb
}

func (wb *Builder) WithDescription(description string) *Builder {
	if wb.err != nil {
		return wb
	}
	wb.description = description
	return wb
}

func (wb *Builder) WithOutputFrom(bindings ...*ExecutorBinding) *Builder {
	if wb.err != nil {
		return wb
	}
	for _, binding := range bindings {
		if !wb.track(binding) {
			return wb
		}
		if wb.outputExecutors == nil {
			wb.outputExecutors = make(map[string]struct{})
		}
		wb.outputExecutors[binding.ID] = struct{}{}
	}
	return wb
}

func (wb *Builder) BindExecutor(binding *ExecutorBinding) *Builder {
	if wb.err != nil {
		return wb
	}
	if !wb.checkBinding(binding) {
		return wb
	}
	if binding.isPlaceholder() {
		wb.err = fmt.Errorf("cannot bind executor with ID %q because it is a placeholder registration", binding.ID)
		return wb
	}
	wb.track(binding)
	return wb
}

func (wb *Builder) AddEdge(source *ExecutorBinding, target *ExecutorBinding) *Builder {
	return wb.AddDirectEdge(source, target, false, nil)
}

func (wb *Builder) AddDirectEdge(source *ExecutorBinding, target *ExecutorBinding, idempotent bool, condition func(any) bool) *Builder {
	if wb.err != nil {
		return wb
	}
	if !wb.checkBinding(source) || !wb.checkBinding(target) {
		return wb
	}
	conn := EdgeConnection{
		SourceIDs: []string{source.ID},
		SinkIDs:   []string{target.ID},
	}
	if condition == nil && slices.ContainsFunc(wb.conditionlessConnections, func(c EdgeConnection) bool {
		return conn.Equal(c)
	}) {
		if idempotent {
			return wb
		}
		wb.err = fmt.Errorf("an edge from '%s' to '%s' already exists without a condition", source.ID, target.ID)
		return wb
	}
	if !wb.track(source) || !wb.track(target) {
		return wb
	}
	wb.edges[source.ID] = append(wb.edges[source.ID], Edge{
		Connection: conn,
		Condition:  condition,
		Index:      wb.edgeIdx(),
	})
	wb.conditionlessConnections = append(wb.conditionlessConnections, conn)
	return wb
}

func (wb *Builder) AddFanOutEdge(source *ExecutorBinding, targets []*ExecutorBinding, partitioner func(any, int, []int) bool) *Builder {
	if wb.err != nil {
		return wb
	}
	if !wb.track(source) {
		return wb
	}
	sinkIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		if !wb.track(target) {
			return wb
		}
		sinkIDs = append(sinkIDs, target.ID)
	}
	conn := EdgeConnection{
		SourceIDs: []string{source.ID},
		SinkIDs:   sinkIDs,
	}
	wb.edges[source.ID] = append(wb.edges[source.ID], Edge{
		Connection: conn,
		Index:      wb.edgeIdx(),
	})
	return wb
}

func (wb *Builder) AddFanInEdge(target *ExecutorBinding, sources []*ExecutorBinding) *Builder {
	if wb.err != nil {
		return wb
	}
	if !wb.track(target) {
		return wb
	}
	sourceIDs := make([]string, 0, len(sources))
	for _, source := range sources {
		if !wb.track(source) {
			return wb
		}
		sourceIDs = append(sourceIDs, source.ID)
	}
	edge := Edge{
		Connection: EdgeConnection{
			SourceIDs: sourceIDs,
			SinkIDs:   []string{target.ID},
		},
		Index: wb.edgeIdx(),
	}
	for _, id := range sourceIDs {
		wb.edges[id] = append(wb.edges[id], edge)
	}
	return wb
}

func (wb *Builder) Build() (*Workflow, error) {
	return wb.build(true)
}

func (wb *Builder) build(validateOrphans bool) (*Workflow, error) {
	if wb.err != nil {
		return nil, wb.err
	}
	if !wb.validate(validateOrphans) {
		return nil, wb.err
	}
	return &Workflow{
		StartExecutorID:  wb.startExecutorId,
		Name:             wb.name,
		Description:      wb.description,
		Edges:            wb.edges,
		Ports:            wb.inputPorts,
		ExecutorBindings: wb.executorsBindings,
		OutputExecutors:  wb.outputExecutors,
	}, nil
}

func (wb *Builder) validate(validateOrphans bool) bool {
	if wb.err != nil {
		return false
	}
	// Check that there are no "unbound" (defined as placeholders that have not been replaced by real bindings)
	// executors.
	if len(wb.unboundExecutors) > 0 {
		keys := slices.Collect(maps.Keys(wb.unboundExecutors))
		slices.Sort(keys)
		wb.err = fmt.Errorf("workflow cannot be built because there are unbound executors: %v", keys)
		return false
	}
	if !validateOrphans || len(wb.executorsBindings) == 0 {
		return true
	}
	// Make sure that all nodes are connected to the start executor (transitively).
	remainingExecutors := make(map[string]struct{}, len(wb.executorsBindings))
	for id := range wb.executorsBindings {
		remainingExecutors[id] = struct{}{}
	}
	toVisit := []string{wb.startExecutorId}
	for len(toVisit) > 0 {
		var currentID string
		currentID, toVisit = toVisit[0], toVisit[1:]
		if _, unvisited := remainingExecutors[currentID]; !unvisited {
			continue
		}
		delete(remainingExecutors, currentID)
		if edges, ok := wb.edges[currentID]; ok {
			for _, edge := range edges {
				toVisit = append(toVisit, edge.Connection.SinkIDs...)
			}
		}
	}
	if len(remainingExecutors) > 0 {
		keys := slices.Collect(maps.Keys(wb.unboundExecutors))
		slices.Sort(keys)
		wb.err = fmt.Errorf("workflow cannot be built because there are orphaned executors: %v", keys)
		return false
	}
	return true
}

func (wb *Builder) edgeIdx() int {
	wb.edgeCount++
	return wb.edgeCount
}

func (wb *Builder) checkBinding(binding *ExecutorBinding) bool {
	if wb.err != nil {
		return false
	}
	if binding == nil {
		wb.err = fmt.Errorf("cannot track nil executor binding")
		return false
	}
	return true
}

func (wb *Builder) track(binding *ExecutorBinding) bool {
	if wb.err != nil {
		return false
	}
	if !wb.checkBinding(binding) {
		return false
	}
	existing, exists := wb.executorsBindings[binding.ID]
	// If the executor is unbound, create an entry for it, unless it already exists.
	// Otherwise, update the entry for it, and remove the unbound tag
	if binding.isPlaceholder() && !exists {
		// If this is an unbound executor, we need to track it separately
		if wb.unboundExecutors == nil {
			wb.unboundExecutors = make(map[string]struct{})
		}
		wb.unboundExecutors[binding.ID] = struct{}{}
	} else if !binding.isPlaceholder() {
		// If there is already a bound executor with this ID, we need to validate (to best efforts)
		// that the two are matching (at least based on type)
		if exists {
			if existing.ExecutorType != binding.ExecutorType {
				wb.err = fmt.Errorf(
					"cannot bind executor with ID %q because an executor with the same ID but a different type (%q vs %q) is already bound",
					binding.ID, existing.ExecutorType, binding.ExecutorType,
				)
				return false
			}
			if existing.Raw != nil && existing.Raw != binding.Raw {
				wb.err = fmt.Errorf(
					"cannot bind executor with ID %q because an executor with the same ID but a different instance is already bound",
					binding.ID,
				)
				return false
			}
		} else {
			if wb.executorsBindings == nil {
				wb.executorsBindings = make(map[string]*ExecutorBinding)
			}
			wb.executorsBindings[binding.ID] = binding
			delete(wb.unboundExecutors, binding.ID)
		}
	}
	if binding.Raw != nil {
		if port, ok := binding.Raw.(RequestPort); ok {
			if wb.inputPorts == nil {
				wb.inputPorts = make(map[string]RequestPort)
			}
			wb.inputPorts[port.ID] = port
		}
	}
	return true
}
