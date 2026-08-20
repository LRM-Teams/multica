package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type EvolutionSubmissionSyncRequest struct {
	Submissions []EvolutionSubmissionRequest `json:"submissions"`
}

type EvolutionSubmissionRequest struct {
	WorkspaceID    string                    `json:"workspace_id,omitempty"`
	AgentID        string                    `json:"agent_id"`
	UnitType       string                    `json:"unit_type"`
	LocalUnitID    string                    `json:"local_unit_id"`
	Title          string                    `json:"title"`
	Summary        string                    `json:"summary,omitempty"`
	Content        string                    `json:"content,omitempty"`
	Payload        json.RawMessage           `json:"payload,omitempty"`
	ContentHash    string                    `json:"content_hash,omitempty"`
	BundleHash     string                    `json:"bundle_hash,omitempty"`
	BundleRef      string                    `json:"bundle_ref,omitempty"`
	Sensitivity    string                    `json:"sensitivity,omitempty"`
	Confidence     string                    `json:"confidence,omitempty"`
	SuggestedScope string                    `json:"suggested_scope,omitempty"`
	SourceUserID   string                    `json:"source_user_id,omitempty"`
	SubjectType    string                    `json:"subject_type,omitempty"`
	SubjectID      string                    `json:"subject_id,omitempty"`
	Evidence       json.RawMessage           `json:"evidence,omitempty"`
	Applies        json.RawMessage           `json:"applies,omitempty"`
	Tags           []string                  `json:"tags,omitempty"`
	Tools          []string                  `json:"tools,omitempty"`
	TaskTypes      []string                  `json:"task_types,omitempty"`
	ProjectTypes   []string                  `json:"project_types,omitempty"`
	Languages      []string                  `json:"languages,omitempty"`
	Frameworks     []string                  `json:"frameworks,omitempty"`
	SourceCreated  string                    `json:"created_at,omitempty"`
	Files          []EvolutionSubmissionFile `json:"files,omitempty"`
}

type EvolutionSubmissionFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

func (h *Handler) SyncEvolutionSubmissions(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}

	var req EvolutionSubmissionSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp := RuntimeSharedSkillSyncResponse{Status: "ok"}
	for _, incoming := range req.Submissions {
		agent, ok := h.loadRuntimeBoundAgentForSync(r.Context(), &resp, rt, incoming.AgentID)
		if !ok {
			continue
		}
		if incoming.WorkspaceID != "" && incoming.WorkspaceID != uuidToString(rt.WorkspaceID) {
			resp.Errors = append(resp.Errors, RuntimeSharedSkillSyncItemError{Key: incoming.LocalUnitID, Name: incoming.Title, Error: "workspace_id does not match runtime workspace"})
			continue
		}
		status, err := h.syncEvolutionSubmission(r.Context(), rt, agent, incoming)
		if err != nil {
			resp.Errors = append(resp.Errors, RuntimeSharedSkillSyncItemError{Key: incoming.LocalUnitID, Name: incoming.Title, Error: err.Error()})
			continue
		}
		switch status {
		case "created":
			resp.Created++
		case "updated":
			resp.Updated++
		case "unchanged":
			resp.Unchanged++
		}
		resp.Acknowledged = append(resp.Acknowledged, evolutionSubmissionAckKey(incoming.AgentID, incoming.LocalUnitID))
	}
	writeJSON(w, http.StatusOK, resp)
}

func evolutionSubmissionAckKey(agentID, localUnitID string) string {
	return strings.TrimSpace(agentID) + "/" + strings.TrimSpace(localUnitID)
}

