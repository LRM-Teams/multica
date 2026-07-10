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
	case StageL1, StageL2, StageL3, StageL4, StageAll:
		return s, nil
	case "l1_daily", "daily":
		return StageL1, nil
	case "l2_review", "review":
		return StageL2, nil
	case "l3_promote", "promote":
		return StageL3, nil
	case "l4_curator", "curator":
		return StageL4, nil
	default:
		return "", fmt.Errorf("unknown memory curation stage %q", raw)
	}
}

func ensureMemoryRoot(root string) error {
	dirs := []string{
		filepath.Join(root, "memory", "daily"),
		filepath.Join(root, "memory", "audit"),
		filepath.Join(root, "notes"),
		filepath.Join(root, "shared-cache", "memory", "proposals"),
		filepath.Join(root, "sync_queue"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		filepath.Join(root, "memory", "MEMORY.md"):     memoryHeader,
		filepath.Join(root, "memory", "USER.md"):       userHeader,
		filepath.Join(root, "memory", "STATE.md"):      stateHeader,
		filepath.Join(root, "memory", "REVIEW.md"):     reviewHeader,
		filepath.Join(root, "memory", "SCRATCHPAD.md"): scratchHeader,
		filepath.Join(root, "notes", "work-log.md"):    "# Work Log\n\nConcise task history and handoffs.\n",
		filepath.Join(root, "notes", "decisions.md"):   "# Decisions\n\nDurable decisions relevant to this agent.\n",
	}
	for path, content := range files {
		if err := ensureFile(path, content); err != nil {
			return err
		}
	}
	return nil
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
				Root:        filepath.Join(workspacesRoot, workspaceID, ".multica", "agents", id),
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
		base := filepath.Join(workspacesRoot, ws, ".multica", "agents")
		entries, err := os.ReadDir(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				roots = append(roots, agentRoot{WorkspaceID: ws, AgentID: entry.Name(), Root: filepath.Join(base, entry.Name())})
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

func DefaultWorkspacesRoot() string {
	if root := os.Getenv("MULTICA_WORKSPACES_ROOT"); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "multica_workspaces")
}
