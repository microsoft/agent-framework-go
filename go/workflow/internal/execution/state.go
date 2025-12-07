// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"fmt"
	"hash/maphash"
	"iter"
	"maps"

	"github.com/microsoft/agent-framework/go/internal/concurrent"
	"github.com/microsoft/agent-framework/go/internal/hashmap"
	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/internal/checkpoint"
)

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

func (u UpdateKey) Equal(other UpdateKey) bool {
	return u.IsMatchingScope(other.ScopeID, true) && u.Key == other.Key
}

// IsMatchingScope returns true if this update key matches the given scope.
func (u UpdateKey) IsMatchingScope(scopeID workflow.ScopeID, strict bool) bool {
	return u.ScopeID.Equal(scopeID) && (!strict || u.ScopeID.ExecutorID == scopeID.ExecutorID)
}

func (s UpdateKey) Hash(h *maphash.Hash) {
	h.WriteString(s.ScopeID.ExecutorID)
	h.WriteString(s.ScopeID.ScopeName)
	h.WriteString(s.Key)
}

var theSeed = maphash.MakeSeed()

type updateKeyHasher struct{}

var UpdateKeyHasher hashmap.Hasher[UpdateKey] = updateKeyHasher{}

func (updateKeyHasher) Hash(s UpdateKey) uint64 {
	var mh maphash.Hash
	mh.SetSeed(theSeed)
	s.Hash(&mh)
	return mh.Sum64()
}

func (h updateKeyHasher) Equal(a, b UpdateKey) bool {
	return a.Equal(b)
}

// StateManager manages state for all executors in a workflow run.
// A zero value is valid is ready to use.
type StateManager struct {
	scopes        concurrent.HashMap[workflow.ScopeID, *StateScope]
	queuedUpdates concurrent.HashMap[UpdateKey, StateUpdate]
}

func NewStateManager() StateManager {
	return StateManager{
		scopes:        *concurrent.NewHashMap[workflow.ScopeID, *StateScope](workflow.ScopeIDHasher),
		queuedUpdates: *concurrent.NewHashMap[UpdateKey, StateUpdate](UpdateKeyHasher),
	}
}

// getOrCreateScope gets or creates a state scope.
func (sm *StateManager) getOrCreateScope(scopeID workflow.ScopeID) *StateScope {
	if scope, ok := sm.scopes.Load(scopeID); ok {
		return scope
	}
	scope := NewStateScope(scopeID)
	sm.scopes.Swap(scopeID, scope)
	return scope
}

// getUpdatesForScopeStrict returns all queued updates for a specific scope.
func (sm *StateManager) getUpdatesForScopeStrict(scopeID workflow.ScopeID) iter.Seq[UpdateKey] {
	return func(yield func(UpdateKey) bool) {
		for key := range sm.queuedUpdates.Keys() {
			if key.IsMatchingScope(scopeID, true) {
				if !yield(key) {
					return
				}
			}
		}
	}
}

// ClearState clears all state in the given scope.
func (sm *StateManager) ClearState(executorID string, scopeName string) error {
	scopeID := workflow.ScopeID{ExecutorID: executorID, ScopeName: scopeName}
	return sm.ClearStateByID(scopeID)
}

// ClearStateByID clears all state for the given scope ID.
func (sm *StateManager) ClearStateByID(scopeID workflow.ScopeID) error {
	scope, exists := sm.scopes.Load(scopeID)
	if !exists {
		return nil
	}

	keysToDelete := scope.ReadKeys()

	// Mark existing updates as deletes
	for updateKey := range sm.getUpdatesForScopeStrict(scopeID) {
		update, _ := sm.queuedUpdates.Load(updateKey)
		if !update.IsDelete {
			sm.queuedUpdates.Swap(updateKey, DeleteStateUpdate(update.Key))
		}
		delete(keysToDelete, update.Key)
	}

	// Queue deletes for remaining keys
	for key := range keysToDelete {
		sm.queuedUpdates.Swap(UpdateKey{ScopeID: scopeID, Key: key}, DeleteStateUpdate(key))
	}

	return nil
}

