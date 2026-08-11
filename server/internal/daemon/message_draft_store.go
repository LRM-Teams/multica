package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

// continue-state.json is intentionally separate from consumed-seqs.json:
// boundaries are receive-side coverage while a Draft is unsent local intent.
// The latter may contain Agent-authored text, so it is private to the Agent
// root and is atomically replaced with mode 0600.
const (
	messageDraftsFileName = "continue-state.json"
	messageDraftTTL       = 10 * time.Minute
)

// DraftKey identifies the one durable Draft for a Workspace, Agent, and
// human-facing Message target.
type DraftKey struct {
	WorkspaceID string
	AgentID     string
	Target      string
}

// MessageDraft is unsent send-side intent. IdempotencyKey retains the existing
// client_message_id JSON field so current AgentRoot files remain compatible.
type MessageDraft struct {
	Target         string    `json:"target"`
	ContextTarget  string    `json:"context_target"`
	Content        string    `json:"content"`
	AttachmentIDs  []string  `json:"attachment_ids,omitempty"`
	IdempotencyKey string    `json:"client_message_id"`
	SeenUpToSeq    int64     `json:"seen_up_to_seq"`
	SavedAt        time.Time `json:"saved_at"`
	HoldCount      int       `json:"hold_count,omitempty"`
	Kind           string    `json:"kind,omitempty"`
}

type messageDraftState struct {
	Drafts map[string]MessageDraft `json:"drafts"`
}

// MessageDraftStore owns Draft serialization, expiry, and atomic replacement
// for every AgentRoot under one WorkspacesRoot.
type MessageDraftStore struct {
	mu         sync.Mutex
	now        func() time.Time
	rootForKey func(DraftKey) (string, error)
	writeState func(string, messageDraftState) error
}

func NewMessageDraftStore(workspacesRoot string) *MessageDraftStore {
	return newMessageDraftStore(workspacesRoot, time.Now)
}

func newMessageDraftStore(workspacesRoot string, now func() time.Time) *MessageDraftStore {
	rawWorkspacesRoot := strings.TrimSpace(workspacesRoot)
	workspacesRoot = filepath.Clean(rawWorkspacesRoot)
	return newMessageDraftStoreWithRoot(now, func(key DraftKey) (string, error) {
		if rawWorkspacesRoot == "" {
			return "", errors.New("Draft WorkspacesRoot is required")
		}
		return agentworkspace.Root(workspacesRoot, key.WorkspaceID, key.AgentID), nil
	})
}

func newMessageDraftStoreWithRoot(now func() time.Time, rootForKey func(DraftKey) (string, error)) *MessageDraftStore {
	if now == nil {
		now = time.Now
	}
	store := &MessageDraftStore{now: now, rootForKey: rootForKey}
	store.writeState = func(path string, state messageDraftState) error {
		return writeAtomicJSON(path, state)
	}
	return store
}

func (s *MessageDraftStore) Save(key DraftKey, draft MessageDraft) error {
	if s == nil || s.now == nil {
		return errors.New("Message Draft store is unavailable")
	}
	_, err := s.saveAt(key, draft, s.now())
	return err
}

func (s *MessageDraftStore) saveAt(key DraftKey, draft MessageDraft, now time.Time) (MessageDraft, error) {
	key, path, err := s.path(key)
	if err != nil {
		return MessageDraft{}, err
	}
	draft.IdempotencyKey = strings.TrimSpace(draft.IdempotencyKey)
	if draft.IdempotencyKey == "" {
		return MessageDraft{}, errors.New("Draft internal identity is required")
	}
	if target := strings.TrimSpace(draft.Target); target != "" && target != key.Target {
		return MessageDraft{}, errors.New("Draft target does not match key")
	}
	draft.Target = key.Target
	draft.ContextTarget = strings.TrimSpace(draft.ContextTarget)
	draft.SavedAt = now.UTC()
	draft.AttachmentIDs = append([]string(nil), draft.AttachmentIDs...)

	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadStateLocked(path)
	if err != nil {
		return MessageDraft{}, err
	}
	removeExpiredMessageDrafts(state.Drafts, now)
	if existing, found := state.Drafts[key.Target]; found && existing.IdempotencyKey == draft.IdempotencyKey {
		// Re-driving the same saved intent is not a replacement lifecycle.
		// RecordHold remains the sole operation that increments this count.
		draft.HoldCount = existing.HoldCount
	}
	state.Drafts[key.Target] = draft
	if err := s.writeState(path, state); err != nil {
		return MessageDraft{}, fmt.Errorf("persist local Draft: %w", err)
	}
	return cloneMessageDraft(draft), nil
}

