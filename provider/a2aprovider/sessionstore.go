// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/microsoft/agent-framework-go/agent"
)

// SessionStore persists hosted agent sessions by A2A context ID.
// A context ID is independent of [agent.Session.ServiceID].
//
// Implementations must support concurrent calls. Get must return an independently
// owned session, and Save must snapshot the session without retaining its pointer.
// The caller must not mutate a session concurrently with Save.
//
// A store must be scoped to one hosted agent. Hosts serving multiple users or
// tenants must also isolate keys using trusted caller identity; context IDs alone
// are not an authorization boundary. A shared custom store must enforce these
// namespaces, for example by wrapping its keys using trusted context values.
type SessionStore interface {
	// Get retrieves a session, or returns (nil, nil) when contextID is not stored.
	Get(ctx context.Context, contextID string) (*agent.Session, error)

	// Save replaces the snapshot for contextID. session must be non-nil.
	// The session must be serializable using encoding/json.
	Save(ctx context.Context, contextID string, session *agent.Session) error
}

type inMemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string][]byte
}

// NewInMemorySessionStore creates a concurrency-safe [SessionStore] that stores
// JSON snapshots in memory. Each Get returns a new session, including its
// provider-specific state and locally retained history.
//
// Snapshots are lost when the store is discarded or the process exits. Concurrent
// saves to the same context ID replace one another; the last save wins. The store
// does not serialize entire agent runs or merge concurrent session branches.
func NewInMemorySessionStore() SessionStore {
	return &inMemorySessionStore{sessions: make(map[string][]byte)}
}

func (s *inMemorySessionStore) Get(ctx context.Context, contextID string) (*agent.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	data, ok := s.sessions[contextID]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	var session agent.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *inMemorySessionStore) Save(ctx context.Context, contextID string, session *agent.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if session == nil {
		return errors.New("session is required")
	}
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sessions[contextID] = data
	s.mu.Unlock()
	return nil
}
