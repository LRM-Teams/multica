package memorycuration

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

const (
	DefaultTimezone = "Asia/Shanghai"
	memoryHeader    = "# Agent Memory\n\nSource of truth: Multica agent settings. This file supplements live agent instructions; it does not override them.\n"
	userHeader      = "# User Preferences\n\nDurable user preferences relevant to this Multica agent.\n"
	stateHeader     = "# Agent State\n\nCurrent dated state, temporary facts, and active initiatives.\n"
	reviewHeader    = "# Memory Review\n\nPending memory candidates, conflicts, and curator review notes.\n"
	scratchHeader   = "# Scratchpad\n\nTransient notes that should not be treated as durable memory.\n"
)

type agentRoot struct {
	WorkspaceID string
	AgentID     string
	Root        string
}

func NormalizeStage(raw string) (Stage, error) {
	s := Stage(strings.ToLower(strings.TrimSpace(raw)))
	if s == "" {
		return StageAll, nil
	}
	switch s {
	case StageAgentSelfReview, StageTeamCuration, StageL1, StageL2, StageL3, StageL4, StageAll:
		return s, nil
	case "self_review", "agent_review", "daily_self_review":
		return StageAgentSelfReview, nil
	case "team", "curator", "workspace_curation":
		return StageTeamCuration, nil
	case "l1_daily", "daily":
		return StageL1, nil
	case "l2_review", "review":
		return StageL2, nil
	case "l3_promote", "promote":
		return StageL3, nil
	case "l4_curator":
		return StageL4, nil
	default:
		return "", fmt.Errorf("unknown memory curation stage %q", raw)
	}
}

func ensureMemoryRoot(root string) error {
	return os.MkdirAll(root, 0o755)
}

func ensureFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func discoverAgentRoots(workspacesRoot, workspaceID string, agentIDs []string, allAgents bool) ([]agentRoot, error) {
	workspacesRoot = strings.TrimSpace(workspacesRoot)
	if workspacesRoot == "" {
		return nil, fmt.Errorf("workspaces root is required")
	}
	if len(agentIDs) > 0 && workspaceID == "" {
		return nil, fmt.Errorf("workspace id is required when --agent is used")
	}
	var roots []agentRoot
	if len(agentIDs) > 0 {
		for _, id := range agentIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			roots = append(roots, agentRoot{
				WorkspaceID: workspaceID,
				AgentID:     id,
				Root:        agentworkspace.Root(workspacesRoot, workspaceID, id),
			})
		}
		return roots, nil
	}
	if !allAgents {
		return nil, fmt.Errorf("select at least one --agent or pass --all-agents")
	}
	workspaceIDs := []string{workspaceID}
	if workspaceID == "" {
		entries, err := os.ReadDir(workspacesRoot)
		if err != nil {
			return nil, err
		}
		workspaceIDs = nil
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				workspaceIDs = append(workspaceIDs, entry.Name())
			}
		}
		sort.Strings(workspaceIDs)
	}
	for _, ws := range workspaceIDs {
		base := agentworkspace.AgentsDir(workspacesRoot, ws)
		entries, err := os.ReadDir(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				roots = append(roots, agentRoot{WorkspaceID: ws, AgentID: entry.Name(), Root: agentworkspace.Root(workspacesRoot, ws, entry.Name())})
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].WorkspaceID == roots[j].WorkspaceID {
			return roots[i].AgentID < roots[j].AgentID
		}
		return roots[i].WorkspaceID < roots[j].WorkspaceID
	})
	return roots, nil
}

func appendAudit(root, stage string, planDate time.Time, payload map[string]any, dryRun bool) error {
	if dryRun {
		return nil
	}
	payload["stage"] = stage
	payload["plan_date"] = formatDate(planDate)
	payload["recorded_at"] = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "memory", "audit", fmt.Sprintf("%s-%s.jsonl", stage, formatDate(planDate)))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func fileContentWithoutTemplate(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	lines := strings.Split(string(b), "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "Source of truth: Multica") || strings.Contains(trimmed, "Durable user preferences") || strings.Contains(trimmed, "Current dated state") || strings.Contains(trimmed, "Pending memory candidates") || strings.Contains(trimmed, "Transient notes") || strings.Contains(trimmed, "Concise task history") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n")), nil
}

