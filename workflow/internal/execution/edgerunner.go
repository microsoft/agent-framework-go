// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"sync"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
	"golang.org/x/sync/errgroup"
)

type statefulEdgeStateJSON struct {
	PendingMessages []*checkpoint.PortableMessageEnvelope
	SourceIDs       []string
	Unseen          []string
}

type statefulEdgeState struct {
	// mu serializes concurrent calls to processMessage from parallel executor
	// tasks during superstep execution.
	mu              sync.Mutex
	pendingMessages []*checkpoint.PortableMessageEnvelope
	sourceIDs       []string
	unseen          map[string]struct{}
}

func (s *statefulEdgeState) MarshalJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unseen := slices.Collect(maps.Keys(s.unseen))
	slices.Sort(unseen)
	tmp := statefulEdgeStateJSON{
		PendingMessages: s.pendingMessages,
		SourceIDs:       s.sourceIDs,
		Unseen:          unseen,
	}
	return json.Marshal(tmp)
}

func (s *statefulEdgeState) UnmarshalJSON(data []byte) error {
	var tmp statefulEdgeStateJSON
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingMessages = tmp.PendingMessages
	s.sourceIDs = tmp.SourceIDs
	s.unseen = make(map[string]struct{}, len(tmp.Unseen))
	for _, id := range tmp.Unseen {
		s.unseen[id] = struct{}{}
	}
	return nil
}

func (s *statefulEdgeState) processMessage(sourceID string, envelope *MessageEnvelope) []*MessageEnvelope {
	// Serialize concurrent calls from parallel executor tasks during superstep execution.
	s.mu.Lock()
	s.pendingMessages = append(s.pendingMessages, envelope.Portable())
	delete(s.unseen, sourceID)
	if len(s.unseen) != 0 {
		s.mu.Unlock()
		return nil
	}
	taken := s.pendingMessages
	s.pendingMessages = nil
	if s.unseen == nil {
		s.unseen = make(map[string]struct{}, len(s.sourceIDs))
	}
	for _, id := range s.sourceIDs {
		s.unseen[id] = struct{}{}
	}
	s.mu.Unlock()

	if len(taken) == 0 {
		return nil
	}

	envelopes := make([]*MessageEnvelope, 0, len(taken))
	for _, env := range taken {
		envelopes = append(envelopes, NewMessageEnvelopeFromPortable(env))
	}
	return envelopes
}

func (s *statefulEdgeState) clone() *statefulEdgeState {
	s.mu.Lock()
	defer s.mu.Unlock()

	unseen := make(map[string]struct{}, len(s.unseen))
	for id := range s.unseen {
		unseen[id] = struct{}{}
	}
	return &statefulEdgeState{
		pendingMessages: slices.Clone(s.pendingMessages),
		sourceIDs:       slices.Clone(s.sourceIDs),
		unseen:          unseen,
	}
}

// EdgeRunner manages routing of messages through workflow edges.
type EdgeRunner struct {
	ensureExecutor  func(context.Context, string, StepTracer) (*workflow.Executor, error)
	tracer          StepTracer
	startExecutorID string
	statefulEdges   map[int]*statefulEdgeState
}

// NewEdgeRunner creates a new [EdgeRunner] for the given workflow.
func NewEdgeRunner(wf *workflow.Workflow, tracer StepTracer, ensureExecutor func(context.Context, string, StepTracer) (*workflow.Executor, error)) *EdgeRunner {
	var statefulEdges map[int]*statefulEdgeState
	for _, edges := range wf.Edges() {
		for _, edge := range edges {
			if len(edge.Connection.SourceIDs) <= 1 {
				continue
			}
			if statefulEdges == nil {
				statefulEdges = make(map[int]*statefulEdgeState)
			}
			unseen := make(map[string]struct{}, len(edge.Connection.SourceIDs))
			for _, id := range edge.Connection.SourceIDs {
				unseen[id] = struct{}{}
			}
			statefulEdges[edge.Index] = &statefulEdgeState{
				sourceIDs: edge.Connection.SourceIDs,
				unseen:    unseen,
			}
		}
	}

	return &EdgeRunner{
		ensureExecutor:  ensureExecutor,
		startExecutorID: wf.StartExecutorID(),
		statefulEdges:   statefulEdges,
		tracer:          tracer,
	}
}

func (em *EdgeRunner) ExportState() (map[string]workflow.PortableValue, error) {
	state := make(map[string]workflow.PortableValue, len(em.statefulEdges))
	for index, edgeState := range em.statefulEdges {
		state[strconv.Itoa(index)] = workflow.AnyPortableValue(edgeState.clone())
	}
	return state, nil
}

