package memorycuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type skillCandidate struct {
	UnitType       string         `json:"unit_type"`
	LocalUnitID    string         `json:"local_unit_id"`
	Title          string         `json:"title"`
	Summary        string         `json:"summary"`
	BundlePath     string         `json:"bundle_path"`
	Sensitivity    string         `json:"sensitivity"`
	Confidence     string         `json:"confidence"`
	SuggestedScope string         `json:"suggested_scope"`
	Evidence       map[string]any `json:"evidence,omitempty"`
	Applies        map[string]any `json:"applies,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Tools          []string       `json:"tools,omitempty"`
	TaskTypes      []string       `json:"task_types,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

func skillCandidateForDecision(root agentRoot, entry reviewEntry, decision L3ReviewDecision, now time.Time) skillCandidate {
	localID := "skill_" + hashShort(entry.ID, entry.HashKey())
	candidate := skillCandidate{
		UnitType:       "skill",
		LocalUnitID:    localID,
		Title:          decision.Skill.Name,
		Summary:        decision.Skill.Description,
		BundlePath:     filepath.ToSlash(filepath.Join("..", "skills", "drafts", localID)),
		Sensitivity:    strings.ToLower(strings.TrimSpace(entry.Sensitivity)),
		Confidence:     "medium",
		SuggestedScope: "workspace",
		Evidence: map[string]any{
			"source":                "memory_curation_l3_reviewer",
			"source_review_entry":   entry.ID,
			"source_date":           entry.SourceDate,
			"evidence_refs":         entry.Evidence,
			"source_agent_id":       root.AgentID,
			"prompt_version":        L3ReviewPromptVersion,
			"requires_human_review": true,
		},
		Applies: map[string]any{
			"scope":           "workspace",
			"source_agent_id": root.AgentID,
		},
		Tags:      append([]string{"memory-curation", "skill-candidate"}, decision.Skill.Tags...),
		Tools:     decision.Skill.Tools,
		TaskTypes: decision.Skill.TaskTypes,
		CreatedAt: now.UTC().Format(time.RFC3339),
	}
	return candidate
}

func renderSkillDraft(draft L3SkillDraft) string {
	return fmt.Sprintf("---\nname: %q\ndescription: %q\n---\n\n%s\n", draft.Name, draft.Description, strings.TrimSpace(draft.Instructions))
}

func recordSkillCandidate(root string, candidate skillCandidate, skillContent string, dryRun bool) (bool, error) {
	mutations, err := prepareSkillCandidateMutations(root, candidate, skillContent)
	if err != nil {
		return false, err
	}
	return commitFileMutations(mutations, dryRun)
}

func prepareSkillCandidateMutations(root string, candidate skillCandidate, skillContent string) ([]fileMutation, error) {
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(root, "sync_queue", "skill-candidates.jsonl")
	manifest, err := candidateJSONLContent(manifestPath, candidate.LocalUnitID, string(encoded))
	if err != nil {
		return nil, err
	}
	return []fileMutation{
		{path: filepath.Join(root, "skills", "drafts", candidate.LocalUnitID, "SKILL.md"), content: skillContent},
		{path: manifestPath, content: manifest},
	}, nil
}

func upsertCandidateJSONL(path, localUnitID, encoded string, dryRun bool) (bool, error) {
	content, err := candidateJSONLContent(path, localUnitID, encoded)
	if err != nil {
		return false, err
	}
	return writeIfChanged(path, content, dryRun)
}

func candidateJSONLContent(path, localUnitID, encoded string) (string, error) {
	var lines []string
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	found := false
	unchanged := false
	for _, line := range strings.Split(string(old), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var item map[string]json.RawMessage
		if json.Unmarshal([]byte(trimmed), &item) != nil {
			lines = append(lines, trimmed)
			continue
		}
		var existingID string
		_ = json.Unmarshal(item["local_unit_id"], &existingID)
		if existingID == localUnitID {
			found = true
			unchanged = trimmed == encoded
			if !unchanged {
				lines = append(lines, encoded)
			}
			continue
		}
		lines = append(lines, trimmed)
	}
	if !found {
		lines = append(lines, encoded)
	}
	if found && unchanged {
		return string(old), nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func skillSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(strings.TrimSpace(b.String()), "-")
}