func AcquireAgentRootFileLock(root string, dryRun bool, now time.Time) (func(), error) {
	if dryRun {
		return func() {}, nil
	}
	lockPath := filepath.Join(root, "memory", ".curation.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	acquire := func() (*os.File, error) {
		return os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	f, err := acquire()
	if os.IsExist(err) {
		info, statErr := os.Stat(lockPath)
		if statErr == nil && now.Sub(info.ModTime()) > 2*time.Hour {
			_ = os.Remove(lockPath)
			f, err = acquire()
		}
	}
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("memory curation already running for agent root")
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "pid=%d\nstarted_at=%s\n", os.Getpid(), now.UTC().Format(time.RFC3339))
	_ = f.Close()
	return func() { _ = os.Remove(lockPath) }, nil
}

func writeIfChanged(path, content string, dryRun bool) (bool, error) {
	old, err := os.ReadFile(path)
	if err == nil && string(old) == content {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if dryRun {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

type fileMutation struct {
	path    string
	content string
}

type fileSnapshot struct {
	path    string
	content []byte
	existed bool
}

type fileMutationTransaction struct {
	dryRun    bool
	snapshots []fileSnapshot
	seen      map[string]struct{}
}

func newFileMutationTransaction(dryRun bool) *fileMutationTransaction {
	return &fileMutationTransaction{dryRun: dryRun, seen: map[string]struct{}{}}
}

func (tx *fileMutationTransaction) commit(mutations []fileMutation) (bool, error) {
	if tx == nil {
		return commitFileMutations(mutations, false)
	}
	if tx.dryRun {
		return commitFileMutations(mutations, true)
	}
	for _, mutation := range mutations {
		if _, ok := tx.seen[mutation.path]; ok {
			continue
		}
		old, err := os.ReadFile(mutation.path)
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		tx.snapshots = append(tx.snapshots, fileSnapshot{path: mutation.path, content: old, existed: err == nil})
		tx.seen[mutation.path] = struct{}{}
	}
	return commitFileMutations(mutations, false)
}

func (tx *fileMutationTransaction) rollback() {
	if tx == nil || tx.dryRun {
		return
	}
	rollbackFileMutations(tx.snapshots)
}

func commitFileMutations(mutations []fileMutation, dryRun bool) (bool, error) {
	changed := false
	snapshots := make([]fileSnapshot, 0, len(mutations))
	for _, mutation := range mutations {
		old, err := os.ReadFile(mutation.path)
		if err != nil && !os.IsNotExist(err) {
			rollbackFileMutations(snapshots)
			return false, err
		}
		if err == nil && string(old) == mutation.content {
			continue
		}
		changed = true
		if dryRun {
			continue
		}
		snapshots = append(snapshots, fileSnapshot{path: mutation.path, content: old, existed: err == nil})
		if err := os.MkdirAll(filepath.Dir(mutation.path), 0o755); err != nil {
			rollbackFileMutations(snapshots)
			return false, err
		}
		if err := os.WriteFile(mutation.path, []byte(mutation.content), 0o644); err != nil {
			rollbackFileMutations(snapshots)
			return false, err
		}
	}
	return changed, nil
}

func rollbackFileMutations(snapshots []fileSnapshot) {
	for i := len(snapshots) - 1; i >= 0; i-- {
		snapshot := snapshots[i]
		if snapshot.existed {
			_ = os.WriteFile(snapshot.path, snapshot.content, 0o644)
		} else {
			_ = os.Remove(snapshot.path)
		}
	}
}

func hashShort(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strings.ToLower(strings.TrimSpace(p))))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func normalizeForDedupe(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func formatDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func dateRange(since, until time.Time) []time.Time {
	start := dateOnly(since)
	end := dateOnly(until)
	if end.Before(start) {
		return nil
	}
	var out []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

func dateOnly(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func sectionLines(content, heading string) []string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	in := false
	var out []string
	needle := "## " + heading
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			in = trimmed == needle
			continue
		}
		if in && strings.HasPrefix(trimmed, "-") {
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if text != "" && text != "..." {
				out = append(out, text)
			}
		}
	}
	return out
}
