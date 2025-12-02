// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"fmt"
	"iter"
	"maps"
	"reflect"

	"github.com/microsoft/agent-framework/go/internal/concurrent"
	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/internal/checkpoint"
)

type StepTracer interface {
	TraceActivated(executorID string)
	TraceCheckpointCreated(workflow.CheckpointInfo)
	TraceInstantiated(executorID string)
	TraceStatePublished()
}

// MessageEnvelope wraps a message with routing and tracing information.
type MessageEnvelope struct {
	Message      any
	SourceID     string
	TargetID     string
	TraceContext map[string]string

	declaredType workflow.TypeID
}

func NewMessageEnvelope(message any, declaredType reflect.Type, sourceID, targetID string) (*MessageEnvelope, error) {
	if declaredType == nil {
		declaredType = reflect.TypeOf(message)
	}
	if !reflect.TypeOf(message).AssignableTo(declaredType) {
		return nil, fmt.Errorf("the declared type %q is not compatible with the message instance of type %q", declaredType, reflect.TypeOf(message))
	}
	return &MessageEnvelope{
		Message:      message,
		declaredType: workflow.NewTypeID(declaredType),
		SourceID:     sourceID,
		TargetID:     targetID,
	}, nil
}

func NewMessageEnvelopeFromPortable(envelope *checkpoint.PortableMessageEnvelope) *MessageEnvelope {
	return &MessageEnvelope{
		Message:      envelope.Message.Any(),
		declaredType: envelope.MessageType,
		SourceID:     envelope.SourceID,
		TargetID:     envelope.TargetID,
		TraceContext: nil,
	}
}

func (e *MessageEnvelope) MessageType() workflow.TypeID {
	if e.declaredType.IsZero() {
		return workflow.NewTypeID(reflect.TypeOf(e.Message))
	}
	return e.declaredType
}

func (e *MessageEnvelope) Portable() *checkpoint.PortableMessageEnvelope {
	return &checkpoint.PortableMessageEnvelope{
		MessageType: e.MessageType(),
		Message:     workflow.AnyPortableValue(e.Message),
		SourceID:    e.SourceID,
		TargetID:    e.TargetID,
	}
}

// IsExternal returns true if this message is from an external source.
func (e *MessageEnvelope) IsExternal() bool {
	return e.SourceID == ""
}

// StepContext manages the queued messages for a single workflow step.
// It provides thread-safe access to message queues for each executor.
type StepContext struct {
	queuedMessages concurrent.Map[string, *concurrent.Queue[*MessageEnvelope]]
}

// HasMessages returns true if there are any queued messages.
func (s *StepContext) HasMessages() bool {
	for _, value := range s.queuedMessages.All() {
		if !value.IsEmpty() {
			return true
		}
	}
	return false
}

func (s *StepContext) Keys() []string {
	var keys []string
	for key := range s.queuedMessages.All() {
		keys = append(keys, key)
	}
	return keys
}

// MessagesFor returns the messages queued for the given target executor.
// It initializes an empty slice if the target doesn't exist yet.
func (s *StepContext) MessagesFor(target string) *concurrent.Queue[*MessageEnvelope] {
	v, _ := s.queuedMessages.LoadOrStore(target, &concurrent.Queue[*MessageEnvelope]{})
	return v
}

// ExportMessages exports all queued messages for checkpointing.
func (s *StepContext) ExportMessages() map[string][]*checkpoint.PortableMessageEnvelope {
	result := make(map[string][]*checkpoint.PortableMessageEnvelope)
	for identity, envelopes := range s.queuedMessages.All() {
		exported := make([]*checkpoint.PortableMessageEnvelope, 0, envelopes.Len())
		for env := range envelopes.All() {
			exported = append(exported, env.Portable())
		}
		result[identity] = exported
	}
	return result
}

// ImportMessages imports queued messages from a checkpoint.
func (s *StepContext) ImportMessages(messages map[string][]*checkpoint.PortableMessageEnvelope) {
	for identity, envelopes := range messages {
		var imported concurrent.Queue[*MessageEnvelope]
		for _, env := range envelopes {
			imported.Enqueue(NewMessageEnvelopeFromPortable(env))
		}
		s.queuedMessages.Store(identity, &imported)
	}
}

