package handler

import "sync"

type runnerActivityCursorKey struct {
	workspaceID      string
	agentID          string
	daemonID         string
	daemonInstanceID string
	launchID         string
}

type runnerActivityCursor struct {
	sequence int64
	factID   string
}

// runnerActivityCursorStore is the Raft-shaped sticky note: last seen
// Activity identity for one live Computer process. A new instance or a
// forgotten key starts from zero; nothing here outlives the server process.
type runnerActivityCursorStore struct {
	mu    sync.Mutex
	notes map[runnerActivityCursorKey]runnerActivityCursor
}

func newRunnerActivityCursorStore() *runnerActivityCursorStore {
	return &runnerActivityCursorStore{notes: make(map[runnerActivityCursorKey]runnerActivityCursor)}
}

func (s *runnerActivityCursorStore) accept(key runnerActivityCursorKey, sequence int64, factID string) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notes == nil {
		s.notes = make(map[runnerActivityCursorKey]runnerActivityCursor)
	}
	current, seen := s.notes[key]
	if seen && sequence < current.sequence {
		return false
	}
	if seen && sequence == current.sequence && current.factID != factID {
		return false
	}
	s.notes[key] = runnerActivityCursor{sequence: sequence, factID: factID}
	return true
}

func (s *runnerActivityCursorStore) forgetInstance(workspaceID, daemonID, daemonInstanceID string) {
	s.forgetMatching(workspaceID, daemonID, func(key runnerActivityCursorKey) bool {
		return key.daemonInstanceID == daemonInstanceID
	})
}

func (s *runnerActivityCursorStore) forgetOtherInstances(workspaceID, daemonID, keepInstanceID string) {
	s.forgetMatching(workspaceID, daemonID, func(key runnerActivityCursorKey) bool {
		return key.daemonInstanceID != keepInstanceID
	})
}

func (s *runnerActivityCursorStore) forgetMatching(workspaceID, daemonID string, match func(runnerActivityCursorKey) bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.notes {
		if key.workspaceID == workspaceID && key.daemonID == daemonID && match(key) {
			delete(s.notes, key)
		}
	}
}

func (h *Handler) activityCursor() *runnerActivityCursorStore {
	if h == nil {
		return nil
	}
	if h.runnerActivityCursor == nil {
		h.runnerActivityCursor = newRunnerActivityCursorStore()
	}
	return h.runnerActivityCursor
}
