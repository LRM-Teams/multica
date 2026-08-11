package daemon

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// messageDraft remains the CredentialProxy caller shape until the next Draft
// migration slice. Persistence is already owned by MessageDraftStore.
type messageDraft struct {
	Target          string
	ContextTarget   string
	Content         string
	AttachmentIDs   []string
	ClientMessageID string
	SeenUpToSeq     int64
	SavedAt         time.Time
	HoldCount       int
	Kind            string
}

func coordinatorMessageDraftStore(agentRoot string) (*MessageDraftStore, DraftKey) {
	cleanRoot := filepath.Clean(agentRoot)
	agentID := filepath.Base(cleanRoot)
	workspaceID := filepath.Base(filepath.Dir(cleanRoot))
	if agentsDir := filepath.Dir(cleanRoot); filepath.Base(agentsDir) == "agents" {
		workspaceID = filepath.Base(filepath.Dir(agentsDir))
	}
	return newAgentRootMessageDraftStore(cleanRoot), DraftKey{WorkspaceID: workspaceID, AgentID: agentID}
}

func (c *MessageCoordinator) messageDraftKey(target string) DraftKey {
	key := c.draftKey
	key.Target = target
	return key
}

func (c *MessageCoordinator) SaveNormalMessageDraft(draft messageDraft, now time.Time) (messageDraft, error) {
	draft.Target = strings.TrimSpace(draft.Target)
	draft.ContextTarget = strings.TrimSpace(draft.ContextTarget)
	draft.ClientMessageID = strings.TrimSpace(draft.ClientMessageID)
	if draft.Target == "" || draft.ClientMessageID == "" {
		return messageDraft{}, errors.New("Draft target and internal identity are required")
	}
	draft.AttachmentIDs = append([]string(nil), draft.AttachmentIDs...)

	c.draftMu.Lock()
	defer c.draftMu.Unlock()
	saved, err := c.draftStore.saveAt(c.messageDraftKey(draft.Target), storedMessageDraft(draft), now)
	if err != nil {
		return messageDraft{}, err
	}
	return legacyMessageDraft(saved), nil
}

func (c *MessageCoordinator) LoadMessageDraft(target string, now time.Time) (messageDraft, bool, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return messageDraft{}, false, errors.New("Draft target is required")
	}
	c.draftMu.Lock()
	defer c.draftMu.Unlock()
	draft, found, err := c.draftStore.Load(c.messageDraftKey(target), now)
	if err != nil || !found {
		return messageDraft{}, found, err
	}
	return legacyMessageDraft(draft), true, nil
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
	draft, err := c.draftStore.update(c.messageDraftKey(target), now, func(draft *MessageDraft) error {
		if draft.IdempotencyKey != clientMessageID {
			return errors.New("Draft identity does not match")
		}
		if canonical := strings.TrimSpace(contextTarget); canonical != "" {
			draft.ContextTarget = canonical
		}
		if seenUpToSeq >= 0 {
			draft.SeenUpToSeq = seenUpToSeq
		}
		return nil
	})
	if err != nil {
		return messageDraft{}, err
	}
	return legacyMessageDraft(draft), nil
}

// ClearMessageDraft removes only the same saved identity. An overlapping
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
	return c.draftStore.Clear(c.messageDraftKey(target), clientMessageID)
}

func storedMessageDraft(draft messageDraft) MessageDraft {
	return MessageDraft{
		Target: draft.Target, ContextTarget: draft.ContextTarget, Content: draft.Content,
		AttachmentIDs: append([]string(nil), draft.AttachmentIDs...), IdempotencyKey: draft.ClientMessageID,
		SeenUpToSeq: draft.SeenUpToSeq, SavedAt: draft.SavedAt, HoldCount: draft.HoldCount, Kind: draft.Kind,
	}
}

func legacyMessageDraft(draft MessageDraft) messageDraft {
	return messageDraft{
		Target: draft.Target, ContextTarget: draft.ContextTarget, Content: draft.Content,
		AttachmentIDs: append([]string(nil), draft.AttachmentIDs...), ClientMessageID: draft.IdempotencyKey,
		SeenUpToSeq: draft.SeenUpToSeq, SavedAt: draft.SavedAt, HoldCount: draft.HoldCount, Kind: draft.Kind,
	}
}
