package memorycuration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

type sharedMemoryCandidate struct {
	UnitType       string         `json:"unit_type"`
	LocalUnitID    string         `json:"local_unit_id"`
	Title          string         `json:"title"`
	Summary        string         `json:"summary,omitempty"`
	Content        string         `json:"content"`
	ContentHash    string         `json:"content_hash"`
	Sensitivity    string         `json:"sensitivity"`
	Confidence     string         `json:"confidence"`
	SuggestedScope string         `json:"suggested_scope"`
	Evidence       map[string]any `json:"evidence,omitempty"`
	Applies        map[string]any `json:"applies,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	TaskTypes      []string       `json:"task_types,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
}

func sharedMemoryCandidateForEntry(root agentRoot, entry reviewEntry, now time.Time) (sharedMemoryCandidate, bool) {
	if !entryEligibleForSharedMemory(entry) {
		return sharedMemoryCandidate{}, false
	}
	unitType := sharedUnitType(entry.Type)
	content := strings.TrimSpace(entry.Body)
	localID := "shared_" + entry.ID
	candidate := sharedMemoryCandidate{
		UnitType:       unitType,
		LocalUnitID:    localID,
		Title:          defaultString(entry.Title, sentenceTitle(content)),
		Summary:        truncateSharedSummary(content, 280),
		Content:        content,
		ContentHash:    hashSharedContent(content),
		Sensitivity:    strings.ToLower(strings.TrimSpace(entry.Sensitivity)),
		Confidence:     defaultString(entry.Confidence, "medium"),
		SuggestedScope: "workspace",
		Evidence: map[string]any{
			"source":              "memory_curation_l3",
			"source_review_entry": entry.ID,
			"source_date":         entry.SourceDate,
			"evidence_refs":       entry.Evidence,
			"source_agent_id":     root.AgentID,
		},
		Applies: map[string]any{
			"scope":           "workspace",
			"source_agent_id": root.AgentID,
		},
		Tags:      sharedMemoryTags(entry, content),
		TaskTypes: sharedMemoryTaskTypes(content),
		CreatedAt: now.UTC().Format(time.RFC3339),
	}
	return candidate, true
}

func entryEligibleForSharedMemory(entry reviewEntry) bool {
	if strings.ToLower(strings.TrimSpace(entry.Status)) != "candidate" || strings.ToLower(strings.TrimSpace(entry.Confidence)) != "high" || strings.TrimSpace(entry.Body) == "" {
		return false
	}
	sensitivity := strings.ToLower(strings.TrimSpace(entry.Sensitivity))
	if sensitivity != "none" {
		return false
	}
	scope := strings.ToLower(strings.TrimSpace(entry.Scope))
	switch strings.ToLower(strings.TrimSpace(entry.Type)) {
	case "temporary", "quota":
		return false
	case "preference":
		return scope == "workspace" || scope == "shared" || looksWorkspaceWidePreference(entry.Body)
	case "stable_fact", "memory", "workflow", "tool_pattern":
		return true
	default:
		return scope == "workspace" || scope == "shared"
	}
}

func looksWorkspaceWidePreference(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "workspace") || strings.Contains(lower, "team") || strings.Contains(lower, "agents") || strings.Contains(lower, "repo") || strings.Contains(lower, "project")
}

func sharedUnitType(entryType string) string {
	switch strings.ToLower(strings.TrimSpace(entryType)) {
	case "workflow":
		return "workflow"
	case "tool_pattern":
		return "tool_pattern"
	case "preference":
		return "preference"
	default:
		return "memory"
	}
}

func sharedMemoryTags(entry reviewEntry, content string) []string {
	seen := map[string]bool{"memory-curation": true, "shared-memory": true}
	out := []string{"memory-curation", "shared-memory"}
	add := func(tag string) {
		if tag == "" || seen[tag] {
			return
		}
		seen[tag] = true
		out = append(out, tag)
	}
	switch sharedUnitType(entry.Type) {
	case "workflow":
		add("workflow")
	case "tool_pattern":
		add("tool-pattern")
	case "preference":
		add("preference")
	default:
		add("workspace-fact")
	}
	lower := strings.ToLower(content)
	for tag, needles := range map[string][]string{
		"git":     {"git", "pr", "pull request"},
		"testing": {"test", "ci", "cicd"},
		"memory":  {"memory", "curation", "curator"},
	} {
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				add(tag)
				break
			}
		}
	}
	return out
}

func sharedMemoryTaskTypes(content string) []string {
	lower := strings.ToLower(content)
	var out []string
	add := func(v string) {
		for _, existing := range out {
			if existing == v {
				return
			}
		}
		out = append(out, v)
	}
	if strings.Contains(lower, "pr") || strings.Contains(lower, "pull request") || strings.Contains(lower, "code") || strings.Contains(lower, "repo") {
		add("code-editing")
	}
	if strings.Contains(lower, "test") || strings.Contains(lower, "ci") || strings.Contains(lower, "cicd") {
		add("validation")
	}
	if strings.Contains(lower, "memory") || strings.Contains(lower, "curation") {
		add("memory-curation")
	}
	return out
}
func prepareSharedMemoryCandidateMutations(root string, candidate sharedMemoryCandidate) ([]fileMutation, error) {
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	jsonlPath := filepath.Join(root, "sync_queue", "memory-candidates.jsonl")
	jsonlContent, err := candidateJSONLContent(jsonlPath, candidate.LocalUnitID, string(encoded))
	if err != nil {
		return nil, err
	}
	return []fileMutation{
		{path: filepath.Join(root, "shared-cache", "memory", "proposals", candidate.LocalUnitID+".json"), content: string(encoded) + "\n"},
		{path: jsonlPath, content: jsonlContent},
	}, nil
}
func truncateSharedSummary(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "..."
}

func hashSharedContent(content string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return "sha256:" + hex.EncodeToString(h[:])
}