func (em *EdgeRunner) ImportState(cp *checkpoint.Checkpoint) error {
	if cp == nil {
		return fmt.Errorf("checkpoint cannot be nil")
	}
	for indexString, value := range cp.EdgeStateData {
		index, err := strconv.Atoi(indexString)
		if err != nil {
			return fmt.Errorf("invalid edge state key %q: %w", indexString, err)
		}
		state, ok := em.statefulEdges[index]
		if !ok {
			return fmt.Errorf("edge state for %d not found", index)
		}
		if imported, ok := value.Any().(*statefulEdgeState); ok {
			em.statefulEdges[index] = imported.clone()
			continue
		}
		if imported, ok := value.As(reflect.TypeFor[*statefulEdgeState]()); ok {
			em.statefulEdges[index] = imported.(*statefulEdgeState).clone()
			continue
		}
		return fmt.Errorf("unsupported exported state type for edge %d: %T", index, state)
	}
	return nil
}

// PrepareDeliveryForEdge prepares message delivery through an edge.
// Returns nil if the message cannot be routed through this edge.
func (em *EdgeRunner) PrepareDeliveryForEdge(ctx context.Context, edge workflow.Edge, envelope *MessageEnvelope) (mapping *DeliveryMapping, err error) {
	ctx, span := observability.FromContext(ctx).StartEdgeGroupProcess(ctx, edgeGroupMetadata(edge))
	defer func() {
		if err != nil {
			span.CaptureError(err)
			span.SetDeliveryStatus(observability.DeliveryStatusException)
		}
		span.End()
	}()

	if edge.Condition != nil && !edge.Condition(envelope.Message) {
		// Condition not met; do not route message.
		span.SetDeliveryStatus(observability.DeliveryStatusDroppedConditionFalse)
		return nil, nil
	}
	targetIDs := selectedTargetIDs(edge, envelope)
	if len(targetIDs) == 0 {
		span.SetDeliveryStatus(observability.DeliveryStatusDroppedTargetMismatch)
		return nil, nil
	}

	var envelopes []*MessageEnvelope
	if len(edge.Connection.SourceIDs) == 1 {
		envelopes = []*MessageEnvelope{envelope}
	} else {
		// Stateful edge - track source messages.
		state, ok := em.statefulEdges[edge.Index]
		if !ok {
			return nil, fmt.Errorf("edge state for %q not found", edge.Index)
		}
		envelopes = state.processMessage(envelope.SourceID, envelope)
		if len(envelopes) == 0 {
			// buffered message; waiting for more.
			span.SetDeliveryStatus(observability.DeliveryStatusBuffered)
			return nil, nil
		}
	}

	targets, err := em.resolveTargets(ctx, targetIDs)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		// Target mismatch.
		span.SetDeliveryStatus(observability.DeliveryStatusDroppedTargetMismatch)
		return nil, nil
	}
	if len(edge.Connection.SourceIDs) == 1 {
		// Filter targets that can handle the message type.
		runtimeType, err := em.messageRuntimeType(ctx, envelope)
		if err != nil {
			return nil, err
		}
		targets = slices.DeleteFunc(targets, func(target *workflow.Executor) bool {
			return !canHandleRuntimeType(target, runtimeType)
		})
	} else {
		if len(targets) != 1 {
			panic("stateful edges only support single target executor")
		}
		envelopes, err = em.filterEnvelopesForTarget(ctx, envelopes, targets[0])
		if err != nil {
			return nil, err
		}
	}
	if len(targets) == 0 || len(envelopes) == 0 {
		// Type mismatch.
		span.SetDeliveryStatus(observability.DeliveryStatusDroppedTypeMismatch)
		return nil, nil
	}
	span.SetDeliveryStatus(observability.DeliveryStatusDelivered)
	return &DeliveryMapping{
		Targets:   targets,
		Envelopes: envelopes,
	}, nil
}

func (em *EdgeRunner) filterEnvelopesForTarget(ctx context.Context, envelopes []*MessageEnvelope, target *workflow.Executor) ([]*MessageEnvelope, error) {
	filtered := envelopes[:0]
	for _, env := range envelopes {
		runtimeType, err := em.messageRuntimeType(ctx, env)
		if err != nil {
			return nil, err
		}
		if canHandleRuntimeType(target, runtimeType) {
			filtered = append(filtered, env)
		}
	}
	return filtered, nil
}

func selectedTargetIDs(edge workflow.Edge, envelope *MessageEnvelope) []string {
	targetIDs := edge.Connection.SinkIDs
	if edge.Assigner != nil {
		targetIDs = make([]string, 0, len(edge.Connection.SinkIDs))
		for id := range edge.Assigner(len(edge.Connection.SinkIDs), envelope.Message) {
			targetIDs = append(targetIDs, edge.Connection.SinkIDs[id])
		}
	}
	if envelope.TargetID == "" {
		return targetIDs
	}
	if edge.Assigner == nil {
		targetIDs = slices.Clone(targetIDs)
	}
	return slices.DeleteFunc(targetIDs, func(id string) bool {
		return id != envelope.TargetID
	})
}

