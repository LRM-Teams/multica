package handler

import (
	"context"
	"encoding/json"
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
		Topic        string          `json:"topic"`
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

type memoryCurationAgentRunStatsAggregate struct {
	Changed               bool `json:"changed"`
	DailyFilesWritten     int  `json:"daily_files_written"`
	ReviewCandidatesAdded int  `json:"review_candidates_added"`
	SkillCandidatesAdded  int  `json:"skill_candidates_added"`
	EvidenceCollected     int  `json:"evidence_collected"`
	ConflictsFound        int  `json:"conflicts_found"`
}

type memoryCurationAgentRunStatusCounts struct{ pending, success, failed, skipped int }

// countMemoryCurationChildStatuses classifies agent-run statuses for parent
// finalization. Offline skips are terminal and must not keep the parent pending.
func countMemoryCurationChildStatuses(statuses []string) memoryCurationAgentRunStatusCounts {
	counts := memoryCurationAgentRunStatusCounts{}
	for _, childStatus := range statuses {
		switch childStatus {
		case "succeeded":
			counts.success++
		case "failed", "cancelled":
			counts.failed++
		case "skipped":
			counts.skipped++
		default:
			counts.pending++
		}
	}
	return counts
}

func aggregateMemoryCurationAgentRunStats(ctx context.Context, exec dbExecutor, parentRunID string) (memoryCurationAgentRunStatsAggregate, memoryCurationAgentRunStatusCounts, error) {
	rows, err := exec.Query(ctx, `
		SELECT status,
		       COALESCE(stats, '{}'::jsonb)
		  FROM memory_curation_agent_run
		 WHERE parent_run_id = $1::uuid
	`, parentRunID)
	if err != nil {
		return memoryCurationAgentRunStatsAggregate{}, memoryCurationAgentRunStatusCounts{}, err
	}
	defer rows.Close()
	statuses := make([]string, 0)
	aggregate := memoryCurationAgentRunStatsAggregate{}
	for rows.Next() {
		var childStatus string
		var raw []byte
		if err := rows.Scan(&childStatus, &raw); err != nil {
			return aggregate, memoryCurationAgentRunStatusCounts{}, err
		}
		if raw != nil {
			var cs memoryCurationAgentRunStatsAggregate
			_ = json.Unmarshal(raw, &cs)
			aggregate.Changed = aggregate.Changed || cs.Changed
			aggregate.DailyFilesWritten += cs.DailyFilesWritten
			aggregate.ReviewCandidatesAdded += cs.ReviewCandidatesAdded
			aggregate.SkillCandidatesAdded += cs.SkillCandidatesAdded
			aggregate.EvidenceCollected += cs.EvidenceCollected
			aggregate.ConflictsFound += cs.ConflictsFound
		}
		statuses = append(statuses, childStatus)
	}
	if err := rows.Err(); err != nil {
		return aggregate, memoryCurationAgentRunStatusCounts{}, err
	}
	return aggregate, countMemoryCurationChildStatuses(statuses), nil
}

func mergeMemoryCurationParentStatsWithAgentRuns(ctx context.Context, exec dbExecutor, parentRunID string) error {
	var raw []byte
	if err := exec.QueryRow(ctx, `SELECT stats FROM memory_curation_run WHERE id = $1::uuid`, parentRunID).Scan(&raw); err != nil {
		return err
	}
	stats := map[string]any{}
	_ = json.Unmarshal(raw, &stats)
	child, counts, err := aggregateMemoryCurationAgentRunStats(ctx, exec, parentRunID)
	if err != nil {
		return err
	}
	stats["agents_scanned"] = intFromStats(stats, "agents_scanned") + counts.success + counts.failed + counts.skipped + counts.pending
	stats["agents_changed"] = intFromStats(stats, "agents_changed") + boolToInt(child.Changed)
	stats["daily_files_written"] = intFromStats(stats, "daily_files_written") + child.DailyFilesWritten
	stats["review_candidates_added"] = intFromStats(stats, "review_candidates_added") + child.ReviewCandidatesAdded
	stats["skill_candidates_added"] = intFromStats(stats, "skill_candidates_added") + child.SkillCandidatesAdded
	stats["evidence_collected"] = intFromStats(stats, "evidence_collected") + child.EvidenceCollected
	stats["conflicts_found"] = intFromStats(stats, "conflicts_found") + child.ConflictsFound
	stats["child_success_count"] = counts.success
	stats["child_failed_count"] = counts.failed
	stats["child_skipped_count"] = counts.skipped
	stats["child_pending_count"] = counts.pending
	merged, _ := json.Marshal(stats)
	_, err = exec.Exec(ctx, `UPDATE memory_curation_run SET stats = $2::jsonb WHERE id = $1::uuid`, parentRunID, merged)
	return err
}

func finalizeMemoryCurationParentFromAgentRuns(ctx context.Context, exec dbExecutor, parentRunID string) error {
	var stage, status string
	if err := exec.QueryRow(ctx, `SELECT stage, status FROM memory_curation_run WHERE id = $1::uuid`, parentRunID).Scan(&stage, &status); err != nil {
		return err
	}
	rows, err := exec.Query(ctx, `
		SELECT status,
		       COALESCE(stats, '{}'::jsonb)
		  FROM memory_curation_agent_run
		 WHERE parent_run_id = $1::uuid
	`, parentRunID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type childStats struct {
		Changed               bool `json:"changed"`
		DailyFilesWritten     int  `json:"daily_files_written"`
		ReviewCandidatesAdded int  `json:"review_candidates_added"`
		SkillCandidatesAdded  int  `json:"skill_candidates_added"`
		EvidenceCollected     int  `json:"evidence_collected"`
		ConflictsFound        int  `json:"conflicts_found"`
	}
	statuses := make([]string, 0)
	aggregate := childStats{}
	for rows.Next() {
		var childStatus string
		var raw []byte
		if err := rows.Scan(&childStatus, &raw); err != nil {
			return err
		}
		if raw != nil {
			var cs childStats
			_ = json.Unmarshal(raw, &cs)
			aggregate.Changed = aggregate.Changed || cs.Changed
			aggregate.DailyFilesWritten += cs.DailyFilesWritten
			aggregate.ReviewCandidatesAdded += cs.ReviewCandidatesAdded
			aggregate.SkillCandidatesAdded += cs.SkillCandidatesAdded
			aggregate.EvidenceCollected += cs.EvidenceCollected
			aggregate.ConflictsFound += cs.ConflictsFound
		}
		statuses = append(statuses, childStatus)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	counts := countMemoryCurationChildStatuses(statuses)
	stats, _ := json.Marshal(map[string]any{
		"agents_scanned":          counts.success + counts.failed + counts.skipped + counts.pending,
		"agents_changed":          boolToInt(aggregate.Changed),
		"daily_files_written":     aggregate.DailyFilesWritten,
		"review_candidates_added": aggregate.ReviewCandidatesAdded,
		"skill_candidates_added":  aggregate.SkillCandidatesAdded,
		"evidence_collected":      aggregate.EvidenceCollected,
		"conflicts_found":         aggregate.ConflictsFound,
		"child_success_count":     counts.success,
		"child_failed_count":      counts.failed,
		"child_skipped_count":     counts.skipped,
		"child_pending_count":     counts.pending,
	})
	if stage == "agent_self_review" {
		parentStatus := "failed"
		errorText := "all self-review child runs failed"
		if counts.success == 0 && counts.failed == 0 && counts.skipped > 0 && counts.pending == 0 {
			errorText = "no online agents available for self-review"
		}
		if counts.success > 0 && counts.pending == 0 {
			parentStatus = "succeeded"
			errorText = ""
		} else if counts.pending > 0 {
			parentStatus = ""
			errorText = ""
		}
		_, err = exec.Exec(ctx, `
			UPDATE memory_curation_run
			   SET stats = $2::jsonb,
			       status = CASE WHEN $3 = '' THEN status ELSE $3 END,
			       error = $4,
			       finished_at = CASE WHEN $3 <> '' THEN now() ELSE finished_at END
			 WHERE id = $1::uuid
		`, parentRunID, stats, parentStatus, errorText)
		return err
	}
	if stage == "all" {
		if counts.pending > 0 {
			_, err = exec.Exec(ctx, `
				UPDATE memory_curation_run
				   SET stats = $2::jsonb
				 WHERE id = $1::uuid
			`, parentRunID, stats)
			return err
		}
		if counts.success == 0 {
			_, err = exec.Exec(ctx, `
				UPDATE memory_curation_run
				   SET stats = $2::jsonb, status = 'failed', error = 'no successful self-review child runs', finished_at = now()
				 WHERE id = $1::uuid
			`, parentRunID, stats)
			return err
		}
		_, err = exec.Exec(ctx, `
			UPDATE memory_curation_run
			   SET stats = $2::jsonb
			 WHERE id = $1::uuid
		`, parentRunID, stats)
		return err
	}
	return nil
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
		metadata := curationOutputMetadata(mapStringAny(
			"scope_id", strings.TrimSpace(c.ScopeID),
			"sensitivity", strings.TrimSpace(c.Sensitivity),
			"topic", strings.TrimSpace(c.Topic),
			"topic_key", strings.TrimSpace(c.Topic),
		), "agent_self_review", c.Applies)
		metadata = appendJSONObjectField(metadata, "shareable", selfReviewCandidateShareable(firstNonEmpty(c.Scope, c.ScopeType), strings.TrimSpace(c.Sensitivity)))
		if topic := strings.TrimSpace(c.Topic); topic != "" {
			metadata = appendJSONObjectField(metadata, "dedupe_key", memorycuration.NormalizeTopicKey(firstNonEmpty(c.Scope, c.ScopeType)+"+"+topic))
		}
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

// uuidStringsFromAny keeps only parseable UUIDs. Curator models often emit
// file-slug ids like "agentPrefix:date:slug"; those must not fail the whole run
// when writing team_knowledge_item.source_candidate_ids (uuid[]).
func uuidStringsFromAny(ids []string) []string {
	if len(ids) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := util.ParseUUID(id); err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
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
		// Preserve non-UUID source refs in metadata for audit; only real UUIDs
		// go into the uuid[] column.
		if refs := nonUUIDStrings(item.SourceCandidateIDs); len(refs) > 0 {
			metadata = appendJSONObjectField(metadata, "source_candidate_refs", refs)
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO team_knowledge_item (
			  workspace_id, kind, title, content, source_candidate_ids,
			  created_by_curator_agent_id, metadata
			)
			SELECT $1,$2,$3,$4,$5::uuid[], curator_agent_id, $6::jsonb
			  FROM memory_curation_run WHERE id = $7
		`, workspaceID, kind, title, content, uuidStringsFromAny(item.SourceCandidateIDs), metadata, runID); err != nil {
			return err
		}
	}
	for _, decision := range parsed.Decisions {
		// Skip slug / file-local ids (agentPrefix:date:slug). Only DB candidate
		// UUIDs can update agent_memory_curation_candidate rows.
		candidateID, err := util.ParseUUID(strings.TrimSpace(decision.CandidateID))
		if err != nil {
			continue
		}
		status := normalizeTeamCurationDecisionStatus(decision.Status)
		if status == "" {
			continue
		}
		// Missing / already-resolved candidates are best-effort skips. Hard-fail
		// here used to leave runs stuck in running when curator cited slug ids.
		if _, err := exec.Exec(ctx, `
			UPDATE agent_memory_curation_candidate
			   SET status = $1,
			       metadata = metadata || jsonb_build_object(
			         'team_curation', jsonb_build_object('run_id',$2::text,'reason',$3::text)
			       ),
			       updated_at = now()
			 WHERE id = $4 AND workspace_id = $5 AND status = 'pending'
		`, status, runID, strings.TrimSpace(decision.Reason), candidateID, workspaceID); err != nil {
			return err
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

func nonUUIDStrings(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := util.ParseUUID(id); err == nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

func appendJSONObjectField(raw json.RawMessage, key string, value any) json.RawMessage {
	metadata := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &metadata)
	}
	metadata[key] = value
	payload, err := json.Marshal(metadata)
	if err != nil {
		return raw
	}
	return payload
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

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intFromStats(stats map[string]any, key string) int {
	switch value := stats[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		if i, err := value.Int64(); err == nil {
			return int(i)
		}
	}
	return 0
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
	case "memory", "pattern", "skill", "policy", "troubleshooting", "context", "decision":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "memory"
	}
}

// selfReviewCandidateShareable marks user-private / sensitive prefs as not
// eligible for team curator fan-out. Team curation still sees agent/workspace
// artifacts, but must not promote these private rows.
func selfReviewCandidateShareable(scope, sensitivity string) bool {
	scope = strings.ToLower(strings.TrimSpace(scope))
	sensitivity = strings.ToLower(strings.TrimSpace(sensitivity))
	if scope == "user" {
		return false
	}
	if sensitivity == "sensitive" || sensitivity == "unknown" {
		return false
	}
	return true
}