// applyUnpublishedUpdates applies queued updates to a set of keys.
func (sm *StateManager) applyUnpublishedUpdates(scopeID workflow.ScopeID, keys map[string]struct{}) map[string]struct{} {
	for key := range sm.getUpdatesForScopeStrict(scopeID) {
		update, _ := sm.queuedUpdates.Load(key)
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
	scopeID := workflow.ScopeID{ExecutorID: executorID, ScopeName: scopeName}
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
	scopeID := workflow.ScopeID{ExecutorID: executorID, ScopeName: scopeName}
	return sm.ReadStateByID(scopeID, key)
}

// ReadStateByID reads state for the given scope ID and key.
func (sm *StateManager) ReadStateByID(scopeID workflow.ScopeID, key string) (workflow.PortableValue, bool, error) {
	if key == "" {
		return workflow.PortableValue{}, false, fmt.Errorf("key cannot be empty")
	}

	stateKey := UpdateKey{ScopeID: scopeID, Key: key}

	// Check queued updates first
	if update, hasUpdate := sm.queuedUpdates.Load(stateKey); hasUpdate {
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
	scopeID := workflow.ScopeID{ExecutorID: executorID, ScopeName: scopeName}
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
	scopeID := workflow.ScopeID{ExecutorID: executorID, ScopeName: scopeName}
	return sm.WriteStateByID(scopeID, key, value)
}

// WriteStateByID writes state for the given scope ID.
func (sm *StateManager) WriteStateByID(scopeID workflow.ScopeID, key string, value any) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	stateKey := UpdateKey{ScopeID: scopeID, Key: key}
	sm.queuedUpdates.Swap(stateKey, UpdateStateUpdate(key, value))

	return nil
}

// ClearStateKey clears a specific key in the given scope.
func (sm *StateManager) ClearStateKey(executorID string, scopeName string, key string) error {
	scopeID := workflow.ScopeID{ExecutorID: executorID, ScopeName: scopeName}
	return sm.ClearStateKeyByID(scopeID, key)
}

// ClearStateKeyByID clears a specific key for the given scope ID.
func (sm *StateManager) ClearStateKeyByID(scopeID workflow.ScopeID, key string) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	stateKey := UpdateKey{ScopeID: scopeID, Key: key}
	sm.queuedUpdates.Swap(stateKey, DeleteStateUpdate(key))

	return nil
}

// PublishUpdates publishes all queued updates to their respective scopes.
func (sm *StateManager) PublishUpdates(tracer StepTracer) error {
	// Aggregate updates by scope
	updatesByScope := hashmap.NewMap[workflow.ScopeID, map[string][]StateUpdate](workflow.ScopeIDHasher)

	for key, update := range sm.queuedUpdates.All() {
		if _, ok := updatesByScope.Load(key.ScopeID); !ok {
			updatesByScope.Set(key.ScopeID, make(map[string][]StateUpdate))
		}

		scopeUpdates, _ := updatesByScope.Load(key.ScopeID)
		scopeUpdates[key.Key] = append(scopeUpdates[key.Key], update)
	}

	if tracer != nil && updatesByScope.Len() > 0 {
		tracer.TraceStatePublished()
	}

	// Apply updates to each scope
	for scopeID, updates := range updatesByScope.All() {
		stateScope := sm.getOrCreateScope(scopeID)
		if err := stateScope.WriteState(updates); err != nil {
			return err
		}
	}

	// Clear queued updates
	sm.queuedUpdates.Clear()

	return nil
}

// ExportState exports all state for checkpointing.
func (sm *StateManager) ExportState() (iter.Seq2[workflow.ScopeKey, workflow.PortableValue], error) {
	for range sm.queuedUpdates.Keys() {
		return nil, fmt.Errorf("cannot export state while there are queued updates. Call PublishUpdates() first")
	}

	return func(yield func(workflow.ScopeKey, workflow.PortableValue) bool) {
		for _, scope := range sm.scopes.All() {
			for key, value := range scope.ExportStates() {
				scopeKey := workflow.ScopeKey{
					ID:  scope.ScopeID(),
					Key: key,
				}
				if !yield(scopeKey, value) {
					return
				}
			}
		}
	}, nil
}

// ImportState imports state from a checkpoint.
func (sm *StateManager) ImportState(cp *checkpoint.Checkpoint) error {
	for range sm.queuedUpdates.Keys() {
		return fmt.Errorf("cannot import state while there are queued updates. Call PublishUpdates() first")
	}

	sm.queuedUpdates.Clear()
	sm.scopes.Clear()

	for scopeKey, value := range cp.StateData.All() {
		scope, ok := sm.scopes.Load(scopeKey.ID)
		if !ok {
			scope = NewStateScope(scopeKey.ID)
			sm.scopes.Swap(scopeKey.ID, scope)
		}
		scope.ImportState(scopeKey.Key, value)
	}

	return nil
}