func (em *EdgeRunner) resolveTargets(ctx context.Context, targetIDs []string) ([]*workflow.Executor, error) {
	targets := make([]*workflow.Executor, 0, 1)
	if len(targetIDs) == 1 {
		target, err := em.ensureExecutor(ctx, targetIDs[0], em.tracer)
		if err != nil {
			return nil, err
		}
		return append(targets, target), nil
	}
	if len(targetIDs) <= 1 {
		return targets, nil
	}

	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)
	for _, targetID := range targetIDs {
		g.Go(func() error {
			target, err := em.ensureExecutor(ctx, targetID, em.tracer)
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				mu.Lock()
				targets = append(targets, target)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (em *EdgeRunner) messageRuntimeType(ctx context.Context, envelope *MessageEnvelope) (reflect.Type, error) {
	if portable, ok := envelope.Message.(workflow.PortableValue); ok {
		if envelope.SourceID == "" {
			return nil, nil
		}
		source, err := em.ensureExecutor(ctx, envelope.SourceID, em.tracer)
		if err != nil {
			return nil, err
		}
		if typ, ok := SentRuntimeType(source, portable.TypeID); ok {
			return typ, nil
		}
		return nil, nil
	}
	return reflect.TypeOf(envelope.Message), nil
}

func canHandleRuntimeType(target *workflow.Executor, runtimeType reflect.Type) bool {
	if runtimeType != nil {
		return CanHandleType(target, runtimeType)
	}
	return target.DescribeProtocol().AcceptsAll
}

// PrepareDeliveryForInput prepares delivery of an external input message.
func (em *EdgeRunner) PrepareDeliveryForInput(ctx context.Context, envelope *MessageEnvelope) (mapping *DeliveryMapping, err error) {
	ctx, span := observability.FromContext(ctx).StartEdgeGroupProcess(ctx, observability.EdgeGroupMetadata{
		Type:     "ResponseEdgeRunner",
		SourceID: envelope.SourceID,
		TargetID: em.startExecutorID + "[]",
	})
	defer func() {
		if err != nil {
			span.CaptureError(err)
			span.SetDeliveryStatus(observability.DeliveryStatusException)
		}
		span.End()
	}()

	target, err := em.ensureExecutor(ctx, em.startExecutorID, em.tracer)
	if err != nil {
		return nil, err
	}
	if !CanHandleTypeID(target, envelope.MessageType()) {
		// Type mismatch.
		span.SetDeliveryStatus(observability.DeliveryStatusDroppedTypeMismatch)
		return nil, nil
	}
	span.SetDeliveryStatus(observability.DeliveryStatusDelivered)
	return &DeliveryMapping{
		Targets:   []*workflow.Executor{target},
		Envelopes: []*MessageEnvelope{envelope},
	}, nil
}

// PrepareDeliveryForResponse prepares delivery of an external response to
// the executor that posted the matching request.
func (em *EdgeRunner) PrepareDeliveryForResponse(ctx context.Context, response *workflow.ExternalResponse, ownerID string) (mapping *DeliveryMapping, err error) {
	ctx, span := observability.FromContext(ctx).StartEdgeGroupProcess(ctx, observability.EdgeGroupMetadata{
		Type:     "ResponseEdgeRunner",
		TargetID: ownerID + "[" + response.PortInfo.PortID + "]",
	})
	defer func() {
		if err != nil {
			span.CaptureError(err)
			span.SetDeliveryStatus(observability.DeliveryStatusException)
		}
		span.End()
	}()

	if ownerID == "" {
		return nil, fmt.Errorf("response %q has no owning executor", response.RequestID)
	}
	envelope := &MessageEnvelope{Message: response}
	target, err := em.ensureExecutor(ctx, ownerID, em.tracer)
	if err != nil {
		return nil, err
	}
	if !CanHandleTypeID(target, envelope.MessageType()) {
		// Type mismatch.
		span.SetDeliveryStatus(observability.DeliveryStatusDroppedTypeMismatch)
		return nil, nil
	}
	span.SetDeliveryStatus(observability.DeliveryStatusDelivered)
	return &DeliveryMapping{
		Targets:   []*workflow.Executor{target},
		Envelopes: []*MessageEnvelope{envelope},
	}, nil
}

func edgeGroupMetadata(edge workflow.Edge) observability.EdgeGroupMetadata {
	metadata := observability.EdgeGroupMetadata{Type: edgeGroupType(edge)}
	switch metadata.Type {
	case "FanInEdgeRunner":
		if len(edge.Connection.SinkIDs) == 1 {
			metadata.TargetID = edge.Connection.SinkIDs[0]
		}
	case "FanOutEdgeRunner":
		if len(edge.Connection.SourceIDs) == 1 {
			metadata.SourceID = edge.Connection.SourceIDs[0]
		}
	default:
		if len(edge.Connection.SourceIDs) == 1 {
			metadata.SourceID = edge.Connection.SourceIDs[0]
		}
		if len(edge.Connection.SinkIDs) == 1 {
			metadata.TargetID = edge.Connection.SinkIDs[0]
		}
	}
	return metadata
}

func edgeGroupType(edge workflow.Edge) string {
	if len(edge.Connection.SourceIDs) > 1 {
		return "FanInEdgeRunner"
	}
	if len(edge.Connection.SinkIDs) > 1 || edge.Assigner != nil {
		return "FanOutEdgeRunner"
	}
	return "DirectEdgeRunner"
}
