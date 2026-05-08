// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"reflect"
	"slices"

	internalobservability "github.com/microsoft/agent-framework-go/workflow/internal/observability"
	workflowobservability "github.com/microsoft/agent-framework-go/workflow/observability"
)

type Builder struct {
	startExecutorId string
	name            string
	description     string

	err error

	edgeCount                int
	executorsBindings        map[string]*ExecutorBinding
	unboundExecutors         map[string]struct{}
	edges                    map[string][]Edge
	conditionlessConnections []EdgeConnection
	inputPorts               map[string]RequestPort
	outputExecutors          map[string]struct{}
	telemetry                *internalobservability.Context
}

func NewBuilder(start *ExecutorBinding) *Builder {
	bld := &Builder{
		startExecutorId: start.ID,
		edges:           make(map[string][]Edge),
	}
	// Always track the start binding so even single-node workflows have a
	// proper ExecutorBindings entry. Without this, Workflow.DescribeProtocol
	// and other lookups by StartExecutorID dereference a nil binding.
	bld.track(start)
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

// WithTelemetry enables telemetry instrumentation for workflows built by this builder.
func (wb *Builder) WithTelemetry(tracer workflowobservability.Tracer, options TelemetryOptions) *Builder {
	if wb.err != nil {
		return wb
	}
	wb.telemetry = internalobservability.New(options.observabilityOptions(tracer))
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

func (wb *Builder) AddEdge(source *ExecutorBinding, target *ExecutorBinding, opts ...EdgeOption) *Builder {
	return wb.AddDirectEdge(source, target, false, nil, opts...)
}

func (wb *Builder) AddDirectEdge(source *ExecutorBinding, target *ExecutorBinding, idempotent bool, condition func(any) bool, opts ...EdgeOption) *Builder {
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
	edge := Edge{
		Connection: conn,
		Condition:  condition,
		Index:      wb.edgeIdx(),
	}
	applyEdgeOptions(&edge, opts)
	wb.edges[source.ID] = append(wb.edges[source.ID], edge)
	wb.conditionlessConnections = append(wb.conditionlessConnections, conn)
	return wb
}

// AddFanOutEdge adds a fan-out edge from source to one or more targets.
//
// By default the message is delivered to every target. Pass [WithAssigner]
// to choose a subset of targets per message. See [Edge.Assigner].
func (wb *Builder) AddFanOutEdge(source *ExecutorBinding, targets []*ExecutorBinding, opts ...EdgeOption) *Builder {
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
	edge := Edge{
		Connection: conn,
		Index:      wb.edgeIdx(),
	}
	applyEdgeOptions(&edge, opts)
	wb.edges[source.ID] = append(wb.edges[source.ID], edge)
	return wb
}

// AddFanInBarrierEdge adds a fan-in edge from sources to target, waiting for
// all sources before dispatching to the target.
func (wb *Builder) AddFanInBarrierEdge(sources []*ExecutorBinding, target *ExecutorBinding, opts ...EdgeOption) *Builder {
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
	applyEdgeOptions(&edge, opts)
	for _, id := range sourceIDs {
		wb.edges[id] = append(wb.edges[id], edge)
	}
	return wb
}

func (wb *Builder) Build() (*Workflow, error) {
	return wb.build(true)
}

func (wb *Builder) build(validateOrphans bool) (*Workflow, error) {
	telemetry := wb.telemetry
	if telemetry == nil {
		telemetry = internalobservability.Disabled()
	}
	_, activity := telemetry.StartWorkflowBuild(context.Background())
	defer activity.End()
	activity.AddEvent(internalobservability.EventBuildStarted)

	if wb.err != nil {
		activity.AddEvent(internalobservability.EventBuildError, internalobservability.BuildErrorAttributes(wb.err)...)
		activity.CaptureError(wb.err)
		return nil, wb.err
	}
	if !wb.validate(validateOrphans) {
		activity.AddEvent(internalobservability.EventBuildError, internalobservability.BuildErrorAttributes(wb.err)...)
		activity.CaptureError(wb.err)
		return nil, wb.err
	}
	activity.AddEvent(internalobservability.EventBuildValidationCompleted)
	wf := &Workflow{
		StartExecutorID:  wb.startExecutorId,
		Name:             wb.name,
		Description:      wb.description,
		Edges:            wb.edges,
		Ports:            wb.inputPorts,
		ExecutorBindings: wb.executorsBindings,
		OutputExecutors:  wb.outputExecutors,
		telemetry:        telemetry,
	}
	internalobservability.SetBuildWorkflowAttributes(activity, observabilityMetadata(wf, ""), workflowTelemetryDefinitionFrom(wf))
	activity.AddEvent(internalobservability.EventBuildCompleted)
	return wf, nil
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
	// Validate that output executors (declared via WithOutputFrom) exist in the graph.
	for id := range wb.outputExecutors {
		if _, ok := wb.executorsBindings[id]; !ok {
			wb.err = fmt.Errorf("output executor %q is not present in the workflow graph", id)
			return false
		}
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
		keys := slices.Collect(maps.Keys(remainingExecutors))
		slices.Sort(keys)
		wb.err = fmt.Errorf("workflow cannot be built because there are orphaned executors: %v", keys)
		return false
	}
	// Warn about self-loops (executor connecting to itself), which may cause infinite
	// recursion if not gated by a condition.
	for sourceID, edges := range wb.edges {
		for _, edge := range edges {
			for _, sinkID := range edge.Connection.SinkIDs {
				if sourceID == sinkID {
					slog.Warn("self-loop detected: executor connects to itself; this may cause infinite recursion if not properly handled with conditions",
						"executor", sourceID)
				}
			}
		}
	}
	// Log dead-end executors (no outgoing edges). Executors declared as outputs
	// are expected final nodes; other dead ends are worth flagging for review.
	executorsWithOutgoing := make(map[string]struct{}, len(wb.edges))
	for sourceID := range wb.edges {
		executorsWithOutgoing[sourceID] = struct{}{}
	}
	var deadEnds []string
	for id := range wb.executorsBindings {
		if _, isOutput := wb.outputExecutors[id]; isOutput {
			continue
		}
		if _, hasOutgoing := executorsWithOutgoing[id]; !hasOutgoing {
			deadEnds = append(deadEnds, id)
		}
	}
	if len(deadEnds) > 0 {
		slices.Sort(deadEnds)
		slog.Info("dead-end executors detected (no outgoing edges); verify these are intended as final nodes",
			"executors", deadEnds)
	}
	// Validate type compatibility between connected executors. For each edge we
	// create temporary executor instances, inspect their routers, and verify that
	// the source's output types overlap with the target's input types. Executors
	// with a catch-all handler accept any type, so they are always compatible as
	// targets. If either side has no declared types we skip that edge (dynamic
	// typing).
	if err := wb.validateTypeCompatibility(); err != nil {
		wb.err = err
		return false
	}
	return true
}

func (wb *Builder) edgeIdx() int {
	wb.edgeCount++
	return wb.edgeCount
}

// validateTypeCompatibility checks that output types of source executors are
// compatible with input types of target executors for every edge. It creates
// temporary executor instances to inspect their routers. If an executor cannot
// be instantiated or has no type metadata (e.g. catch-all or no output type
// annotations), the edge is silently skipped.
func (wb *Builder) validateTypeCompatibility() error {
	type routerInfo struct {
		router *messageRouter
		ok     bool
	}
	cache := make(map[string]routerInfo, len(wb.executorsBindings))
	getRouter := func(id string) (*messageRouter, bool) {
		if ri, cached := cache[id]; cached {
			return ri.router, ri.ok
		}
		binding := wb.executorsBindings[id]
		if binding == nil || binding.isPlaceholder() {
			cache[id] = routerInfo{}
			return nil, false
		}
		ex, err := binding.CreateInstance("")
		if err != nil {
			cache[id] = routerInfo{}
			return nil, false
		}
		r, err := ex.router()
		if err != nil {
			cache[id] = routerInfo{}
			return nil, false
		}
		cache[id] = routerInfo{router: r, ok: true}
		return r, true
	}

	for sourceID, edges := range wb.edges {
		sourceRouter, sourceOK := getRouter(sourceID)
		if !sourceOK {
			continue
		}
		sourceOutputTypes := slices.Collect(sourceRouter.DefaultOutputTypes())
		if len(sourceOutputTypes) == 0 {
			// Source has no declared output types; skip validation.
			continue
		}
		for _, edge := range edges {
			for _, sinkID := range edge.Connection.SinkIDs {
				targetRouter, targetOK := getRouter(sinkID)
				if !targetOK {
					continue
				}
				// Targets with a catch-all accept anything.
				if targetRouter.HasCatchAll() {
					continue
				}
				targetInputTypes := targetRouter.IncomingTypes()
				if len(targetInputTypes) == 0 {
					continue
				}
				// Check that at least one source output type matches a target input type.
				compatible := false
				for _, outType := range sourceOutputTypes {
					for _, inType := range targetInputTypes {
						if outType == inType || outType.AssignableTo(inType) || (inType.Kind() == reflect.Interface && outType.Implements(inType)) {
							compatible = true
							break
						}
					}
					if compatible {
						break
					}
				}
				if !compatible {
					return fmt.Errorf(
						"type incompatibility between executors %q -> %q: source outputs %v but target accepts %v",
						sourceID, sinkID, sourceOutputTypes, targetInputTypes,
					)
				}
			}
		}
	}
	return nil
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
	for _, port := range binding.Ports {
		if wb.inputPorts == nil {
			wb.inputPorts = make(map[string]RequestPort)
		}
		wb.inputPorts[port.ID] = port
	}
	return true
}

// AddChain connects source to each binding in executors in order, producing a
// linear pipeline source → executors[0] → executors[1] → ...
//
// If allowRepetition is false, the same binding may not appear twice in the
// chain (including the source). Adding the same edge twice is idempotent.
func (wb *Builder) AddChain(source *ExecutorBinding, executors []*ExecutorBinding, allowRepetition bool) *Builder {
	if wb.err != nil {
		return wb
	}
	if !wb.checkBinding(source) {
		return wb
	}
	seen := map[string]struct{}{source.ID: {}}
	current := source
	for _, exec := range executors {
		if !wb.checkBinding(exec) {
			return wb
		}
		if !allowRepetition {
			if _, ok := seen[exec.ID]; ok {
				wb.err = fmt.Errorf("executor %q is already in the chain", exec.ID)
				return wb
			}
			seen[exec.ID] = struct{}{}
		}
		wb.AddDirectEdge(current, exec, true /*idempotent*/, nil)
		if wb.err != nil {
			return wb
		}
		current = exec
	}
	return wb
}

// SwitchBuilder constructs a case-based fan-out edge. Cases are evaluated in
// the order they were added; matching cases route the message to that case's
// targets. If no case matches, the default targets are used.
type SwitchBuilder struct {
	source *ExecutorBinding

	// targets aggregates every binding referenced by any case so we can build
	// a single fan-out edge with stable indexes.
	targets         []*ExecutorBinding
	targetIndexByID map[string]int

	cases []switchCase

	// defaultIndices are the target indexes to dispatch to when no case
	// predicate matches.
	defaultIndices []int
}

type switchCase struct {
	predicate func(any) bool
	indices   []int
}

// AddSwitch starts building a switch-style fan-out from source. Configure
// cases via [SwitchBuilder.AddCase] and an optional default via
// [SwitchBuilder.WithDefault], then commit by calling
// [SwitchBuilder.AddToBuilder].
func (wb *Builder) AddSwitch(source *ExecutorBinding) *SwitchBuilder {
	return &SwitchBuilder{
		source:          source,
		targetIndexByID: map[string]int{},
	}
}

// AddCase adds a case branch matching messages of type T satisfying the
// predicate. The matched message is routed to all bindings in targets.
func (s *SwitchBuilder) AddCase(predicate func(msg any) bool, targets ...*ExecutorBinding) *SwitchBuilder {
	indices := s.collectTargets(targets)
	s.cases = append(s.cases, switchCase{predicate: predicate, indices: indices})
	return s
}

// WithDefault sets the targets to dispatch to when no case matches.
func (s *SwitchBuilder) WithDefault(targets ...*ExecutorBinding) *SwitchBuilder {
	s.defaultIndices = s.collectTargets(targets)
	return s
}

func (s *SwitchBuilder) collectTargets(targets []*ExecutorBinding) []int {
	out := make([]int, 0, len(targets))
	for _, t := range targets {
		if t == nil {
			continue
		}
		idx, ok := s.targetIndexByID[t.ID]
		if !ok {
			idx = len(s.targets)
			s.targets = append(s.targets, t)
			s.targetIndexByID[t.ID] = idx
		}
		out = append(out, idx)
	}
	return out
}

// AddToBuilder commits the configured switch onto wb as a fan-out edge with
// an assigner that picks targets based on the registered cases.
func (s *SwitchBuilder) AddToBuilder(wb *Builder) *Builder {
	if wb.err != nil {
		return wb
	}
	if s.source == nil {
		wb.err = fmt.Errorf("switch source is required")
		return wb
	}
	if len(s.targets) == 0 {
		// Nothing to do; return without error to allow no-op switches.
		return wb
	}

	cases := slices.Clone(s.cases)
	defaultIndices := slices.Clone(s.defaultIndices)
	assigner := func(_ int, msg any) iter.Seq[int] {
		return func(yield func(int) bool) {
			for _, c := range cases {
				if c.predicate(msg) {
					for _, idx := range c.indices {
						if !yield(idx) {
							return
						}
					}
					return
				}
			}
			for _, idx := range defaultIndices {
				if !yield(idx) {
					return
				}
			}
		}
	}
	return wb.AddFanOutEdge(s.source, s.targets, WithAssigner(assigner))
}