// StateScope manages state for a single scope (executor + optional scope name).
type StateScope struct {
	scopeID   workflow.ScopeID
	stateData map[string]workflow.PortableValue
}

// NewStateScope creates a new StateScope.
func NewStateScope(scopeID workflow.ScopeID) *StateScope {
	return &StateScope{
		scopeID:   scopeID,
		stateData: make(map[string]workflow.PortableValue),
	}
}

// ScopeID returns the scope identifier.
func (s *StateScope) ScopeID() workflow.ScopeID {
	return s.scopeID
}

// ReadKeys returns all keys in this scope.
func (s *StateScope) ReadKeys() map[string]struct{} {
	keys := make(map[string]struct{}, len(s.stateData))
	for k := range s.stateData {
		keys[k] = struct{}{}
	}
	return keys
}

// ContainsKey returns true if the key exists in this scope.
func (s *StateScope) ContainsKey(key string) bool {
	_, ok := s.stateData[key]
	return ok
}

// ReadState reads the state value for the given key.
func (s *StateScope) ReadState(key string) (workflow.PortableValue, bool) {
	value, ok := s.stateData[key]
	return value, ok
}

// WriteState writes multiple state updates to this scope.
func (s *StateScope) WriteState(updates map[string][]StateUpdate) error {
	for key, updateList := range updates {
		if len(updateList) == 0 {
			continue
		}

		if len(updateList) > 1 {
			return fmt.Errorf("expected exactly one update for key '%s'", key)
		}

		update := updateList[0]
		if update.IsDelete {
			delete(s.stateData, key)
		} else {
			s.stateData[key] = workflow.AnyPortableValue(update.Value)
		}
	}

	return nil
}

// ExportStates exports all state values for checkpointing.
func (s *StateScope) ExportStates() iter.Seq2[string, workflow.PortableValue] {
	return maps.All(s.stateData)
}

// ImportState imports a single state value.
func (s *StateScope) ImportState(key string, state workflow.PortableValue) {
	s.stateData[key] = state
}

// StateUpdate represents a state update operation.
type StateUpdate struct {
	Key      string
	Value    any
	IsDelete bool
}

// UpdateStateUpdate creates an update operation.
func UpdateStateUpdate(key string, value any) StateUpdate {
	return StateUpdate{Key: key, Value: value, IsDelete: false}
}

// DeleteStateUpdate creates a delete operation.
func DeleteStateUpdate(key string) StateUpdate {
	return StateUpdate{Key: key, IsDelete: true}
}

// UpdateKey identifies a state update by scope and key.
type UpdateKey struct {
	ScopeID workflow.ScopeID
	Key     string
}

// IsMatchingScope returns true if this update key matches the given scope.
func (u UpdateKey) IsMatchingScope(scopeID workflow.ScopeID, strict bool) bool {
	if strict {
		return u.ScopeID == scopeID
	}
	// For non-strict, match executor ID only
	return u.ScopeID.ExecutorID == scopeID.ExecutorID
}

// StateManager manages state for all executors in a workflow run.
// A zero value is valid is ready to use.
type StateManager struct {
	scopes        map[workflow.ScopeID]*StateScope
	queuedUpdates map[UpdateKey]StateUpdate
}

// getOrCreateScope gets or creates a state scope.
func (sm *StateManager) getOrCreateScope(scopeID workflow.ScopeID) *StateScope {
	if scope, ok := sm.scopes[scopeID]; ok {
		return scope
	}

	if sm.scopes == nil {
		sm.scopes = make(map[workflow.ScopeID]*StateScope)
	}
	scope := NewStateScope(scopeID)
	sm.scopes[scopeID] = scope
	return scope
}

// getUpdatesForScopeStrict returns all queued updates for a specific scope.
func (sm *StateManager) getUpdatesForScopeStrict(scopeID workflow.ScopeID) []UpdateKey {
	var keys []UpdateKey
	for key := range sm.queuedUpdates {
		if key.IsMatchingScope(scopeID, true) {
			keys = append(keys, key)
		}
	}
	return keys
}

// ClearState clears all state in the given scope.
func (sm *StateManager) ClearState(executorID string, scopeName string) error {
	scopeID := workflow.ScopeID{ExecutorID: executorID, Name: scopeName}
	return sm.ClearStateByID(scopeID)
}