func (h *Handler) syncEvolutionSubmission(ctx context.Context, rt db.AgentRuntime, agent db.Agent, incoming EvolutionSubmissionRequest) (string, error) {
	unitType := normalizeEvolutionUnitType(incoming.UnitType)
	if unitType == "" {
		return "", fmt.Errorf("unit_type is required")
	}
	localID := strings.TrimSpace(incoming.LocalUnitID)
	if localID == "" {
		return "", fmt.Errorf("local_unit_id is required")
	}
	title := sanitizeNullBytes(strings.TrimSpace(incoming.Title))
	if title == "" {
		title = localID
	}
	payload := incoming.Payload
	if len(payload) == 0 || !json.Valid(payload) {
		raw, _ := json.Marshal(incoming)
		payload = raw
	}
	payload = mergeEvolutionAttributionPayload(payload, incoming)
	evidence := jsonObjectOrEmpty(incoming.Evidence)
	applies := jsonObjectOrEmpty(incoming.Applies)
	sensitivity := normalizeEvolutionSensitivity(incoming.Sensitivity)
	confidence := normalizeEvolutionConfidence(incoming.Confidence)
	sourceCreatedAt := parseOptionalTimestamptz(incoming.SourceCreated)

	contentHash := strings.TrimSpace(incoming.ContentHash)
	if contentHash == "" && strings.TrimSpace(incoming.Content) != "" {
		contentHash = hashEvolutionContent(incoming.Content)
	}
	bundleHash := strings.TrimSpace(incoming.BundleHash)
	if bundleHash == "" && len(incoming.Files) > 0 {
		bundleHash = hashEvolutionFiles(incoming.Files)
	}

	sourceMemberID := pgtype.UUID{}
	sourceUserID := strings.TrimSpace(incoming.SourceUserID)
	if sourceUserID == "" {
		sourceUserID = evolutionPayloadString(payload, "source_user_id")
	}
	if sourceUserID != "" {
		member, err := h.getWorkspaceMember(ctx, sourceUserID, uuidToString(rt.WorkspaceID))
		if err != nil {
			return "", fmt.Errorf("source_user_id is not a workspace member")
		}
		sourceMemberID = member.ID
	} else {
		if ownerID, err := h.resolveRuntimeOwnerQuery(ctx, rt); err == nil {
			if member, err := h.getWorkspaceMember(ctx, uuidToString(ownerID), uuidToString(rt.WorkspaceID)); err == nil {
				sourceMemberID = member.ID
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(incoming.SubjectType), "member") && strings.TrimSpace(incoming.SubjectID) != "" {
		if _, err := h.getWorkspaceMember(ctx, strings.TrimSpace(incoming.SubjectID), uuidToString(rt.WorkspaceID)); err != nil {
			return "", fmt.Errorf("subject_id is not a workspace member")
		}
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	submission, err := qtx.UpsertEvolutionUnitSubmission(ctx, db.UpsertEvolutionUnitSubmissionParams{
		WorkspaceID:      rt.WorkspaceID,
		SourceAgentID:    agent.ID,
		SourceMemberID:   sourceMemberID,
		UnitType:         unitType,
		LocalUnitID:      localID,
		Title:            title,
		Summary:          sanitizeNullBytes(incoming.Summary),
		Content:          sanitizeNullBytes(incoming.Content),
		Payload:          []byte(payload),
		SanitizedPayload: []byte(payload),
		ContentHash:      contentHash,
		BundleHash:       bundleHash,
		BundleRef:        sanitizeNullBytes(incoming.BundleRef),
		Sensitivity:      sensitivity,
		Confidence:       confidence,
		SuggestedScope:   defaultString(strings.TrimSpace(incoming.SuggestedScope), "workspace"),
		Evidence:         []byte(evidence),
		Applies:          []byte(applies),
		Tags:             cleanStringList(incoming.Tags),
		Tools:            cleanStringList(incoming.Tools),
		TaskTypes:        cleanStringList(incoming.TaskTypes),
		ProjectTypes:     cleanStringList(incoming.ProjectTypes),
		Languages:        cleanStringList(incoming.Languages),
		Frameworks:       cleanStringList(incoming.Frameworks),
		SourceCreatedAt:  sourceCreatedAt,
	})
	if err != nil {
		return "", err
	}

	if err := qtx.DeleteEvolutionSubmissionFiles(ctx, db.DeleteEvolutionSubmissionFilesParams{WorkspaceID: rt.WorkspaceID, SubmissionID: submission.ID}); err != nil {
		return "", err
	}
	for _, file := range incoming.Files {
		path, err := normalizeEvolutionFilePath(file.Path)
		if err != nil {
			return "", err
		}
		content := sanitizeNullBytes(file.Content)
		fileHash := strings.TrimSpace(file.ContentHash)
		if fileHash == "" {
			fileHash = hashEvolutionContent(content)
		}
		mimeType := defaultString(strings.TrimSpace(file.MimeType), "text/plain")
		if strings.EqualFold(path, "SKILL.md") {
			mimeType = "text/markdown"
		}
		if _, err := qtx.UpsertEvolutionSubmissionFile(ctx, db.UpsertEvolutionSubmissionFileParams{WorkspaceID: rt.WorkspaceID, SubmissionID: submission.ID, Path: path, Content: content, ContentHash: fileHash, MimeType: mimeType, SizeBytes: int64(len(content))}); err != nil {
			return "", err
		}
	}
	if sensitivity == "secret" {
		if _, err := tx.Exec(ctx, `UPDATE evolution_unit_submission SET status = 'rejected', reject_reason = 'sensitivity marked secret', updated_at = now() WHERE id = $1 AND workspace_id = $2`, submission.ID, rt.WorkspaceID); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if sensitivity != "secret" {
		if _, err := service.NewEvolutionServiceWithReviewerAndTx(h.Queries, h.TxStarter, h.cfg.EvolutionReviewer, h.cfg.EvolutionReviewEnabled).CurateAndMatchWorkspace(ctx, rt.WorkspaceID, 50); err != nil {
			return "", err
		}
	}
	if submission.CreatedAt.Valid && submission.UpdatedAt.Valid && submission.CreatedAt.Time.Equal(submission.UpdatedAt.Time) {
		return "created", nil
	}
	return "updated", nil
}

func mergeEvolutionAttributionPayload(payload json.RawMessage, incoming EvolutionSubmissionRequest) json.RawMessage {
	envelope := map[string]any{}
	_ = json.Unmarshal(payload, &envelope)
	for key, value := range map[string]string{
		"source_user_id": strings.TrimSpace(incoming.SourceUserID),
		"subject_type":   strings.ToLower(strings.TrimSpace(incoming.SubjectType)),
		"subject_id":     strings.TrimSpace(incoming.SubjectID),
	} {
		if value != "" {
			envelope[key] = value
		}
	}
	encoded, _ := json.Marshal(envelope)
	return encoded
}

func evolutionPayloadString(payload json.RawMessage, key string) string {
	var envelope map[string]any
	if json.Unmarshal(payload, &envelope) != nil {
		return ""
	}
	value, _ := envelope[key].(string)
	return strings.TrimSpace(value)
}

func normalizeEvolutionUnitType(v string) string {
	switch strings.TrimSpace(v) {
	case "memory", "skill", "workflow", "tool_pattern", "preference":
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func normalizeEvolutionSensitivity(v string) string {
	switch strings.TrimSpace(v) {
	case "none", "local_path", "personal", "secret", "unknown":
		return strings.TrimSpace(v)
	default:
		return "unknown"
	}
}

func normalizeEvolutionConfidence(v string) string {
	switch strings.TrimSpace(v) {
	case "low", "medium", "high":
		return strings.TrimSpace(v)
	default:
		return "medium"
	}
}

func normalizeEvolutionFilePath(raw string) (string, error) {
	cleaned := strings.ReplaceAll(sanitizeNullBytes(strings.TrimSpace(raw)), "\\", "/")
	if cleaned == "" || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("invalid file path %q", raw)
	}
	parts := strings.Split(cleaned, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("invalid file path %q", raw)
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return "", fmt.Errorf("invalid file path %q", raw)
	}
	return strings.Join(out, "/"), nil
}

func jsonObjectOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
}

func parseOptionalTimestamptz(raw string) pgtype.Timestamptz {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.Timestamptz{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return pgtype.Timestamptz{Time: t, Valid: true}
	}
	return pgtype.Timestamptz{}
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = sanitizeNullBytes(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func hashEvolutionContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(h[:])
}

func hashEvolutionFiles(files []EvolutionSubmissionFile) string {
	h := sha256.New()
	for _, file := range files {
		_, _ = h.Write([]byte("\x00" + file.Path + "\x00" + file.Content))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
