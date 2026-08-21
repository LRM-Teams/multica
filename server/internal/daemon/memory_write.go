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
	"sync"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/memoryorigin"
	"github.com/multica-ai/multica/server/internal/memorysignal"
)

const memoryWriteSnapshotRel = ".multica/memory-write-hashes.json"

var memoryWriteReportLocks sync.Map

func lockMemoryWriteReport(agentRoot string) func() {
	value, _ := memoryWriteReportLocks.LoadOrStore(agentRoot, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

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
	parts := strings.Split(rel, "/")
	switch {
	case rel == "memory/MEMORY.md":
		return "agent_global", "MEMORY", true
	case rel == "memory/STATE.md":
		return "agent_state", "STATE", true
	case strings.HasPrefix(rel, "memory/daily/") && strings.HasSuffix(base, ".md"):
		return "agent_daily", "DAILY", true
	case len(parts) == 3 && parts[0] == "users" && parts[1] != "" && base == "USER.md":
		return "user", "USER", true
	case len(parts) == 3 && parts[0] == "users" && parts[1] != "" && base == "RELATIONSHIP.md":
		return "user", "RELATIONSHIP", true
	case len(parts) == 3 && parts[0] == "channels" && parts[1] != "" && base == "CONTEXT.md":
		return "channel", "CONTEXT", true
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "MEMORY.md":
		return "project", "MEMORY", true
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "STATE.md":
		return "project", "STATE", true
	case len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && base == "DECISIONS.md":
		return "project", "DECISIONS", true
	case rel == "notes/agents.md":
		return "agent_notes", "AGENTS", true
	case rel == "notes/relationship-map.md":
		return "agent_notes", "RELATIONSHIP_MAP", true
	case rel == "notes/work-log.md":
		return "agent_notes", "WORK_LOG", true
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

const memorySignalQueueRel = "sync_queue/memory-signal.jsonl"

func (d *Daemon) reportAgentMemoryWrites(ctx context.Context, task Task, friction memorysignal.FrictionVector) {
	if d == nil || d.client == nil {
		return
	}
	if profile, err := taskExecutionProfile(task); err == nil && memoryorigin.SkipDurableCandidates(profile) {
		return
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	agentID := strings.TrimSpace(task.AgentID)
	if workspaceID == "" || agentID == "" {
		return
	}
	agentRoot := agentworkspace.Root(d.cfg.WorkspacesRoot, workspaceID, agentID)
	unlock := lockMemoryWriteReport(agentRoot)
	defer unlock()
	prior, err := loadMemoryWriteSnapshot(agentRoot)
	if err != nil {
		return
	}
	next, changes, err := diffAgentMemoryWrites(agentRoot, prior)
	if err != nil {
		return
	}
	triggerText := memoryWriteTriggerText(task)
	friction = memorysignal.AugmentFrictionFromIssue(friction, task.IssueID, triggerText)
	signals := loadMemorySignals(agentRoot)
	// Non-zero friction forces a report even without writes so the server-side
	// friction guard can queue a lesson candidate (friction-gated memory spec).
	if len(changes) == 0 && friction.IsZero() && !memorysignal.ShouldReportEvenWithoutWrites(triggerText, toMemorySignals(signals)) {
		// A removed durable file produces no current-file write event. Reconcile
		// the atom index anyway so the deletion becomes a center tombstone.
		d.syncAgentMemoryCenter(ctx, task, nil)
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
		AgentID:     agentID,
		RuntimeID:   task.RuntimeID,
		TaskID:      task.ID,
		TriggerText: triggerText,
		InitiatorID: strings.TrimSpace(task.InitiatorID),
		Signals:     signals,
		Writes:      entries,
	}
	if !friction.IsZero() {
		report.Friction = &AgentFrictionVector{
			HumanCorrection: friction.HumanCorrection,
			ActionRejected:  friction.ActionRejected,
			RetryLoop:       friction.RetryLoop,
			Rework:          friction.Rework,
			SelfErrorStreak: friction.SelfErrorStreak,
		}
	}
	if err := d.client.ReportAgentMemoryWrites(ctx, report); err != nil {
		return
	}
	if len(changes) > 0 {
		_ = saveMemoryWriteSnapshot(agentRoot, next)
		d.syncAgentMemoryCenter(ctx, task, changes)
	}
	_ = clearMemorySignalQueue(agentRoot)
}

func memoryWriteTriggerText(task Task) string {
	if msg := strings.TrimSpace(task.ChatMessage); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(task.TriggerCommentContent); msg != "" {
		return msg
	}
	return strings.TrimSpace(task.QuickCreatePrompt)
}

func loadMemorySignals(agentRoot string) []AgentMemorySignal {
	path := filepath.Join(agentRoot, filepath.FromSlash(memorySignalQueueRel))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	parsed := memorysignal.ParseSignalJSONL(string(data))
	if len(parsed) == 0 {
		return nil
	}
	out := make([]AgentMemorySignal, 0, len(parsed))
	for _, s := range parsed {
		out = append(out, AgentMemorySignal{
			Action:     s.Action,
			Kind:       s.Kind,
			Scope:      s.Scope,
			SubjectID:  s.SubjectID,
			Topic:      s.Topic,
			Summary:    s.Summary,
			Importance: s.Importance,
		})
	}
	return out
}

func clearMemorySignalQueue(agentRoot string) error {
	path := filepath.Join(agentRoot, filepath.FromSlash(memorySignalQueueRel))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func toMemorySignals(signals []AgentMemorySignal) []memorysignal.Signal {
	out := make([]memorysignal.Signal, 0, len(signals))
	for _, s := range signals {
		out = append(out, memorysignal.Signal{
			Action:     s.Action,
			Kind:       s.Kind,
			Scope:      s.Scope,
			SubjectID:  s.SubjectID,
			Topic:      s.Topic,
			Summary:    s.Summary,
			Importance: s.Importance,
		})
	}
	return out
}

// AgentMemoryWriteEntry mirrors protocol.AgentMemoryWriteEntry for the client.
type AgentMemoryWriteEntry struct {
	RelPath     string `json:"rel_path"`
	ScopeType   string `json:"scope_type"`
	FileKey     string `json:"file_key"`
	ContentHash string `json:"content_hash"`
	DeltaChars  int    `json:"delta_chars"`
}

// AgentMemorySignal mirrors protocol.AgentMemorySignal for the client.
type AgentMemorySignal struct {
	Action     string `json:"action"`
	Kind       string `json:"kind,omitempty"`
	Scope      string `json:"scope,omitempty"`
	SubjectID  string `json:"subject_id,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Importance string `json:"importance,omitempty"`
}

// AgentMemoryWriteReport mirrors protocol.AgentMemoryWriteReport for the client.
type AgentMemoryWriteReport struct {
	AgentID     string                  `json:"agent_id"`
	RuntimeID   string                  `json:"runtime_id"`
	TaskID      string                  `json:"task_id,omitempty"`
	TriggerText string                  `json:"trigger_text,omitempty"`
	InitiatorID string                  `json:"initiator_id,omitempty"`
	Signals     []AgentMemorySignal     `json:"signals,omitempty"`
	Writes      []AgentMemoryWriteEntry `json:"writes"`
	Friction    *AgentFrictionVector    `json:"friction,omitempty"`
}

// AgentFrictionVector mirrors protocol.AgentFrictionVector for the client.
type AgentFrictionVector struct {
	HumanCorrection int `json:"human_correction,omitempty"`
	ActionRejected  int `json:"action_rejected,omitempty"`
	RetryLoop       int `json:"retry_loop,omitempty"`
	Rework          int `json:"rework,omitempty"`
	SelfErrorStreak int `json:"self_error_streak,omitempty"`
}