// ClearStateByID clears all state for the given scope ID.
func (sm *StateManager) ClearStateByID(scopeID workflow.ScopeID) error {
	scope, exists := sm.scopes[scopeID]
	if !exists {
		return nil
	}

	keysToDelete := scope.ReadKeys()

	// Mark existing updates as deletes
	for _, updateKey := range sm.getUpdatesForScopeStrict(scopeID) {
		update := sm.queuedUpdates[updateKey]
		if !update.IsDelete {
			if sm.queuedUpdates == nil {
				sm.queuedUpdates = make(map[UpdateKey]StateUpdate)
			}
			sm.queuedUpdates[updateKey] = DeleteStateUpdate(update.Key)
		}
		delete(keysToDelete, update.Key)
	}

	// Queue deletes for remaining keys
	for key := range keysToDelete {
		if sm.queuedUpdates == nil {
			sm.queuedUpdates = make(map[UpdateKey]StateUpdate)
		}
		sm.queuedUpdates[UpdateKey{ScopeID: scopeID, Key: key}] = DeleteStateUpdate(key)
	}

	return nil
}

// applyUnpublishedUpdates applies queued updates to a set of keys.
func (sm *StateManager) applyUnpublishedUpdates(scopeID workflow.ScopeID, keys map[string]struct{}) map[string]struct{} {
	for _, key := range sm.getUpdatesForScopeStrict(scopeID) {
		update := sm.queuedUpdates[key]
		if update.IsDelete {
			delete(keys, update.Key)
		} else {
			keys[update.Key] = struct{}{}
		}
	}

	return keys
}

// ReadKeys returns all keys in the given scope.
func (sm *StateManager) ReadKeys(executorID string, scopeName string) map[string]struct{} {
	scopeID := workflow.ScopeID{ExecutorID: executorID, Name: scopeName}
	return sm.ReadKeysByID(scopeID)
}

// ReadKeysByID returns all keys for the given scope ID.
func (sm *StateManager) ReadKeysByID(scopeID workflow.ScopeID) map[string]struct{} {
	scope := sm.getOrCreateScope(scopeID)
	keys := scope.ReadKeys()
	return sm.applyUnpublishedUpdates(scopeID, keys)
}

// ReadState reads state from the given scope.
func (sm *StateManager) ReadState(executorID string, scopeName string, key string) (workflow.PortableValue, bool, error) {
	scopeID := workflow.ScopeID{ExecutorID: executorID, Name: scopeName}
	return sm.ReadStateByID(scopeID, key)
}

// ReadStateByID reads state for the given scope ID and key.
func (sm *StateManager) ReadStateByID(scopeID workflow.ScopeID, key string) (workflow.PortableValue, bool, error) {
	if key == "" {
		return workflow.PortableValue{}, false, fmt.Errorf("key cannot be empty")
	}

	stateKey := UpdateKey{ScopeID: scopeID, Key: key}

	// Check queued updates first
	if update, hasUpdate := sm.queuedUpdates[stateKey]; hasUpdate {
		if update.IsDelete || update.Value == nil {
			return workflow.PortableValue{}, false, nil
		}
		return workflow.AnyPortableValue(update.Value), true, nil
	}

	// Read from scope
	scope := sm.getOrCreateScope(scopeID)
	value, ok := scope.ReadState(key)
	return value, ok, nil
}

// ReadOrInitState reads state or initializes it with the factory function.
func (sm *StateManager) ReadOrInitState(executorID string, scopeName string, key string, factory func() any) (workflow.PortableValue, error) {
	scopeID := workflow.ScopeID{ExecutorID: executorID, Name: scopeName}
	return sm.ReadOrInitStateByID(scopeID, key, factory)
}

// ReadOrInitStateByID reads state or initializes it for the given scope ID.
func (sm *StateManager) ReadOrInitStateByID(scopeID workflow.ScopeID, key string, factory func() any) (workflow.PortableValue, error) {
	value, ok, err := sm.ReadStateByID(scopeID, key)
	if err != nil {
		return workflow.PortableValue{}, err
	}

	if !ok {
		if factory == nil {
			return workflow.PortableValue{}, fmt.Errorf("factory function cannot be nil when initializing state")
		}
		newValue := factory()
		if err := sm.WriteStateByID(scopeID, key, newValue); err != nil {
			return workflow.PortableValue{}, err
		}
		return workflow.AnyPortableValue(newValue), nil
	}

	return value, nil
}

