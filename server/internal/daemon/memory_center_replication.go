package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/memorysync"
)

const (
	memorySyncStateRel  = ".multica/memory-sync-state.json"
	memorySyncOutboxRel = ".multica/memory-sync-outbox.json"
)

type memorySyncState struct {
	Cursor       int64                                `json:"cursor"`
	LocalAtoms   map[string]AgentMemoryCenterSyncAtom `json:"local_atoms"`
	RemoteActive map[string]AgentMemoryHydrateEntry   `json:"remote_active"`
}

type memorySyncBatch struct {
	ID                  string                      `json:"id"`
	TaskID              string                      `json:"task_id,omitempty"`
	Entries             []AgentMemoryCenterSyncAtom `json:"entries,omitempty"`
	DeletedIdentityKeys []string                    `json:"deleted_identity_keys,omitempty"`
}

type memorySyncOutbox struct {
	Batches []memorySyncBatch `json:"batches"`
}

var memoryCenterSyncLocks sync.Map

func lockMemoryCenterSync(agentRoot string) func() {
	value, _ := memoryCenterSyncLocks.LoadOrStore(agentRoot, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// reconcileAgentMemoryCenter durably queues local changes, flushes them, then
// applies center changes newer than the local cursor. Local writes always enter
// the outbox before the local atom index advances.
func (d *WorkspaceDaemonCore) reconcileAgentMemoryCenter(ctx context.Context, workspaceID, agentID, runtimeID, taskID, agentRoot string) error {
	if d == nil || d.client == nil {
		return nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	agentID = strings.TrimSpace(agentID)
	agentRoot = strings.TrimSpace(agentRoot)
	if workspaceID == "" || agentID == "" || agentRoot == "" {
		return nil
	}

	unlock := lockMemoryCenterSync(agentRoot)
	defer unlock()

	state, err := loadMemorySyncState(agentRoot)
	if err != nil {
		return err
	}
	outbox, err := loadMemorySyncOutbox(agentRoot)
	if err != nil {
		return err
	}
	if err := queueLocalMemoryChanges(agentRoot, taskID, &state, &outbox); err != nil {
		return err
	}

	tombstoned, err := d.flushMemorySyncOutbox(ctx, agentID, runtimeID, agentRoot, &outbox)
	if err != nil {
		return err
	}
	for _, identity := range tombstoned {
		if err := removeMemoryIdentity(agentRoot, identity); err != nil {
			return err
		}
	}

	for {
		resp, err := d.client.HydrateAgentMemoryCenter(ctx, AgentMemoryHydrateRequest{
			AgentID:   agentID,
			RuntimeID: runtimeID,
			Cursor:    state.Cursor,
		})
		if err != nil {
			return err
		}
		previousCursor := state.Cursor
		if err := applyMemoryCenterDelta(agentRoot, &state, resp); err != nil {
			return err
		}
		changeCount := len(resp.Active) + len(resp.Conflicts) + len(resp.Deleted)
		if changeCount < 1000 || state.Cursor <= previousCursor {
			break
		}
	}

	current, err := collectPortableMemoryAtoms(agentRoot)
	if err != nil {
		return err
	}
	state.LocalAtoms = current
	return saveMemorySyncState(agentRoot, state)
}

func queueLocalMemoryChanges(agentRoot, taskID string, state *memorySyncState, outbox *memorySyncOutbox) error {
	current, err := collectPortableMemoryAtoms(agentRoot)
	if err != nil {
		return err
	}

	entries := make([]AgentMemoryCenterSyncAtom, 0)
	for key, atom := range current {
		if _, exists := state.LocalAtoms[key]; !exists {
			entries = append(entries, atom)
		}
	}
	currentIdentities := atomIdentitySet(current)
	priorIdentities := atomIdentitySet(state.LocalAtoms)
	deleted := make([]string, 0)
	for identity := range priorIdentities {
		if !currentIdentities[identity] {
			deleted = append(deleted, identity)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		left, right := memoryAtomStorageKey(entries[i]), memoryAtomStorageKey(entries[j])
		return left < right
	})
	sort.Strings(deleted)
	if len(entries) > 0 || len(deleted) > 0 {
		outbox.Batches = append(outbox.Batches, memorySyncBatch{
			ID:                  uuid.NewString(),
			TaskID:              strings.TrimSpace(taskID),
			Entries:             entries,
			DeletedIdentityKeys: deleted,
		})
		if err := saveMemorySyncOutbox(agentRoot, *outbox); err != nil {
			return err
		}
	}

	state.LocalAtoms = current
	return saveMemorySyncState(agentRoot, *state)
}

func (d *WorkspaceDaemonCore) flushMemorySyncOutbox(ctx context.Context, agentID, runtimeID, agentRoot string, outbox *memorySyncOutbox) ([]string, error) {
	tombstoned := make([]string, 0)
	for len(outbox.Batches) > 0 {
		batch := outbox.Batches[0]
		resp, err := d.client.SyncAgentMemoryCenter(ctx, AgentMemoryCenterSyncReport{
			AgentID:             agentID,
			RuntimeID:           runtimeID,
			TaskID:              batch.TaskID,
			MutationID:          batch.ID,
			Entries:             batch.Entries,
			DeletedIdentityKeys: batch.DeletedIdentityKeys,
		})
		if err != nil {
			return tombstoned, err
		}
		if err := validateMemorySyncResponse(batch, resp); err != nil {
			return tombstoned, err
		}
		tombstoned = append(tombstoned, resp.TombstonedIdentityKeys...)
		outbox.Batches = outbox.Batches[1:]
		if err := saveMemorySyncOutbox(agentRoot, *outbox); err != nil {
			return tombstoned, err
		}
	}
	return tombstoned, nil
}

func validateMemorySyncResponse(batch memorySyncBatch, resp AgentMemoryCenterSyncResponse) error {
	if len(batch.DeletedIdentityKeys) > 0 && resp.ProtocolVersion < 2 {
		return fmt.Errorf("memory sync server does not acknowledge tombstones (protocol_version=%d)", resp.ProtocolVersion)
	}
	return nil
}

func collectPortableMemoryAtoms(agentRoot string) (map[string]AgentMemoryCenterSyncAtom, error) {
	paths, err := collectWhitelistedMemoryFiles(agentRoot)
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	atoms := make(map[string]AgentMemoryCenterSyncAtom)
	for _, rel := range paths {
		rel = filepath.ToSlash(rel)
		if !memorysync.IsDurableRelPath(rel) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(agentRoot, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range memorysync.EntriesFromFile(rel, string(data)) {
			if !memorysync.IsPortableContent(entry.Content) || isMemoryScaffold(entry.Content) {
				continue
			}
			atom := AgentMemoryCenterSyncAtom{
				RelPath:   entry.RelPath,
				Scope:     entry.Scope,
				SubjectID: entry.SubjectID,
				Kind:      entry.Kind,
				Topic:     entry.Topic,
				Content:   entry.Content,
			}
			atoms[memoryAtomStorageKey(atom)] = atom
		}
	}
	return atoms, nil
}

func isMemoryScaffold(content string) bool {
	normalized := memorysync.NormalizeContent(content)
	for _, prefix := range []string{
		"Source of truth: Multica agent settings.",
		"Durable user preferences relevant to this Multica agent.",
		"Current dated state, temporary facts, and active initiatives.",
		"Durable preferences stated by this stable workspace member.",
		"Durable collaboration context for this stable workspace member.",
		"Stable facts, conventions, and reusable knowledge for this project.",
		"Current dated status, blockers, active initiatives, and expiring facts for this project.",
		"Durable project decisions and their rationale.",
		"Non-secret purpose, language, routing, and collaboration context for this channel.",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func memoryAtomIdentity(atom AgentMemoryCenterSyncAtom) string {
	return memorysync.IdentityKey(atom.Scope, atom.SubjectID, atom.Kind, atom.Topic, atom.Content)
}

func memoryAtomStorageKey(atom AgentMemoryCenterSyncAtom) string {
	return memoryAtomIdentity(atom) + "|" + memorysync.ContentHash(atom.Content)
}

func atomIdentitySet(atoms map[string]AgentMemoryCenterSyncAtom) map[string]bool {
	out := make(map[string]bool)
	for _, atom := range atoms {
		out[memoryAtomIdentity(atom)] = true
	}
	return out
}

func applyMemoryCenterDelta(agentRoot string, state *memorySyncState, resp AgentMemoryHydrateResponse) error {
	for _, entry := range resp.Deleted {
		if err := removeMemoryIdentity(agentRoot, entry.IdentityKey); err != nil {
			return err
		}
		delete(state.RemoteActive, entry.IdentityKey)
	}

	localAtoms, err := collectPortableMemoryAtoms(agentRoot)
	if err != nil {
		return err
	}
	active := make([]AgentMemoryHydrateEntry, 0, len(resp.Active))
	for _, entry := range resp.Active {
		if !memorysync.IsPortableContent(entry.Content) {
			continue
		}
		for _, atom := range localAtoms {
			if memoryAtomIdentity(atom) != entry.IdentityKey || memorysync.NormalizeContent(atom.Content) == memorysync.NormalizeContent(entry.Content) {
				continue
			}
			if err := removeMemoryBullet(agentRoot, atom.RelPath, atom.Content); err != nil {
				return err
			}
		}
		if prior, ok := state.RemoteActive[entry.IdentityKey]; ok &&
			(prior.RelPath != entry.RelPath || memorysync.NormalizeContent(prior.Content) != memorysync.NormalizeContent(entry.Content)) {
			if err := removeMemoryBullet(agentRoot, prior.RelPath, prior.Content); err != nil {
				return err
			}
		}
		active = append(active, entry)
		state.RemoteActive[entry.IdentityKey] = entry
	}

	conflicts := make([]AgentMemoryHydrateEntry, 0, len(resp.Conflicts))
	for _, entry := range resp.Conflicts {
		if !memorysync.IsPortableContent(entry.Content) {
			continue
		}
		if err := removeMemoryBullet(agentRoot, entry.RelPath, entry.Content); err != nil {
			return err
		}
		conflicts = append(conflicts, entry)
	}
	if err := materializeHydrateEntries(agentRoot, AgentMemoryHydrateResponse{
		Active:    active,
		Conflicts: conflicts,
	}); err != nil {
		return err
	}
	if resp.Cursor > state.Cursor {
		state.Cursor = resp.Cursor
	}
	return nil
}

func removeMemoryIdentity(agentRoot, identity string) error {
	atoms, err := collectPortableMemoryAtoms(agentRoot)
	if err != nil {
		return err
	}
	for _, atom := range atoms {
		if memoryAtomIdentity(atom) != identity {
			continue
		}
		if err := removeMemoryBullet(agentRoot, atom.RelPath, atom.Content); err != nil {
			return err
		}
	}
	return nil
}

func removeMemoryBullet(agentRoot, relPath, content string) error {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if !memorysync.IsDurableRelPath(relPath) {
		return nil
	}
	path := filepath.Join(agentRoot, filepath.FromSlash(relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	target := memorysync.NormalizeContent(content)
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 2 && (strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")) &&
			memorysync.NormalizeContent(trimmed[2:]) == target {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		bullets := memorysync.ExtractBullets(string(data))
		if len(bullets) != 1 || memorysync.NormalizeContent(bullets[0]) != target {
			return nil
		}
		return writeAtomicFile(path, []byte(defaultHeaderForRel(relPath)), 0o644)
	}
	out := strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n"
	return writeAtomicFile(path, []byte(out), 0o644)
}

func loadMemorySyncState(agentRoot string) (memorySyncState, error) {
	state := memorySyncState{
		LocalAtoms:   map[string]AgentMemoryCenterSyncAtom{},
		RemoteActive: map[string]AgentMemoryHydrateEntry{},
	}
	data, err := os.ReadFile(filepath.Join(agentRoot, filepath.FromSlash(memorySyncStateRel)))
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return memorySyncState{}, fmt.Errorf("decode memory sync state: %w", err)
	}
	if state.LocalAtoms == nil {
		state.LocalAtoms = map[string]AgentMemoryCenterSyncAtom{}
	}
	if state.RemoteActive == nil {
		state.RemoteActive = map[string]AgentMemoryHydrateEntry{}
	}
	return state, nil
}

func saveMemorySyncState(agentRoot string, state memorySyncState) error {
	return writeAtomicJSON(filepath.Join(agentRoot, filepath.FromSlash(memorySyncStateRel)), state)
}

func loadMemorySyncOutbox(agentRoot string) (memorySyncOutbox, error) {
	var outbox memorySyncOutbox
	data, err := os.ReadFile(filepath.Join(agentRoot, filepath.FromSlash(memorySyncOutboxRel)))
	if err != nil {
		if os.IsNotExist(err) {
			return outbox, nil
		}
		return outbox, err
	}
	if err := json.Unmarshal(data, &outbox); err != nil {
		return memorySyncOutbox{}, fmt.Errorf("decode memory sync outbox: %w", err)
	}
	return outbox, nil
}

func saveMemorySyncOutbox(agentRoot string, outbox memorySyncOutbox) error {
	return writeAtomicJSON(filepath.Join(agentRoot, filepath.FromSlash(memorySyncOutboxRel)), outbox)
}

func writeAtomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeAtomicFile(path, data, 0o600)
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".memory-sync-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
