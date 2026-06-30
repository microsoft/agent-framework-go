// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

const sessionStateKey = "workflowprovider_state"

// providerServiceID marks sessions managed by this provider. Setting a
// non-empty ServiceID on the session opts out of the agent package's
// default history provider on subsequent calls. Explicitly configured
// history providers still run. The workflow itself owns conversational
// state across turns.
const providerServiceID = "workflowprovider"

type sessionCheckpointEntry struct {
	CheckpointInfo workflow.CheckpointInfo  `json:"checkpointInfo"`
	Data           json.RawMessage          `json:"data"`
	Parent         *workflow.CheckpointInfo `json:"parent,omitempty"`
}

type sessionCheckpointStore struct {
	Entries []sessionCheckpointEntry `json:"entries,omitempty"`
}

var _ checkpoint.Store[json.RawMessage] = (*sessionCheckpointStore)(nil)

func (s *sessionCheckpointStore) CreateCheckpoint(_ context.Context, sessionID string, data json.RawMessage, parent *workflow.CheckpointInfo) (workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return workflow.CheckpointInfo{}, fmt.Errorf("agentworkflow: sessionID cannot be empty")
	}
	if !json.Valid(data) {
		return workflow.CheckpointInfo{}, fmt.Errorf("agentworkflow: checkpoint data is not valid JSON")
	}
	if parent != nil && *parent != (workflow.CheckpointInfo{}) && parent.SessionID != sessionID {
		return workflow.CheckpointInfo{}, fmt.Errorf("agentworkflow: parent sessionID %q does not match sessionID %q", parent.SessionID, sessionID)
	}

	info := s.unusedCheckpointInfo(sessionID)
	var parentCopy *workflow.CheckpointInfo
	if parent != nil {
		p := *parent
		parentCopy = &p
	}
	s.Entries = append(s.Entries, sessionCheckpointEntry{
		CheckpointInfo: info,
		Data:           append(json.RawMessage(nil), data...),
		Parent:         parentCopy,
	})
	return info, nil
}

func (s *sessionCheckpointStore) RetrieveCheckpoint(_ context.Context, sessionID string, info workflow.CheckpointInfo) (json.RawMessage, error) {
	if err := validateCheckpointLookup(sessionID, info); err != nil {
		return nil, err
	}
	for _, entry := range s.Entries {
		if entry.CheckpointInfo == info {
			return append(json.RawMessage(nil), entry.Data...), nil
		}
	}
	return nil, fmt.Errorf("agentworkflow: checkpoint %s not found for session %s", info.CheckpointID, sessionID)
}

func (s *sessionCheckpointStore) RetrieveIndex(_ context.Context, sessionID string, withParent *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("agentworkflow: sessionID cannot be empty")
	}
	if withParent != nil && *withParent != (workflow.CheckpointInfo{}) && withParent.SessionID != sessionID {
		return nil, fmt.Errorf("agentworkflow: parent sessionID %q does not match sessionID %q", withParent.SessionID, sessionID)
	}
	var result []workflow.CheckpointInfo
	for _, entry := range s.Entries {
		if entry.CheckpointInfo.SessionID != sessionID {
			continue
		}
		if withParent != nil {
			if entry.Parent == nil || *entry.Parent != *withParent {
				continue
			}
		}
		result = append(result, entry.CheckpointInfo)
	}
	return result, nil
}

func (s *sessionCheckpointStore) unusedCheckpointInfo(sessionID string) workflow.CheckpointInfo {
	for {
		info := workflow.NewCheckpointInfo(sessionID)
		if !s.contains(info) {
			return info
		}
	}
}

func (s *sessionCheckpointStore) contains(info workflow.CheckpointInfo) bool {
	for _, entry := range s.Entries {
		if entry.CheckpointInfo == info {
			return true
		}
	}
	return false
}

func validateCheckpointLookup(sessionID string, info workflow.CheckpointInfo) error {
	if sessionID == "" {
		return fmt.Errorf("agentworkflow: sessionID cannot be empty")
	}
	if info.SessionID == "" {
		return fmt.Errorf("agentworkflow: checkpoint sessionID cannot be empty")
	}
	if info.CheckpointID == "" {
		return fmt.Errorf("agentworkflow: checkpointID cannot be empty")
	}
	if info.SessionID != sessionID {
		return fmt.Errorf("agentworkflow: checkpoint sessionID %q does not match sessionID %q", info.SessionID, sessionID)
	}
	return nil
}

