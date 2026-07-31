package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/memorysignal"
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
	case strings.HasPrefix(rel, "memory/daily/") && strings.HasSuffix(base, ".md"):
		return "agent_daily", "DAILY", true
	case rel == "memory/USER.md":
		return "user", "USER", true
	case strings.HasPrefix(rel, "users/") && base == "USER.md":
		return "user", "USER", true
	case strings.HasPrefix(rel, "users/") && base == "RELATIONSHIP.md":
		return "user", "RELATIONSHIP", true
	case strings.HasPrefix(rel, "channels/") && base == "CONTEXT.md":
		return "channel", "CONTEXT", true
	case strings.HasPrefix(rel, "projects/") && base == "MEMORY.md":
		return "project", "MEMORY", true
	case strings.HasPrefix(rel, "projects/") && base == "STATE.md":
		return "project", "STATE", true
	case strings.HasPrefix(rel, "projects/") && base == "DECISIONS.md":
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

func (d *Daemon) maybeAppendDailyCloseoutStub(task Task, result TaskResult) {
	if d == nil || !taskHasSubstantiveCloseoutSignal(task, result) {
		return
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	agentID := strings.TrimSpace(task.AgentID)
	if workspaceID == "" || agentID == "" {
		return
	}
	now := time.Now().UTC()
	agentRoot := multicaAgentRoot(d.cfg, workspaceID, agentID)
	rel := filepath.ToSlash(filepath.Join("memory", "daily", now.Format("2006-01-02")+".md"))
	prior, err := loadMemoryWriteSnapshot(agentRoot)
	if err != nil {
		return
	}
	abs := filepath.Join(agentRoot, filepath.FromSlash(rel))
	current, readErr := os.ReadFile(abs)
	if readErr != nil && !os.IsNotExist(readErr) {
		return
	}
	if strings.TrimSpace(string(current)) != "" {
		currentHash := hashFileContent(current)
		if prior.Files[rel] == "" || prior.Files[rel] != currentHash {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(current) > 0 && !strings.HasSuffix(string(current), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString(renderDailyCloseoutStub(task, result, now, len(current) == 0))
}

func taskHasSubstantiveCloseoutSignal(task Task, result TaskResult) bool {
	chatMessage := strings.TrimSpace(task.ChatMessage)
	if chatMessage != "" && isSimpleSocialMessage(chatMessage) && !resultHasSubstantiveSignal(result) {
		return false
	}
	if resultHasSubstantiveSignal(result) {
		return true
	}
	if strings.TrimSpace(task.IssueID) != "" || strings.TrimSpace(task.AutopilotRunID) != "" || strings.TrimSpace(task.AutopilotID) != "" || strings.TrimSpace(task.QuickCreatePrompt) != "" || strings.TrimSpace(task.TriggerCommentID) != "" {
		return true
	}
	msg := strings.TrimSpace(task.ChatMessage)
	return msg != "" && !isSimpleSocialMessage(msg)
}

func resultHasSubstantiveSignal(result TaskResult) bool {
	if strings.TrimSpace(result.BranchName) != "" || strings.TrimSpace(result.WorkDir) != "" || len(result.Parts) > 0 || len(result.Usage) > 0 || result.RuntimeStats != nil {
		return true
	}
	action := strings.ToLower(strings.TrimSpace(result.Action))
	return action != "" && action != "no_reply"
}

func isSimpleSocialMessage(message string) bool {
	msg := strings.ToLower(strings.TrimSpace(message))
	msg = strings.Trim(msg, " \t\r\n.!?。！？~～")
	if msg == "" {
		return true
	}
	switch msg {
	case "hi", "hello", "hey", "yo", "thanks", "thank you", "thx", "谢谢", "谢谢你", "感谢", "辛苦了", "贴纸", "sticker":
		return true
	}
	return false
}

func renderDailyCloseoutStub(task Task, result TaskResult, now time.Time, includeHeader bool) string {
	var b strings.Builder
	if includeHeader {
		b.WriteString("# Daily Memory - ")
		b.WriteString(now.Format("2006-01-02"))
		b.WriteString("\n\n")
	}
	b.WriteString("## Auto Closeout - ")
	b.WriteString(now.Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString("- Substantive work completed, but no fresh daily write was detected; the daemon recorded this conservative closeout stub.\n")
	b.WriteString("- Task kind: ")
	b.WriteString(closeoutTaskKind(task))
	if detail := closeoutSignalDetail(task, result); detail != "" {
		b.WriteString("\n- Signal: ")
		b.WriteString(detail)
	}
	b.WriteString("\n")
	return b.String()
}

func closeoutTaskKind(task Task) string {
	switch {
	case strings.TrimSpace(task.IssueID) != "":
		return "issue"
	case strings.TrimSpace(task.AutopilotRunID) != "" || strings.TrimSpace(task.AutopilotID) != "":
		return "autopilot"
	case strings.TrimSpace(task.QuickCreatePrompt) != "":
		return "quick_create"
	case strings.TrimSpace(task.ChatSessionID) != "":
		return "chat"
	default:
		return "task"
	}
}

func closeoutSignalDetail(task Task, result TaskResult) string {
	parts := []string{}
	if v := compactCloseoutValue(result.BranchName); v != "" {
		parts = append(parts, "branch="+v)
	}
	if v := compactCloseoutValue(result.Action); v != "" {
		parts = append(parts, "action="+v)
	}
	if strings.TrimSpace(task.ProjectID) != "" {
		parts = append(parts, "project_id="+compactCloseoutValue(task.ProjectID))
	}
	if len(result.Usage) > 0 {
		parts = append(parts, fmt.Sprintf("usage_entries=%d", len(result.Usage)))
	}
	return strings.Join(parts, ", ")
}

func compactCloseoutValue(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) > 120 {
		return value[:120] + "..."
	}
	return value
}

const memorySignalQueueRel = "sync_queue/memory-signal.jsonl"

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
	if err != nil {
		return
	}
	triggerText := memoryWriteTriggerText(task)
	signals := loadMemorySignals(agentRoot)
	if len(changes) == 0 && !memorysignal.ShouldReportEvenWithoutWrites(triggerText, toMemorySignals(signals)) {
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
}
