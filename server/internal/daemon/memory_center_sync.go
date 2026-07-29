package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/memorysync"
)

const memoryHydrateMarkerRel = ".multica/memory-hydrate.json"

type memoryHydrateMarker struct {
	HydratedAt string `json:"hydrated_at"`
	AgentID    string `json:"agent_id"`
}

// syncAgentMemoryCenter uploads durable memory bullets after local file changes.
func (d *Daemon) syncAgentMemoryCenter(ctx context.Context, task Task, changes []memoryWriteChange) {
	if d == nil || d.client == nil || len(changes) == 0 {
		return
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	agentID := strings.TrimSpace(task.AgentID)
	if workspaceID == "" || agentID == "" {
		return
	}
	agentRoot := multicaAgentRoot(d.cfg, workspaceID, agentID)
	atoms := make([]AgentMemoryCenterSyncAtom, 0)
	seenFiles := map[string]bool{}
	for _, ch := range changes {
		rel := filepath.ToSlash(ch.RelPath)
		if !memorysync.IsDurableRelPath(rel) || seenFiles[rel] {
			continue
		}
		seenFiles[rel] = true
		data, err := os.ReadFile(filepath.Join(agentRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, entry := range memorysync.EntriesFromFile(rel, string(data)) {
			atoms = append(atoms, AgentMemoryCenterSyncAtom{
				RelPath:   entry.RelPath,
				Scope:     entry.Scope,
				SubjectID: entry.SubjectID,
				Kind:      entry.Kind,
				Topic:     entry.Topic,
				Content:   entry.Content,
			})
		}
	}
	if len(atoms) == 0 {
		return
	}
	_ = d.client.SyncAgentMemoryCenter(ctx, AgentMemoryCenterSyncReport{
		AgentID:   agentID,
		RuntimeID: task.RuntimeID,
		TaskID:    task.ID,
		Entries:   atoms,
	})
}

// hydrateAgentMemoryCenter materializes center active entries into local files
// and conflicts into REVIEW.md. Runs once per agent root until marker exists.
func (d *Daemon) hydrateAgentMemoryCenter(ctx context.Context, workspaceID, agentID, runtimeID, agentRoot string) {
	if d == nil || d.client == nil {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	agentID = strings.TrimSpace(agentID)
	agentRoot = strings.TrimSpace(agentRoot)
	if workspaceID == "" || agentID == "" || agentRoot == "" {
		return
	}
	markerPath := filepath.Join(agentRoot, filepath.FromSlash(memoryHydrateMarkerRel))
	if _, err := os.Stat(markerPath); err == nil {
		return
	}
	resp, err := d.client.HydrateAgentMemoryCenter(ctx, AgentMemoryHydrateRequest{
		AgentID:   agentID,
		RuntimeID: runtimeID,
	})
	if err != nil {
		return
	}
	if err := materializeHydrateEntries(agentRoot, resp); err != nil {
		return
	}
	_ = writeMemoryHydrateMarker(markerPath, agentID)
}

func materializeHydrateEntries(agentRoot string, resp AgentMemoryHydrateResponse) error {
	// Group active bullets by rel_path.
	byPath := map[string][]string{}
	for _, entry := range resp.Active {
		rel := filepath.ToSlash(strings.TrimSpace(entry.RelPath))
		if rel == "" || !memorysync.IsDurableRelPath(rel) {
			continue
		}
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		byPath[rel] = append(byPath[rel], content)
	}
	for rel, bullets := range byPath {
		path := filepath.Join(agentRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		existing, _ := os.ReadFile(path)
		merged := mergeBulletFile(string(existing), bullets, defaultHeaderForRel(rel))
		if err := os.WriteFile(path, []byte(merged), 0o644); err != nil {
			return err
		}
	}
	if len(resp.Conflicts) > 0 {
		reviewPath := filepath.Join(agentRoot, "memory", "REVIEW.md")
		if err := os.MkdirAll(filepath.Dir(reviewPath), 0o755); err != nil {
			return err
		}
		existing, _ := os.ReadFile(reviewPath)
		block := renderConflictReviewBlock(resp.Conflicts)
		if !strings.Contains(string(existing), block) {
			out := strings.TrimRight(string(existing), "\n")
			if out == "" {
				out = "# Memory Review\n\nPending memory candidates, conflicts, and curator review notes.\n"
			}
			out = out + "\n\n" + block + "\n"
			if err := os.WriteFile(reviewPath, []byte(out), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func mergeBulletFile(existing string, bullets []string, header string) string {
	have := map[string]bool{}
	for _, b := range memorysync.ExtractBullets(existing) {
		have[memorysync.NormalizeContent(b)] = true
	}
	var b strings.Builder
	body := strings.TrimSpace(existing)
	if body == "" {
		b.WriteString(header)
		if !strings.HasSuffix(header, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	} else {
		b.WriteString(strings.TrimRight(existing, "\n"))
		b.WriteByte('\n')
	}
	for _, bullet := range bullets {
		norm := memorysync.NormalizeContent(bullet)
		if norm == "" || have[norm] {
			continue
		}
		have[norm] = true
		b.WriteString("- ")
		b.WriteString(norm)
		b.WriteByte('\n')
	}
	return b.String()
}

func defaultHeaderForRel(rel string) string {
	switch {
	case strings.HasSuffix(rel, "/USER.md") || rel == "memory/USER.md":
		return "# User Preferences\n\nDurable user preferences relevant to this Multica agent.\n"
	case strings.HasSuffix(rel, "/RELATIONSHIP.md"):
		return "# Relationship\n\nDurable collaboration preferences and relationship context.\n"
	case strings.HasSuffix(rel, "/CONTEXT.md"):
		return "# Channel Context\n\nNon-secret channel purpose, language, routing, and collaboration defaults.\n"
	case strings.HasSuffix(rel, "/DECISIONS.md"):
		return "# Decisions\n\nDurable project decisions.\n"
	case strings.HasSuffix(rel, "/STATE.md"):
		return "# State\n\nCurrent dated state and temporary facts.\n"
	case strings.HasSuffix(rel, "/MEMORY.md") || rel == "memory/MEMORY.md":
		return "# Memory\n\nDurable memory for this Multica agent.\n"
	default:
		return "# Memory\n\n"
	}
}

func renderConflictReviewBlock(conflicts []AgentMemoryHydrateEntry) string {
	var b strings.Builder
	b.WriteString("## Center Sync Conflicts\n\n")
	b.WriteString("These entries conflict with an existing active center memory (strategy A: first active kept).\n")
	b.WriteString("They are not injected as authoritative rules until reviewed.\n\n")
	for _, c := range conflicts {
		b.WriteString("- topic=`")
		b.WriteString(strings.TrimSpace(c.Topic))
		b.WriteString("` identity=`")
		b.WriteString(strings.TrimSpace(c.IdentityKey))
		b.WriteString("`: ")
		b.WriteString(strings.TrimSpace(c.Content))
		b.WriteByte('\n')
	}
	return b.String()
}

func writeMemoryHydrateMarker(path, agentID string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(memoryHydrateMarker{
		HydratedAt: time.Now().UTC().Format(time.RFC3339),
		AgentID:    agentID,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o644)
}

// AgentMemoryCenterSyncAtom mirrors protocol for the daemon client.
type AgentMemoryCenterSyncAtom struct {
	RelPath   string `json:"rel_path"`
	Scope     string `json:"scope,omitempty"`
	SubjectID string `json:"subject_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Content   string `json:"content"`
}

// AgentMemoryCenterSyncReport mirrors protocol for the daemon client.
type AgentMemoryCenterSyncReport struct {
	AgentID   string                      `json:"agent_id"`
	RuntimeID string                      `json:"runtime_id,omitempty"`
	TaskID    string                      `json:"task_id,omitempty"`
	Entries   []AgentMemoryCenterSyncAtom `json:"entries"`
}

// AgentMemoryHydrateRequest mirrors protocol for the daemon client.
type AgentMemoryHydrateRequest struct {
	AgentID   string `json:"agent_id"`
	RuntimeID string `json:"runtime_id,omitempty"`
}

// AgentMemoryHydrateEntry mirrors protocol for the daemon client.
type AgentMemoryHydrateEntry struct {
	ID          string `json:"id"`
	IdentityKey string `json:"identity_key"`
	Scope       string `json:"scope"`
	SubjectID   string `json:"subject_id,omitempty"`
	Kind        string `json:"kind"`
	Topic       string `json:"topic,omitempty"`
	RelPath     string `json:"rel_path"`
	Content     string `json:"content"`
	Status      string `json:"status"`
	ConflictOf  string `json:"conflict_of,omitempty"`
}

// AgentMemoryHydrateResponse mirrors protocol for the daemon client.
type AgentMemoryHydrateResponse struct {
	Active    []AgentMemoryHydrateEntry `json:"active"`
	Conflicts []AgentMemoryHydrateEntry `json:"conflicts"`
}