// WriteState writes state to the given scope.
func (sm *StateManager) WriteState(executorID string, scopeName string, key string, value any) error {
	scopeID := workflow.ScopeID{ExecutorID: executorID, Name: scopeName}
	return sm.WriteStateByID(scopeID, key, value)
}

// WriteStateByID writes state for the given scope ID.
func (sm *StateManager) WriteStateByID(scopeID workflow.ScopeID, key string, value any) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	if sm.queuedUpdates == nil {
		sm.queuedUpdates = make(map[UpdateKey]StateUpdate)
	}
	stateKey := UpdateKey{ScopeID: scopeID, Key: key}
	sm.queuedUpdates[stateKey] = UpdateStateUpdate(key, value)

	return nil
}

// ClearStateKey clears a specific key in the given scope.
func (sm *StateManager) ClearStateKey(executorID string, scopeName string, key string) error {
	scopeID := workflow.ScopeID{ExecutorID: executorID, Name: scopeName}
	return sm.ClearStateKeyByID(scopeID, key)
}

// ClearStateKeyByID clears a specific key for the given scope ID.
func (sm *StateManager) ClearStateKeyByID(scopeID workflow.ScopeID, key string) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	if sm.queuedUpdates == nil {
		sm.queuedUpdates = make(map[UpdateKey]StateUpdate)
	}
	stateKey := UpdateKey{ScopeID: scopeID, Key: key}
	sm.queuedUpdates[stateKey] = DeleteStateUpdate(key)

	return nil
}

// PublishUpdates publishes all queued updates to their respective scopes.
func (sm *StateManager) PublishUpdates(tracer StepTracer) error {
	// Aggregate updates by scope
	updatesByScope := make(map[workflow.ScopeID]map[string][]StateUpdate)

	for key, update := range sm.queuedUpdates {
		if _, ok := updatesByScope[key.ScopeID]; !ok {
			updatesByScope[key.ScopeID] = make(map[string][]StateUpdate)
		}

		scopeUpdates := updatesByScope[key.ScopeID]
		scopeUpdates[key.Key] = append(scopeUpdates[key.Key], update)
	}

	if tracer != nil && len(updatesByScope) > 0 {
		tracer.TraceStatePublished()
	}

	// Apply updates to each scope
	for scopeID, updates := range updatesByScope {
		scope := sm.scopes[scopeID]
		if scope == nil {
			if sm.scopes == nil {
				sm.scopes = make(map[workflow.ScopeID]*StateScope)
			}
			scope = NewStateScope(scopeID)
			sm.scopes[scopeID] = scope
		}

		if err := scope.WriteState(updates); err != nil {
			return err
		}
	}

	// Clear queued updates
	clear(sm.queuedUpdates)

	return nil
}

// ExportState exports all state for checkpointing.
func (sm *StateManager) ExportState() (map[workflow.ScopeKey]workflow.PortableValue, error) {
	if len(sm.queuedUpdates) != 0 {
		return nil, fmt.Errorf("cannot export state while there are queued updates. Call PublishUpdates() first")
	}

	result := make(map[workflow.ScopeKey]workflow.PortableValue)
	for _, scope := range sm.scopes {
		for key, value := range scope.ExportStates() {
			scopeKey := workflow.ScopeKey{
				ID:  scope.ScopeID(),
				Key: key,
			}
			result[scopeKey] = value
		}
	}

	return result, nil
}

// ImportState imports state from a checkpoint.
func (sm *StateManager) ImportState(cp *checkpoint.Checkpoint) error {
	if len(sm.queuedUpdates) != 0 {
		return fmt.Errorf("cannot import state while there are queued updates. Call PublishUpdates() first")
	}

	clear(sm.queuedUpdates)
	clear(sm.scopes)

	for scopeKey, value := range cp.StateData {
		scope, ok := sm.scopes[scopeKey.ID]
		if !ok {
			if sm.scopes == nil {
				sm.scopes = make(map[workflow.ScopeID]*StateScope)
			}
			scope = NewStateScope(scopeKey.ID)
			sm.scopes[scopeKey.ID] = scope
		}
		scope.ImportState(scopeKey.Key, value)
	}

	return nil
}
