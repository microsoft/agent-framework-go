// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/microsoft/agent-framework/go/internal/errgroup"
	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/internal/checkpoint"
)

type statefulEdgeStateJSON struct {
	PendingMessages []*checkpoint.PortableMessageEnvelope
	SourceIDs       []string
	Unseen          []string
}

type statefulEdgeState struct {
	pendingMessages []*checkpoint.PortableMessageEnvelope
	sourceIDs       []string
	unseen          map[string]struct{}
}

func (s *statefulEdgeState) MarshalJSON() ([]byte, error) {
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
	s.pendingMessages = tmp.PendingMessages
	s.sourceIDs = tmp.SourceIDs
	s.unseen = make(map[string]struct{}, len(tmp.Unseen))
	for _, id := range tmp.Unseen {
		s.unseen[id] = struct{}{}
	}
	return nil
}

func (s *statefulEdgeState) processMessage(sourceID string, envelope *MessageEnvelope) []*MessageEnvelope {
	s.pendingMessages = append(s.pendingMessages, envelope.Portable())
	delete(s.unseen, sourceID)
	if len(s.unseen) != 0 {
		return nil
	}
	if s.unseen == nil {
		s.unseen = make(map[string]struct{})
	} else {
		clear(s.unseen)
	}
	for _, id := range s.sourceIDs {
		s.unseen[id] = struct{}{}
	}
	taken := make([]*MessageEnvelope, 0, len(s.pendingMessages))
	for _, env := range s.pendingMessages {
		taken = append(taken, NewMessageEnvelopeFromPortable(env))
	}
	clear(s.pendingMessages)
	return taken
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
	for _, edges := range wf.Edges {
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
		startExecutorID: wf.StartExecutorID,
		statefulEdges:   statefulEdges,
		tracer:          tracer,
	}
}

// PrepareDeliveryForEdge prepares message delivery through an edge.
// Returns nil if the message cannot be routed through this edge.
func (em *EdgeRunner) PrepareDeliveryForEdge(ctx context.Context, edge workflow.Edge, envelope *MessageEnvelope) (*DeliveryMapping, error) {
	if edge.Condition != nil && !edge.Condition(envelope.Message) {
		// Condition not met; do not route message.
		return nil, nil
	}
	// Determine target executors based on edge configuration.
	targetIDs := edge.Connection.SinkIDs
	if edge.Assigner != nil {
		targetIDs = make([]string, 0, len(edge.Connection.SinkIDs))
		for id := range edge.Assigner(len(edge.Connection.SinkIDs), envelope.Message) {
			targetIDs = append(targetIDs, edge.Connection.SinkIDs[id])
		}
	}
	// If the envelope specifies a target, filter to that target only.
	if envelope.TargetID != "" {
		if edge.Assigner == nil {
			// Clone target IDs to avoid modifying original slice.
			targetIDs = slices.Clone(targetIDs)
		}
		targetIDs = slices.DeleteFunc(targetIDs, func(id string) bool {
			return id != envelope.TargetID
		})
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
			return nil, nil
		}
	}

	// Resolve target executors.
	targets := make([]*workflow.Executor, 0, 1)
	if len(targetIDs) == 1 {
		// Optimize for single target case.
		target, err := em.ensureExecutor(ctx, targetIDs[0], em.tracer)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	} else if len(targetIDs) > 1 {
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
	}
	if len(targets) == 0 {
		// Target mismatch.
		return nil, nil
	}
	if len(edge.Connection.SourceIDs) == 1 {
		// Filter targets that can handle the message type.
		targets = slices.DeleteFunc(targets, func(target *workflow.Executor) bool {
			return !target.CanHandleTypeID(envelope.MessageType())
		})
	} else {
		if len(targets) != 1 {
			panic("stateful edges only support single target executor")
		}
		// Filter envelopes whose message type is not handled by any target.
		envelopes = slices.DeleteFunc(envelopes, func(env *MessageEnvelope) bool {
			return !targets[0].CanHandleTypeID(env.MessageType())
		})
	}
	if len(targets) == 0 || len(envelopes) == 0 {
		// Type mismatch.
		return nil, nil
	}
	return &DeliveryMapping{
		Targets:   targets,
		Envelopes: envelopes,
	}, nil
}

// PrepareDeliveryForInput prepares delivery of an external input message.
func (em *EdgeRunner) PrepareDeliveryForInput(ctx context.Context, envelope *MessageEnvelope) (*DeliveryMapping, error) {
	target, err := em.ensureExecutor(ctx, em.startExecutorID, em.tracer)
	if err != nil {
		return nil, err
	}
	if !target.CanHandleTypeID(envelope.MessageType()) {
		// Type mismatch.
		return nil, nil
	}
	return &DeliveryMapping{
		Targets:   []*workflow.Executor{target},
		Envelopes: []*MessageEnvelope{envelope},
	}, nil
}

// PrepareDeliveryForResponse prepares delivery of an external response.
func (em *EdgeRunner) PrepareDeliveryForResponse(ctx context.Context, response *workflow.ExternalResponse) (*DeliveryMapping, error) {
	return em.PrepareDeliveryForInput(ctx, &MessageEnvelope{Message: response})
}
