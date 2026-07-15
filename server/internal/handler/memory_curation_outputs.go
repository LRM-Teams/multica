package handler

import (
	"context"
	"encoding/json"
	"strings"
)

type curationResultEnvelope struct {
	Stage        string `json:"stage"`
	WorkspaceID  string `json:"workspace_id"`
	AgentResults []struct {
		AgentID       string `json:"agent_id"`
		CuratorOutput string `json:"curator_output"`
	} `json:"agent_results"`
}

type selfReviewOutput struct {
	Candidates []struct {
		Type         string          `json:"type"`
		Scope        string          `json:"scope"`
		Title        string          `json:"title"`
		Content      string          `json:"content"`
		Confidence   float64         `json:"confidence"`
		EvidenceRefs json.RawMessage `json:"evidence_refs"`
	} `json:"candidates"`
}

type teamCurationOutput struct {
	TeamKnowledge []struct {
		Kind               string          `json:"kind"`
		Title              string          `json:"title"`
		Content            string          `json:"content"`
		SourceCandidateIDs []string        `json:"source_candidate_ids"`
		Metadata           json.RawMessage `json:"metadata"`
	} `json:"team_knowledge"`
	Conflicts []struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"conflicts"`
}

func (h *Handler) persistAgenticCurationOutputs(ctx context.Context, runID string, raw json.RawMessage) error {
	var env curationResultEnvelope
	if len(raw) == 0 || json.Unmarshal(raw, &env) != nil {
		return nil
	}
	for _, ar := range env.AgentResults {
		output := strings.TrimSpace(ar.CuratorOutput)
		if output == "" {
			continue
		}
		switch env.Stage {
		case "agent_self_review":
			if err := h.persistSelfReviewOutput(ctx, runID, env.WorkspaceID, ar.AgentID, output); err != nil {
				return err
			}
		case "team_curation":
			if err := h.persistTeamCurationOutput(ctx, runID, env.WorkspaceID, output); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) persistSelfReviewOutput(ctx context.Context, runID, workspaceID, agentID, output string) error {
	var parsed selfReviewOutput
	if !extractJSONObject(output, &parsed) || len(parsed.Candidates) == 0 {
		return nil
	}
	for _, c := range parsed.Candidates {
		content := strings.TrimSpace(c.Content)
		if content == "" {
			continue
		}
		kind := normalizeCandidateType(c.Type)
		scope := normalizeCandidateScope(c.Scope)
		evidence := c.EvidenceRefs
		if len(evidence) == 0 || string(evidence) == "null" {
			evidence = json.RawMessage(`[]`)
		}
		confidence := c.Confidence
		if confidence <= 0 || confidence > 1 {
			confidence = 0.5
		}
		if _, err := h.DB.Exec(ctx, `
			INSERT INTO agent_memory_curation_candidate (
			  workspace_id, source_agent_id, run_id, candidate_type, scope, title,
			  content, evidence_refs, confidence, metadata
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,jsonb_build_object('source','agent_self_review'))
		`, workspaceID, agentID, runID, kind, scope, strings.TrimSpace(c.Title), content, evidence, confidence); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) persistTeamCurationOutput(ctx context.Context, runID, workspaceID, output string) error {
	var parsed teamCurationOutput
	if !extractJSONObject(output, &parsed) {
		return nil
	}
	for _, item := range parsed.TeamKnowledge {
		content := strings.TrimSpace(item.Content)
		title := strings.TrimSpace(item.Title)
		if content == "" || title == "" {
			continue
		}
		kind := normalizeTeamKnowledgeKind(item.Kind)
		metadata := item.Metadata
		if len(metadata) == 0 || string(metadata) == "null" {
			metadata = json.RawMessage(`{}`)
		}
		if _, err := h.DB.Exec(ctx, `
			INSERT INTO team_knowledge_item (
			  workspace_id, kind, title, content, source_candidate_ids,
			  created_by_curator_agent_id, metadata
			)
			SELECT $1,$2,$3,$4,$5::uuid[], curator_agent_id, $6::jsonb
			  FROM memory_curation_run WHERE id = $7
		`, workspaceID, kind, title, content, item.SourceCandidateIDs, metadata, runID); err != nil {
			return err
		}
	}
	for _, conflict := range parsed.Conflicts {
		content := strings.TrimSpace(conflict.Content)
		if content == "" {
			continue
		}
		if _, err := h.DB.Exec(ctx, `
			INSERT INTO agent_memory_curation_candidate (
			  workspace_id, run_id, candidate_type, scope, title, content, metadata
			) VALUES ($1,$2,'conflict','team',$3,$4,jsonb_build_object('source','team_curation'))
		`, workspaceID, runID, strings.TrimSpace(conflict.Title), content); err != nil {
			return err
		}
	}
	return nil
}

func extractJSONObject(output string, dst any) bool {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return false
	}
	return json.Unmarshal([]byte(output[start:end+1]), dst) == nil
}

func normalizeCandidateType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "memory", "user_preference", "state", "skill", "team_memory", "team_skill", "conflict", "follow_up":
		return strings.ToLower(strings.TrimSpace(v))
	case "preference":
		return "user_preference"
	case "temporary":
		return "state"
	default:
		return "memory"
	}
}

func normalizeCandidateScope(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "agent", "user", "workspace", "team":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "agent"
	}
}

func normalizeTeamKnowledgeKind(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "memory", "pattern", "skill", "policy", "troubleshooting":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "memory"
	}
}
