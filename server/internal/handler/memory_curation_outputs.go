package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/internal/util"
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
		ScopeType    string          `json:"scope_type"`
		ScopeID      string          `json:"scope_id"`
		Title        string          `json:"title"`
		Content      string          `json:"content"`
		Confidence   float64         `json:"confidence"`
		Sensitivity  string          `json:"sensitivity"`
		EvidenceRefs json.RawMessage `json:"evidence_refs"`
		Applies      json.RawMessage `json:"applies"`
	} `json:"candidates"`
}

type teamCurationOutput struct {
	TeamKnowledge []struct {
		Kind               string          `json:"kind"`
		Title              string          `json:"title"`
		Content            string          `json:"content"`
		SourceCandidateIDs []string        `json:"source_candidate_ids"`
		Metadata           json.RawMessage `json:"metadata"`
		Applies            json.RawMessage `json:"applies"`
	} `json:"team_knowledge"`
	Decisions []struct {
		CandidateID string `json:"candidate_id"`
		Status      string `json:"status"`
		Reason      string `json:"reason"`
	} `json:"decisions"`
	Conflicts []struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"conflicts"`
}

func (h *Handler) persistAgenticCurationOutputs(ctx context.Context, exec dbExecutor, runID, workspaceID, stage string, raw json.RawMessage) error {
	var result memorycuration.Result
	if len(raw) == 0 || json.Unmarshal(raw, &result) != nil {
		return nil
	}
	for _, ar := range result.AgentResults {
		output := strings.TrimSpace(ar.CuratorOutput)
		if output == "" {
			continue
		}
		switch stage {
		case "agent_self_review":
			if err := h.persistSelfReviewOutput(ctx, exec, runID, workspaceID, ar.AgentID, output); err != nil {
				return err
			}
		case "team_curation":
			if err := h.persistTeamCurationOutput(ctx, exec, runID, workspaceID, output); err != nil {
				return err
			}
		case "all":
			if ar.AgentID == "team" {
				if err := h.persistTeamCurationOutput(ctx, exec, runID, workspaceID, output); err != nil {
					return err
				}
			} else if err := h.persistSelfReviewOutput(ctx, exec, runID, workspaceID, ar.AgentID, output); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) persistMemoryCurationAgentRunOutputsFromRaw(ctx context.Context, exec dbExecutor, runID, workspaceID, stage string, raw json.RawMessage) error {
	var result memorycuration.Result
	if len(raw) == 0 || json.Unmarshal(raw, &result) != nil {
		return nil
	}
	return h.persistMemoryCurationAgentRunOutputs(ctx, exec, runID, workspaceID, stage, result)
}

func (h *Handler) persistMemoryCurationAgentRunOutputs(ctx context.Context, exec dbExecutor, runID, workspaceID, stage string, result memorycuration.Result) error {
	if stage != "agent_self_review" && stage != "all" {
		return nil
	}
	errs := map[string]string{}
	for _, item := range result.Errors {
		if strings.TrimSpace(item.AgentID) != "" && item.AgentID != "team" {
			errs[item.AgentID] = strings.TrimSpace(item.Error)
		}
	}
	for _, ar := range result.AgentResults {
		if strings.TrimSpace(ar.AgentID) == "" || ar.AgentID == "team" {
			continue
		}
		status := "succeeded"
		errText := errs[ar.AgentID]
		if errText != "" {
			status = "failed"
		}
		stats, _ := json.Marshal(map[string]any{
			"changed":                 ar.Changed,
			"daily_files_written":     ar.DailyFilesWritten,
			"review_candidates_added": ar.ReviewCandidatesAdded,
			"skill_candidates_added":  ar.SkillCandidatesAdded,
			"evidence_collected":      ar.EvidenceCollected,
			"conflicts_found":         ar.ConflictsFound,
		})
		output, _ := json.Marshal(map[string]any{"curator_output": ar.CuratorOutput})
		if _, err := exec.Exec(ctx, `
			INSERT INTO memory_curation_agent_run (
			  parent_run_id, workspace_id, agent_id, runtime_id, stage, status,
			  stats, output, error, attempt, started_at, finished_at
			)
			SELECT $1::uuid, $2::uuid, $3::uuid, r.runtime_id, 'agent_self_review', $4,
			       $5::jsonb, $6::jsonb, $7, 1, now(), now()
			  FROM memory_curation_run r
			 WHERE r.id = $1::uuid
			ON CONFLICT (parent_run_id, agent_id, stage) DO UPDATE SET
			  runtime_id = COALESCE(memory_curation_agent_run.runtime_id, EXCLUDED.runtime_id),
			  status = EXCLUDED.status,
			  stats = EXCLUDED.stats,
			  output = EXCLUDED.output,
			  error = EXCLUDED.error,
			  attempt = GREATEST(memory_curation_agent_run.attempt, EXCLUDED.attempt),
			  started_at = COALESCE(memory_curation_agent_run.started_at, EXCLUDED.started_at),
			  finished_at = EXCLUDED.finished_at,
			  updated_at = now()
		`, runID, workspaceID, ar.AgentID, status, stats, output, errText); err != nil {
			return err
		}
	}
	return nil
}

func finishUnreportedMemoryCurationAgentRuns(ctx context.Context, exec dbExecutor, runID, parentStatus, parentError string) error {
	childStatus := "skipped"
	errText := ""
	if parentStatus == "failed" {
		childStatus = "failed"
		errText = strings.TrimSpace(parentError)
		if errText == "" {
			errText = "parent memory curation run failed"
		}
	}
	_, err := exec.Exec(ctx, `
		UPDATE memory_curation_agent_run
		   SET status = $2,
		       error = CASE WHEN $2 = 'failed' THEN $3 ELSE error END,
		       finished_at = now(),
		       updated_at = now()
		 WHERE parent_run_id = $1::uuid
		   AND status IN ('queued','waiting_runtime','running')
	`, runID, childStatus, errText)
	return err
}

func (h *Handler) persistSelfReviewOutput(ctx context.Context, exec dbExecutor, runID, workspaceID, agentID, output string) error {
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
		scope := normalizeCandidateScope(firstNonEmpty(c.Scope, c.ScopeType))
		evidence := c.EvidenceRefs
		if len(evidence) == 0 || string(evidence) == "null" {
			evidence = json.RawMessage(`[]`)
		}
		confidence := c.Confidence
		if confidence <= 0 || confidence > 1 {
			confidence = 0.5
		}
		metadata := curationOutputMetadata(mapStringAny("scope_id", strings.TrimSpace(c.ScopeID), "sensitivity", strings.TrimSpace(c.Sensitivity)), "agent_self_review", c.Applies)
		if _, err := exec.Exec(ctx, `
			INSERT INTO agent_memory_curation_candidate (
			  workspace_id, source_agent_id, run_id, candidate_type, scope, title,
			  content, evidence_refs, confidence, metadata
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10::jsonb)
		`, workspaceID, agentID, runID, kind, scope, strings.TrimSpace(c.Title), content, evidence, confidence, metadata); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) persistTeamCurationOutput(ctx context.Context, exec dbExecutor, runID, workspaceID, output string) error {
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
		metadata := curationOutputMetadata(item.Metadata, "team_curation", item.Applies)
		if _, err := exec.Exec(ctx, `
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
	for _, decision := range parsed.Decisions {
		candidateID, err := util.ParseUUID(strings.TrimSpace(decision.CandidateID))
		if err != nil {
			return fmt.Errorf("invalid team curation candidate_id %q", decision.CandidateID)
		}
		status := normalizeTeamCurationDecisionStatus(decision.Status)
		if status == "" {
			return fmt.Errorf("invalid team curation decision status %q", decision.Status)
		}
		tag, err := exec.Exec(ctx, `
			UPDATE agent_memory_curation_candidate
			   SET status = $1,
			       metadata = metadata || jsonb_build_object(
			         'team_curation', jsonb_build_object('run_id',$2::text,'reason',$3::text)
			       ),
			       updated_at = now()
			 WHERE id = $4 AND workspace_id = $5 AND status = 'pending'
		`, status, runID, strings.TrimSpace(decision.Reason), candidateID, workspaceID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("team curation candidate %s is not pending in this workspace", decision.CandidateID)
		}
	}
	for _, conflict := range parsed.Conflicts {
		content := strings.TrimSpace(conflict.Content)
		if content == "" {
			continue
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO agent_memory_curation_candidate (
			  workspace_id, run_id, candidate_type, scope, title, content, metadata
			) VALUES ($1,$2,'conflict','team',$3,$4,jsonb_build_object('source','team_curation'))
		`, workspaceID, runID, strings.TrimSpace(conflict.Title), content); err != nil {
			return err
		}
	}
	return nil
}

func curationOutputMetadata(raw json.RawMessage, source string, appliesRaw json.RawMessage) json.RawMessage {
	metadata := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &metadata)
	}
	metadata["source"] = source
	var applies map[string]any
	if len(appliesRaw) > 0 && string(appliesRaw) != "null" && json.Unmarshal(appliesRaw, &applies) == nil && len(applies) > 0 {
		metadata["applies"] = applies
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return json.RawMessage(`{"source":"` + source + `"}`)
	}
	return payload
}

func mapStringAny(pairs ...string) json.RawMessage {
	metadata := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		key := strings.TrimSpace(pairs[i])
		value := strings.TrimSpace(pairs[i+1])
		if key != "" && value != "" {
			metadata[key] = value
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	return encoded
}

func normalizeTeamCurationDecisionStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "promoted", "rejected", "merged", "superseded":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
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
	case "memory", "user_preference", "relationship", "project_fact", "project_state", "decision", "state", "skill", "team_memory", "team_skill", "conflict", "follow_up":
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
	case "agent", "user", "project", "channel", "workspace", "team":
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