// providerState holds the workflow run state that survives across multiple
// [agent.Agent.Run] invocations on the same session. It is stored on the
// [agent.Session] under [sessionStateKey].
//
// The streaming run is an in-memory object and is not portable across process
// boundaries. Session JSON retains the latest checkpoint, any implicit
// checkpoint data, and pending request-tracking metadata so a later process can
// resume the workflow.
type providerState struct {
	stream            *inproc.StreamingRun
	workflowSessionID string
	lastCheckpoint    *workflow.CheckpointInfo
	checkpointStore   *sessionCheckpointStore
	pending           map[string]*workflow.ExternalRequest // keyed by request content ID (e.g. CallID/RequestID)
}

type providerStateJSON struct {
	WorkflowSessionID string                               `json:"workflowSessionID,omitempty"`
	LastCheckpoint    *workflow.CheckpointInfo             `json:"lastCheckpoint,omitempty"`
	CheckpointStore   *sessionCheckpointStore              `json:"checkpointStore,omitempty"`
	Pending           map[string]*workflow.ExternalRequest `json:"pending,omitempty"`
}

func (s *providerState) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	return json.Marshal(providerStateJSON{
		WorkflowSessionID: s.workflowSessionID,
		LastCheckpoint:    s.lastCheckpoint,
		CheckpointStore:   s.checkpointStore,
		Pending:           s.pending,
	})
}

func (s *providerState) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = providerState{}
		return nil
	}
	var tmp providerStateJSON
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	s.stream = nil
	s.workflowSessionID = tmp.WorkflowSessionID
	s.lastCheckpoint = tmp.LastCheckpoint
	s.checkpointStore = tmp.CheckpointStore
	s.pending = tmp.Pending
	s.ensureWorkflowSessionID()
	if s.pending == nil {
		s.pending = make(map[string]*workflow.ExternalRequest)
	}
	return nil
}

func newProviderState() *providerState {
	state := &providerState{pending: make(map[string]*workflow.ExternalRequest)}
	state.ensureWorkflowSessionID()
	return state
}

// loadOrInitState fetches the per-session [providerState], creating a fresh
// streaming workflow run on first use.
func loadOrInitState(
	ctx context.Context,
	sess *agent.Session,
	env *inproc.ExecutionEnvironment,
	wf *workflow.Workflow,
) (*providerState, error) {
	var state *providerState
	if sess != nil {
		if ok, _ := sess.Get(sessionStateKey, &state); ok && state != nil {
			state.ensurePending()
			state.ensureWorkflowSessionID()
			if state.stream != nil {
				return state, nil
			}
		} else {
			state = nil
		}
	}
	if state == nil {
		state = newProviderState()
	}
	state.ensureWorkflowSessionID()

	effectiveEnv, err := state.executionEnvironment(env)
	if err != nil {
		return nil, err
	}

	if state.lastCheckpoint != nil {
		stream, err := effectiveEnv.ResumeStreaming(ctx, wf, *state.lastCheckpoint, inproc.WithPendingRequestRepublish(false))
		if err != nil {
			return nil, err
		}
		state.stream = stream
		return state, nil
	}

	stream, err := effectiveEnv.RunStreaming(ctx, wf, nil, inproc.WithSessionID(state.workflowSessionID))
	if err != nil {
		return nil, err
	}
	state.stream = stream
	state.ensurePending()
	return state, nil
}

func (s *providerState) ensurePending() {
	if s.pending == nil {
		s.pending = make(map[string]*workflow.ExternalRequest)
	}
}

func (s *providerState) ensureWorkflowSessionID() {
	if s.workflowSessionID != "" {
		return
	}
	if s.lastCheckpoint != nil && s.lastCheckpoint.SessionID != "" {
		s.workflowSessionID = s.lastCheckpoint.SessionID
		return
	}
	s.workflowSessionID = uuid.NewString()
}

func (s *providerState) closeStream(ctx context.Context) error {
	if s == nil || s.stream == nil {
		return nil
	}
	stream := s.stream
	s.stream = nil
	return stream.Close(ctx)
}

func (s *providerState) executionEnvironment(env *inproc.ExecutionEnvironment) (*inproc.ExecutionEnvironment, error) {
	if env.IsCheckpointingEnabled() {
		if s.checkpointStore != nil {
			return nil, errors.New("agentworkflow: session was saved with an externalized checkpoint store, but the configured execution environment already has checkpointing enabled")
		}
		return env, nil
	}

	if s.checkpointStore == nil {
		if s.lastCheckpoint != nil {
			return nil, errors.New("agentworkflow: session has a saved checkpoint but no checkpoint store, and the configured execution environment has no checkpoint manager")
		}
		s.checkpointStore = &sessionCheckpointStore{}
	}
	return env.WithCheckpointing(checkpoint.NewJSONManager(s.checkpointStore)), nil
}

func saveState(sess *agent.Session, state *providerState) {
	if sess == nil || state == nil {
		return
	}
	sess.Set(sessionStateKey, state)
}
