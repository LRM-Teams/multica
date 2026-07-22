package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const memoryWriteSnapshotRel = ".multica/memory-write-hashes.json"

// memoryWriteSnapshot persists per-file content hashes between task runs so
// daemon can diff whitelisted agent-local memory paths.
type memoryWriteSnapshot struct {
	Files map[string]string `json:"files"`
}

// memoryWriteChange is one newly-detected memory file write.
type memoryWriteChange struct {
	RelPath     string
	ScopeType   string
	FileKey     string
	ContentHash string
	DeltaChars  int
}

func agentMemoryWriteSnapshotPath(agentRoot string) string {
	return filepath.Join(agentRoot, filepath.FromSlash(memoryWriteSnapshotRel))
}

func loadMemoryWriteSnapshot(agentRoot string) (memoryWriteSnapshot, error) {
	snap := memoryWriteSnapshot{Files: map[string]string{}}
	path := agentMemoryWriteSnapshotPath(agentRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return snap, nil
		}
		return snap, err
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return memoryWriteSnapshot{Files: map[string]string{}}, nil
	}
	if snap.Files == nil {
		snap.Files = map[string]string{}
	}
	return snap, nil
}

func saveMemoryWriteSnapshot(agentRoot string, snap memoryWriteSnapshot) error {
	if snap.Files == nil {
		snap.Files = map[string]string{}
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	path := agentMemoryWriteSnapshotPath(agentRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func hashFileContent(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func classifyMemoryWritePath(rel string) (scopeType, fileKey string, ok bool) {
	rel = filepath.ToSlash(filepath.Clean(rel))
	base := filepath.Base(rel)
	switch {
	case rel == "memory/MEMORY.md":
		return "agent_global", "MEMORY", true
	case rel == "memory/STATE.md":
		return "agent_state", "STATE", true
	case rel == "memory/USER.md":
		return "user", "USER", true
	case strings.HasPrefix(rel, "users/") && base == "USER.md":
		return "user", "USER", true
	case strings.HasPrefix(rel, "channels/") && base == "CONTEXT.md":
		return "channel", "CONTEXT", true
	case strings.HasPrefix(rel, "projects/") && base == "MEMORY.md":
		return "project", "MEMORY", true
	case strings.HasPrefix(rel, "projects/") && base == "DECISIONS.md":
		return "project", "DECISIONS", true
	default:
		return "", "", false
	}
}

func isExcludedMemoryWritePath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "memory/REVIEW.md" {
		return true
	}
	return false
}

func collectWhitelistedMemoryFiles(agentRoot string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(agentRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && path != agentRoot {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(agentRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isExcludedMemoryWritePath(rel) {
			return nil
		}
		if _, _, ok := classifyMemoryWritePath(rel); ok {
			paths = append(paths, rel)
		}
		return nil
	})
	return paths, err
}

func diffAgentMemoryWrites(agentRoot string, prior memoryWriteSnapshot) (memoryWriteSnapshot, []memoryWriteChange, error) {
	next := memoryWriteSnapshot{Files: map[string]string{}}
	for k, v := range prior.Files {
		next.Files[k] = v
	}
	whitelist, err := collectWhitelistedMemoryFiles(agentRoot)
	if err != nil {
		return prior, nil, err
	}
	changes := []memoryWriteChange{}
	for _, rel := range whitelist {
		abs := filepath.Join(agentRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return prior, nil, err
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			continue
		}
		hash := hashFileContent(data)
		next.Files[rel] = hash
		if prior.Files[rel] == hash {
			continue
		}
		scopeType, fileKey, ok := classifyMemoryWritePath(rel)
		if !ok {
			continue
		}
		delta := len(data)
		if oldHash, seen := prior.Files[rel]; seen && oldHash != "" {
			// Approximate delta for UI; exact diff is unnecessary for Phase ①.
			if delta > 0 {
				delta = len(trimmed)
			}
		}
		changes = append(changes, memoryWriteChange{
			RelPath:     rel,
			ScopeType:   scopeType,
			FileKey:     fileKey,
			ContentHash: hash,
			DeltaChars:  delta,
		})
	}
	return next, changes, nil
}

func (d *Daemon) reportAgentMemoryWrites(ctx context.Context, task Task) {
	if d == nil || d.client == nil {
		return
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	agentID := strings.TrimSpace(task.AgentID)
	if workspaceID == "" || agentID == "" {
		return
	}
	agentRoot := multicaAgentRoot(d.cfg, workspaceID, agentID)
	prior, err := loadMemoryWriteSnapshot(agentRoot)
	if err != nil {
		return
	}
	next, changes, err := diffAgentMemoryWrites(agentRoot, prior)
	if err != nil || len(changes) == 0 {
		return
	}
	entries := make([]AgentMemoryWriteEntry, 0, len(changes))
	for _, ch := range changes {
		entries = append(entries, AgentMemoryWriteEntry{
			RelPath:     ch.RelPath,
			ScopeType:   ch.ScopeType,
			FileKey:     ch.FileKey,
			ContentHash: ch.ContentHash,
			DeltaChars:  ch.DeltaChars,
		})
	}
	report := AgentMemoryWriteReport{
		AgentID:   agentID,
		RuntimeID: task.RuntimeID,
		TaskID:    task.ID,
		Writes:    entries,
	}
	if err := d.client.ReportAgentMemoryWrites(ctx, report); err != nil {
		return
	}
	_ = saveMemoryWriteSnapshot(agentRoot, next)
}

// AgentMemoryWriteEntry mirrors protocol.AgentMemoryWriteEntry for the client.
type AgentMemoryWriteEntry struct {
	RelPath     string `json:"rel_path"`
	ScopeType   string `json:"scope_type"`
	FileKey     string `json:"file_key"`
	ContentHash string `json:"content_hash"`
	DeltaChars  int    `json:"delta_chars"`
}

// AgentMemoryWriteReport mirrors protocol.AgentMemoryWriteReport for the client.
type AgentMemoryWriteReport struct {
	AgentID   string                  `json:"agent_id"`
	RuntimeID string                  `json:"runtime_id"`
	TaskID    string                  `json:"task_id,omitempty"`
	Writes    []AgentMemoryWriteEntry `json:"writes"`
}