func (s *MessageDraftStore) Load(key DraftKey, now time.Time) (MessageDraft, bool, error) {
	key, path, err := s.path(key)
	if err != nil {
		return MessageDraft{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadStateLocked(path)
	if err != nil {
		return MessageDraft{}, false, err
	}
	draft, found := state.Drafts[key.Target]
	if !found {
		return MessageDraft{}, false, nil
	}
	if messageDraftExpired(draft, now) {
		delete(state.Drafts, key.Target)
		if err := s.writeState(path, state); err != nil {
			return MessageDraft{}, false, fmt.Errorf("remove expired local Draft: %w", err)
		}
		return MessageDraft{}, false, nil
	}
	return cloneMessageDraft(draft), true, nil
}

// UpdateBoundary records the current canonical Message target and local
// coverage without extending Draft expiry. Only a normal save or an actual
// freshness hold keeps an unsent Draft alive.
func (s *MessageDraftStore) UpdateBoundary(key DraftKey, idempotencyKey, contextTarget string, seenUpToSeq int64) (MessageDraft, error) {
	if s == nil || s.now == nil {
		return MessageDraft{}, errors.New("Message Draft store is unavailable")
	}
	return s.updateBoundaryAt(key, idempotencyKey, contextTarget, seenUpToSeq, s.now())
}

func (s *MessageDraftStore) updateBoundaryAt(key DraftKey, idempotencyKey, contextTarget string, seenUpToSeq int64, now time.Time) (MessageDraft, error) {
	return s.updateAt(key, idempotencyKey, contextTarget, seenUpToSeq, now, false)
}

// RecordHold preserves the Draft identity and authored payload while recording
// one freshness hold. A hold extends the ten-minute explicit replay window.
func (s *MessageDraftStore) RecordHold(key DraftKey, idempotencyKey, contextTarget string, seenUpToSeq int64) (MessageDraft, error) {
	if s == nil || s.now == nil {
		return MessageDraft{}, errors.New("Message Draft store is unavailable")
	}
	return s.recordHoldAt(key, idempotencyKey, contextTarget, seenUpToSeq, s.now())
}

func (s *MessageDraftStore) recordHoldAt(key DraftKey, idempotencyKey, contextTarget string, seenUpToSeq int64, now time.Time) (MessageDraft, error) {
	return s.updateAt(key, idempotencyKey, contextTarget, seenUpToSeq, now, true)
}

func (s *MessageDraftStore) Clear(key DraftKey, idempotencyKey string) error {
	key, path, err := s.path(key)
	if err != nil {
		return err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return errors.New("Draft internal identity is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadStateLocked(path)
	if err != nil {
		return err
	}
	if draft, found := state.Drafts[key.Target]; !found || draft.IdempotencyKey != idempotencyKey {
		return nil
	}
	delete(state.Drafts, key.Target)
	if err := s.writeState(path, state); err != nil {
		return fmt.Errorf("clear local Draft: %w", err)
	}
	return nil
}

func (s *MessageDraftStore) updateAt(key DraftKey, idempotencyKey, contextTarget string, seenUpToSeq int64, now time.Time, held bool) (MessageDraft, error) {
	key, path, err := s.path(key)
	if err != nil {
		return MessageDraft{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return MessageDraft{}, errors.New("Draft internal identity is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadStateLocked(path)
	if err != nil {
		return MessageDraft{}, err
	}
	draft, found := state.Drafts[key.Target]
	if !found || messageDraftExpired(draft, now) {
		if found {
			delete(state.Drafts, key.Target)
			if err := s.writeState(path, state); err != nil {
				return MessageDraft{}, fmt.Errorf("remove expired local Draft: %w", err)
			}
		}
		return MessageDraft{}, errors.New("saved Draft not found")
	}
	if draft.IdempotencyKey != idempotencyKey {
		return MessageDraft{}, errors.New("Draft identity does not match")
	}
	if canonical := strings.TrimSpace(contextTarget); canonical != "" {
		draft.ContextTarget = canonical
	}
	if seenUpToSeq >= 0 {
		draft.SeenUpToSeq = seenUpToSeq
	}
	if held {
		draft.HoldCount++
		draft.SavedAt = now.UTC()
	}
	draft.AttachmentIDs = append([]string(nil), draft.AttachmentIDs...)
	state.Drafts[key.Target] = draft
	if err := s.writeState(path, state); err != nil {
		return MessageDraft{}, fmt.Errorf("refresh local Draft: %w", err)
	}
	return cloneMessageDraft(draft), nil
}

func (s *MessageDraftStore) path(key DraftKey) (DraftKey, string, error) {
	if s == nil || s.rootForKey == nil || s.writeState == nil {
		return DraftKey{}, "", errors.New("Message Draft store is unavailable")
	}
	key.WorkspaceID = strings.TrimSpace(key.WorkspaceID)
	key.AgentID = strings.TrimSpace(key.AgentID)
	key.Target = strings.TrimSpace(key.Target)
	if !validDraftPathSegment(key.WorkspaceID) || !validDraftPathSegment(key.AgentID) || key.Target == "" {
		return DraftKey{}, "", errors.New("Draft Workspace, Agent, and target key are required")
	}
	root, err := s.rootForKey(key)
	if err != nil {
		return DraftKey{}, "", err
	}
	if strings.TrimSpace(root) == "" {
		return DraftKey{}, "", errors.New("Draft AgentRoot is unavailable")
	}
	return key, filepath.Join(root, messageDraftsFileName), nil
}

func (s *MessageDraftStore) loadStateLocked(path string) (messageDraftState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return messageDraftState{Drafts: make(map[string]MessageDraft)}, nil
	}
	if err != nil {
		return messageDraftState{}, fmt.Errorf("read local Drafts: %w", err)
	}
	var state messageDraftState
	if err := json.Unmarshal(data, &state); err != nil {
		// Draft corruption must not create a new identity over unknown intent.
		return messageDraftState{}, fmt.Errorf("decode local Drafts: %w", err)
	}
	if state.Drafts == nil {
		state.Drafts = make(map[string]MessageDraft)
	}
	return state, nil
}

func validDraftPathSegment(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, `/\\`)
}

func removeExpiredMessageDrafts(drafts map[string]MessageDraft, now time.Time) {
	for target, draft := range drafts {
		if messageDraftExpired(draft, now) {
			delete(drafts, target)
		}
	}
}

func messageDraftExpired(draft MessageDraft, now time.Time) bool {
	return draft.SavedAt.IsZero() || !now.Before(draft.SavedAt.Add(messageDraftTTL))
}

func cloneMessageDraft(draft MessageDraft) MessageDraft {
	draft.AttachmentIDs = append([]string(nil), draft.AttachmentIDs...)
	return draft
}
