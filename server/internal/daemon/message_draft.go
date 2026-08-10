package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// continue-state.json is intentionally separate from consumed-seqs.json:
// boundaries are receive-side coverage while a Draft is unsent local intent.
// The latter may contain Agent-authored text, so it is private to the Agent
// root and is atomically replaced with mode 0600.
const (
	messageDraftsFileName = "continue-state.json"
	messageDraftTTL       = 10 * time.Minute
)

type messageDraftState struct {
	Drafts map[string]messageDraft `json:"drafts"`
}

type messageDraft struct {
	Target          string    `json:"target"`
	ContextTarget   string    `json:"context_target"`
	Content         string    `json:"content"`
	AttachmentIDs   []string  `json:"attachment_ids,omitempty"`
	ClientMessageID string    `json:"client_message_id"`
	SeenUpToSeq     int64     `json:"seen_up_to_seq"`
	SavedAt         time.Time `json:"saved_at"`
	// Kind is the optional structured agent output kind (LRM-1529).
	Kind string `json:"kind,omitempty"`
}

func (c *MessageCoordinator) messageDraftPath() string {
	return filepath.Join(c.root, messageDraftsFileName)
}

func (c *MessageCoordinator) SaveNormalMessageDraft(draft messageDraft, now time.Time) (messageDraft, error) {
	draft.Target = strings.TrimSpace(draft.Target)
	draft.ContextTarget = strings.TrimSpace(draft.ContextTarget)
	draft.ClientMessageID = strings.TrimSpace(draft.ClientMessageID)
	if draft.Target == "" || draft.ClientMessageID == "" {
		return messageDraft{}, errors.New("Draft target and internal identity are required")
	}
	draft.SavedAt = now.UTC()
	draft.AttachmentIDs = append([]string(nil), draft.AttachmentIDs...)

	c.draftMu.Lock()
	defer c.draftMu.Unlock()
	state, err := c.loadMessageDraftStateLocked()
	if err != nil {
		return messageDraft{}, err
	}
	if state.Drafts == nil {
		state.Drafts = make(map[string]messageDraft)
	}
	removeExpiredMessageDrafts(state.Drafts, now)
	state.Drafts[draft.Target] = draft
	if err := c.writeMessageDraftStateLocked(state); err != nil {
		return messageDraft{}, fmt.Errorf("persist local Draft: %w", err)
	}
	return draft, nil
}

func (c *MessageCoordinator) LoadMessageDraft(target string, now time.Time) (messageDraft, bool, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return messageDraft{}, false, errors.New("Draft target is required")
	}
	c.draftMu.Lock()
	defer c.draftMu.Unlock()
	state, err := c.loadMessageDraftStateLocked()
	if err != nil {
		return messageDraft{}, false, err
	}
	draft, found := state.Drafts[target]
	if !found {
		return messageDraft{}, false, nil
	}
	if messageDraftExpired(draft, now) {
		delete(state.Drafts, target)
		if err := c.writeMessageDraftStateLocked(state); err != nil {
			return messageDraft{}, false, fmt.Errorf("remove expired local Draft: %w", err)
		}
		return messageDraft{}, false, nil
	}
	draft.AttachmentIDs = append([]string(nil), draft.AttachmentIDs...)
	return draft, true, nil
}

// RefreshMessageDraft records the canonical target and latest accepted
// freshness state without changing the authored payload or its identity.
func (c *MessageCoordinator) RefreshMessageDraft(target, clientMessageID, contextTarget string, seenUpToSeq int64, now time.Time) (messageDraft, error) {
	target = strings.TrimSpace(target)
	clientMessageID = strings.TrimSpace(clientMessageID)
	if target == "" || clientMessageID == "" {
		return messageDraft{}, errors.New("Draft target and internal identity are required")
	}
	c.draftMu.Lock()
	defer c.draftMu.Unlock()
	state, err := c.loadMessageDraftStateLocked()
	if err != nil {
		return messageDraft{}, err
	}
	draft, found := state.Drafts[target]
	if !found || messageDraftExpired(draft, now) {
		if found {
			delete(state.Drafts, target)
			if err := c.writeMessageDraftStateLocked(state); err != nil {
				return messageDraft{}, fmt.Errorf("remove expired local Draft: %w", err)
			}
		}
		return messageDraft{}, errors.New("saved Draft not found")
	}
	if draft.ClientMessageID != clientMessageID {
		return messageDraft{}, errors.New("Draft identity does not match")
	}
	if canonical := strings.TrimSpace(contextTarget); canonical != "" {
		draft.ContextTarget = canonical
	}
	if seenUpToSeq >= 0 {
		draft.SeenUpToSeq = seenUpToSeq
	}
	draft.SavedAt = now.UTC()
	state.Drafts[target] = draft
	if err := c.writeMessageDraftStateLocked(state); err != nil {
		return messageDraft{}, fmt.Errorf("refresh local Draft: %w", err)
	}
	draft.AttachmentIDs = append([]string(nil), draft.AttachmentIDs...)
	return draft, nil
}

// ClearMessageDraft removes only the same saved identity.  An overlapping
// normal send may already have replaced this target's Draft, and a successful
// older request must never delete that newer intent.
func (c *MessageCoordinator) ClearMessageDraft(target, clientMessageID string) error {
	target = strings.TrimSpace(target)
	clientMessageID = strings.TrimSpace(clientMessageID)
	if target == "" || clientMessageID == "" {
		return errors.New("Draft target and internal identity are required")
	}
	c.draftMu.Lock()
	defer c.draftMu.Unlock()
	state, err := c.loadMessageDraftStateLocked()
	if err != nil {
		return err
	}
	if draft, found := state.Drafts[target]; !found || draft.ClientMessageID != clientMessageID {
		return nil
	}
	delete(state.Drafts, target)
	if err := c.writeMessageDraftStateLocked(state); err != nil {
		return fmt.Errorf("clear local Draft: %w", err)
	}
	return nil
}

func (c *MessageCoordinator) loadMessageDraftStateLocked() (messageDraftState, error) {
	data, err := os.ReadFile(c.messageDraftPath())
	if errors.Is(err, os.ErrNotExist) {
		return messageDraftState{Drafts: make(map[string]messageDraft)}, nil
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
		state.Drafts = make(map[string]messageDraft)
	}
	return state, nil
}

func (c *MessageCoordinator) writeMessageDraftStateLocked(state messageDraftState) error {
	return writeAtomicJSON(c.messageDraftPath(), state)
}

func removeExpiredMessageDrafts(drafts map[string]messageDraft, now time.Time) {
	for target, draft := range drafts {
		if messageDraftExpired(draft, now) {
			delete(drafts, target)
		}
	}
}

func messageDraftExpired(draft messageDraft, now time.Time) bool {
	return draft.SavedAt.IsZero() || !now.Before(draft.SavedAt.Add(messageDraftTTL))
}
