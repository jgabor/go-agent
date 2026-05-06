package goagent

import (
	"context"
	"fmt"
	"sync"
)

// NewMemorySessionStore creates an in-memory SessionStore for tests and examples.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: map[string]Session{}}
}

// MemorySessionStore stores sessions in process memory.
type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

// LoadSession returns the stored session for id, or an empty session with that id.
func (s *MemorySessionStore) LoadSession(_ context.Context, id string) (Session, error) {
	if id == "" {
		return Session{}, fmt.Errorf("goagent: session id cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[id]
	if !ok {
		return Session{ID: id}, nil
	}
	return cloneSession(session), nil
}

// SaveSession stores session by its stable id.
func (s *MemorySessionStore) SaveSession(_ context.Context, session Session) error {
	if session.ID == "" {
		return fmt.Errorf("goagent: session id cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessions == nil {
		s.sessions = map[string]Session{}
	}
	s.sessions[session.ID] = cloneSession(session)
	return nil
}
