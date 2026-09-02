package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

const (
	evolutionModelAttentionStudent = "attention_student"
	evolutionModelContextFilter    = "context_filter"
)

type evolutionTrainingExampleResponse struct {
	ID                string         `json:"id"`
	WorkspaceID       string         `json:"workspace_id"`
	ModelKind         string         `json:"model_kind"`
	SourceKind        string         `json:"source_kind"`
	SourceID          string         `json:"source_id,omitempty"`
	AgentID           string         `json:"agent_id,omitempty"`
	ChannelID         string         `json:"channel_id,omitempty"`
	MessageID         string         `json:"message_id,omitempty"`
	Input             map[string]any `json:"input"`
	TeacherLabel      map[string]any `json:"teacher_label"`
	StudentPrediction map[string]any `json:"student_prediction"`
	Split             string         `json:"split"`
	Status            string         `json:"status"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
}

type evolutionTrainingExampleUpsertRequest struct {
	ModelKind         string         `json:"model_kind"`
	SourceKind        string         `json:"source_kind"`
	SourceID          string         `json:"source_id"`
	AgentID           string         `json:"agent_id"`
	ChannelID         string         `json:"channel_id"`
	MessageID         string         `json:"message_id"`
	Input             map[string]any `json:"input"`
	TeacherLabel      map[string]any `json:"teacher_label"`
	StudentPrediction map[string]any `json:"student_prediction"`
	Split             string         `json:"split"`
	Status            string         `json:"status"`
}

type evolutionTrainingExamplePatchRequest struct {
	TeacherLabel      map[string]any `json:"teacher_label"`
	StudentPrediction map[string]any `json:"student_prediction"`
	Split             string         `json:"split"`
	Status            string         `json:"status"`
}

type evolutionModelRuntimeConfigResponse struct {
	WorkspaceID      string         `json:"workspace_id"`
	ModelKind        string         `json:"model_kind"`
	Mode             string         `json:"mode"`
	ActiveVersion    string         `json:"active_version"`
	CandidateVersion string         `json:"candidate_version"`
	RolloutPercent   int            `json:"rollout_percent"`
	Config           map[string]any `json:"config"`
	UpdatedBy        string         `json:"updated_by,omitempty"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
}

type evolutionModelRuntimeConfigRequest struct {
	Mode             string         `json:"mode"`
	ActiveVersion    string         `json:"active_version"`
	CandidateVersion string         `json:"candidate_version"`
	RolloutPercent   int            `json:"rollout_percent"`
	Config           map[string]any `json:"config"`
}

type evolutionModelEvalRunResponse struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	ModelKind     string         `json:"model_kind"`
	ModelVersion  string         `json:"model_version"`
	Mode          string         `json:"mode"`
	Status        string         `json:"status"`
	DatasetFilter map[string]any `json:"dataset_filter"`
	Metrics       map[string]any `json:"metrics"`
	ExampleCount  int            `json:"example_count"`
	CreatedAt     string         `json:"created_at"`
}

type evolutionModelEvalRunRequest struct {
	ModelKind     string         `json:"model_kind"`
	ModelVersion  string         `json:"model_version"`
	Mode          string         `json:"mode"`
	Status        string         `json:"status"`
	DatasetFilter map[string]any `json:"dataset_filter"`
	Metrics       map[string]any `json:"metrics"`
}

func (h *Handler) ListEvolutionTrainingExamples(w http.ResponseWriter, r *http.Request) {
	workspaceID, wsUUID, ok := evolutionTrainingWorkspace(w, r)
	if !ok {
		return
	}
	modelKind := strings.TrimSpace(r.URL.Query().Get("model_kind"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	split := strings.TrimSpace(r.URL.Query().Get("split"))
	limit := queryIntInRange(r, "limit", 50, 1, 200)
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, workspace_id, model_kind, source_kind, COALESCE(source_id::text, ''),
		       COALESCE(agent_id::text, ''), COALESCE(channel_id::text, ''), COALESCE(message_id::text, ''),
		       input, teacher_label, student_prediction, split, status, created_at::text, updated_at::text
		FROM evolution_training_example
		WHERE workspace_id = $1
		  AND ($2 = '' OR model_kind = $2)
		  AND ($3 = '' OR status = $3)
		  AND ($4 = '' OR split = $4)
		ORDER BY created_at DESC
		LIMIT $5`, wsUUID, modelKind, status, split, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list training examples")
		return
	}
	defer rows.Close()
	items := []evolutionTrainingExampleResponse{}
	for rows.Next() {
		item, err := scanEvolutionTrainingExample(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan training example")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list training examples")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "examples": items, "total": len(items)})
}

func (h *Handler) CreateEvolutionTrainingExample(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionTrainingWorkspace(w, r)
	if !ok {
		return
	}
	var req evolutionTrainingExampleUpsertRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.SourceKind == "" {
		req.SourceKind = "manual"
	}
	if req.Split == "" {
		req.Split = "unassigned"
	}
	if req.Status == "" {
		req.Status = "candidate"
	}
	if !validEvolutionModelKind(req.ModelKind) || !validTrainingExampleSource(req.SourceKind) || !validTrainingSplit(req.Split) || !validTrainingStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "invalid training example fields")
		return
	}
	input, label, prediction, ok := marshalTrainingExamplePayloads(w, req.Input, req.TeacherLabel, req.StudentPrediction)
	if !ok {
		return
	}
	sourceID, ok := optionalUUIDOrBadRequest(w, req.SourceID, "source_id")
	if !ok {
		return
	}
	if req.SourceKind != "manual" && !sourceID.Valid {
		writeError(w, http.StatusBadRequest, "source_id is required for non-manual training examples")
		return
	}
	agentID, ok := optionalUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	channelID, ok := optionalUUIDOrBadRequest(w, req.ChannelID, "channel_id")
	if !ok {
		return
	}
	messageID, ok := optionalUUIDOrBadRequest(w, req.MessageID, "message_id")
	if !ok {
		return
	}
	var id pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
		INSERT INTO evolution_training_example (
		  workspace_id, model_kind, source_kind, source_id, agent_id, channel_id, message_id,
		  input, teacher_label, student_prediction, split, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11,$12)
		RETURNING id`, wsUUID, req.ModelKind, req.SourceKind, nullableUUID(sourceID), nullableUUID(agentID), nullableUUID(channelID), nullableUUID(messageID), input, label, prediction, req.Split, req.Status).Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create training example")
		return
	}
	item, err := h.loadEvolutionTrainingExample(r.Context(), wsUUID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load training example")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *Handler) UpdateEvolutionTrainingExample(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionTrainingWorkspace(w, r)
	if !ok {
		return
	}
	exampleID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "exampleId"), "example_id")
	if !ok {
		return
	}
	var req evolutionTrainingExamplePatchRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Split == "" {
		req.Split = "__keep__"
	} else if !validTrainingSplit(req.Split) {
		writeError(w, http.StatusBadRequest, "invalid split")
		return
	}
	if req.Status == "" {
		req.Status = "__keep__"
	} else if !validTrainingStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	label, pred, ok := marshalPatchPayloads(w, req.TeacherLabel, req.StudentPrediction)
	if !ok {
		return
	}
	result, err := h.DB.Exec(r.Context(), `
		UPDATE evolution_training_example
		SET teacher_label = CASE WHEN $3::jsonb = '{}'::jsonb THEN teacher_label ELSE $3::jsonb END,
		    student_prediction = CASE WHEN $4::jsonb = '{}'::jsonb THEN student_prediction ELSE $4::jsonb END,
		    split = CASE WHEN $5 = '__keep__' THEN split ELSE $5 END,
		    status = CASE WHEN $6 = '__keep__' THEN status ELSE $6 END,
		    updated_at = now()
		WHERE id = $1 AND workspace_id = $2`, exampleID, wsUUID, label, pred, req.Split, req.Status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update training example")
		return
	}
	if result.RowsAffected() != 1 {
		writeError(w, http.StatusNotFound, "training example not found")
		return
	}
	item, err := h.loadEvolutionTrainingExample(r.Context(), wsUUID, exampleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load training example")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) ListEvolutionModelRuntimeConfigs(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionTrainingWorkspace(w, r)
	if !ok {
		return
	}
	items := make([]evolutionModelRuntimeConfigResponse, 0, 2)
	for _, kind := range []string{evolutionModelAttentionStudent, evolutionModelContextFilter} {
		item, err := h.ensureEvolutionModelRuntimeConfig(r.Context(), wsUUID, kind)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load model runtime config")
			return
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": items, "total": len(items)})
}

func (h *Handler) UpdateEvolutionModelRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionTrainingWorkspace(w, r)
	if !ok {
		return
	}
	modelKind := chi.URLParam(r, "modelKind")
	if !validEvolutionModelKind(modelKind) {
		writeError(w, http.StatusBadRequest, "invalid model kind")
		return
	}
	var req evolutionModelRuntimeConfigRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Mode = strings.TrimSpace(req.Mode)
	if req.Mode == "" {
		req.Mode = "off"
	}
	if req.Mode != "off" && req.Mode != "shadow" && req.Mode != "canary" {
		writeError(w, http.StatusBadRequest, "mode must be off, shadow, or canary")
		return
	}
	req.ActiveVersion = strings.TrimSpace(req.ActiveVersion)
	req.CandidateVersion = strings.TrimSpace(req.CandidateVersion)
	if req.Mode != "off" && req.CandidateVersion == "" {
		writeError(w, http.StatusBadRequest, "candidate_version is required before enabling a student")
		return
	}
	if req.RolloutPercent < 0 || req.RolloutPercent > 100 {
		writeError(w, http.StatusBadRequest, "rollout_percent must be within [0,100]")
		return
	}
	// Task 18 (spec 14.1): enabling a student model (shadow/canary) is model
	// consumption — before reward calibration flips the global execution
	// switch and the workspace grant is active, no rollout may run.
	if req.Mode != "off" {
		if h.TrainingGovernance == nil {
			writeError(w, http.StatusServiceUnavailable, "training governance is not configured")
			return
		}
		if err := h.requireTrainingExecutionOpen(r.Context(), wsUUID); err != nil {
			writeTrainingGovernanceError(w, err)
			return
		}
	}
	config, err := json.Marshal(defaultObject(req.Config))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config")
		return
	}
	updatedBy := pgtype.UUID{}
	if userID := requestUserID(r); userID != "" {
		updatedBy = parseUUID(userID)
	}
	_, err = h.DB.Exec(r.Context(), `
		INSERT INTO evolution_model_runtime_config (workspace_id, model_kind, mode, active_version, candidate_version, rollout_percent, config, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)
		ON CONFLICT (workspace_id, model_kind) DO UPDATE
		SET mode = EXCLUDED.mode,
		    active_version = EXCLUDED.active_version,
		    candidate_version = EXCLUDED.candidate_version,
		    rollout_percent = EXCLUDED.rollout_percent,
		    config = EXCLUDED.config,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = now()`, wsUUID, modelKind, req.Mode, req.ActiveVersion, req.CandidateVersion, req.RolloutPercent, config, nullableUUID(updatedBy))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update model runtime config")
		return
	}
	item, err := h.ensureEvolutionModelRuntimeConfig(r.Context(), wsUUID, modelKind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load model runtime config")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// requireTrainingExecutionOpen checks the global execution switch and the
// workspace tenant grant for model rollout enablement.
func (h *Handler) requireTrainingExecutionOpen(ctx context.Context, wsUUID pgtype.UUID) error {
	policy, err := h.TrainingGovernance.TrainingPolicy(ctx)
	if err != nil {
		return err
	}
	if !policy.ExecutionEnabled {
		return service.ErrTrainingExecutionDisabled
	}
	grant, err := h.TrainingGovernance.CurrentGrant(ctx, wsUUID.String())
	if err != nil {
		return err
	}
	switch grant.TenantStatus {
	case "active":
		return nil
	case "revoked":
		return service.ErrTrainingGrantRevoked
	default:
		return service.ErrTrainingGrantPendingOwnerAck
	}
}

func (h *Handler) ListEvolutionModelEvalRuns(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionTrainingWorkspace(w, r)
	if !ok {
		return
	}
	modelKind := strings.TrimSpace(r.URL.Query().Get("model_kind"))
	limit := queryIntInRange(r, "limit", 20, 1, 100)
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, workspace_id, model_kind, model_version, mode, status, dataset_filter, metrics, example_count, created_at::text
		FROM evolution_model_eval_run
		WHERE workspace_id = $1 AND ($2 = '' OR model_kind = $2)
		ORDER BY created_at DESC
		LIMIT $3`, wsUUID, modelKind, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list model eval runs")
		return
	}
	defer rows.Close()
	items := []evolutionModelEvalRunResponse{}
	for rows.Next() {
		item, err := scanEvolutionModelEvalRun(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan model eval run")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list model eval runs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"eval_runs": items, "total": len(items)})
}

func (h *Handler) CreateEvolutionModelEvalRun(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionTrainingWorkspace(w, r)
	if !ok {
		return
	}
	var req evolutionModelEvalRunRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ModelKind = strings.TrimSpace(req.ModelKind)
	req.ModelVersion = strings.TrimSpace(req.ModelVersion)
	req.Mode = strings.TrimSpace(req.Mode)
	req.Status = strings.TrimSpace(req.Status)
	if req.Mode == "" {
		req.Mode = "offline"
	}
	if req.Status == "" {
		req.Status = "completed"
	}
	if !validEvolutionModelKind(req.ModelKind) || req.ModelVersion == "" || !validEvalMode(req.Mode) || !validEvalStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "invalid eval run fields")
		return
	}
	filter := defaultObject(req.DatasetFilter)
	metrics := defaultObject(req.Metrics)
	exampleCount := 0
	if len(metrics) == 0 {
		computed, count, err := h.computeEvolutionModelEvalMetrics(r.Context(), wsUUID, req.ModelKind, filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to compute eval metrics")
			return
		}
		metrics = computed
		exampleCount = count
	} else if rawCount, ok := metrics["example_count"].(float64); ok {
		exampleCount = int(rawCount)
	}
	filterJSON, _ := json.Marshal(filter)
	metricsJSON, _ := json.Marshal(metrics)
	var id pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
		INSERT INTO evolution_model_eval_run (workspace_id, model_kind, model_version, mode, status, dataset_filter, metrics, example_count)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8)
		RETURNING id`, wsUUID, req.ModelKind, req.ModelVersion, req.Mode, req.Status, filterJSON, metricsJSON, exampleCount).Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create model eval run")
		return
	}
	item, err := h.loadEvolutionModelEvalRun(r.Context(), wsUUID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load model eval run")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *Handler) loadEvolutionTrainingExample(ctx context.Context, workspaceID, id pgtype.UUID) (evolutionTrainingExampleResponse, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, workspace_id, model_kind, source_kind, COALESCE(source_id::text, ''),
		       COALESCE(agent_id::text, ''), COALESCE(channel_id::text, ''), COALESCE(message_id::text, ''),
		       input, teacher_label, student_prediction, split, status, created_at::text, updated_at::text
		FROM evolution_training_example
		WHERE id = $1 AND workspace_id = $2`, id, workspaceID)
	return scanEvolutionTrainingExample(row)
}

func (h *Handler) loadEvolutionModelEvalRun(ctx context.Context, workspaceID, id pgtype.UUID) (evolutionModelEvalRunResponse, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, workspace_id, model_kind, model_version, mode, status, dataset_filter, metrics, example_count, created_at::text
		FROM evolution_model_eval_run
		WHERE id = $1 AND workspace_id = $2`, id, workspaceID)
	return scanEvolutionModelEvalRun(row)
}

func (h *Handler) ensureEvolutionModelRuntimeConfig(ctx context.Context, workspaceID pgtype.UUID, modelKind string) (evolutionModelRuntimeConfigResponse, error) {
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO evolution_model_runtime_config (workspace_id, model_kind, mode)
		VALUES ($1, $2, 'off')
		ON CONFLICT (workspace_id, model_kind) DO NOTHING`, workspaceID, modelKind); err != nil {
		return evolutionModelRuntimeConfigResponse{}, err
	}
	row := h.DB.QueryRow(ctx, `
		SELECT workspace_id, model_kind, mode, active_version, candidate_version, rollout_percent,
		       config, COALESCE(updated_by::text, ''), created_at::text, updated_at::text
		FROM evolution_model_runtime_config
		WHERE workspace_id = $1 AND model_kind = $2`, workspaceID, modelKind)
	return scanEvolutionModelRuntimeConfig(row)
}

func (h *Handler) computeEvolutionModelEvalMetrics(ctx context.Context, workspaceID pgtype.UUID, modelKind string, filter map[string]any) (map[string]any, int, error) {
	split, _ := filter["split"].(string)
	status, _ := filter["status"].(string)
	var count, predicted, correct int
	err := h.DB.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE student_prediction ? 'decision')::int,
		       count(*) FILTER (WHERE student_prediction->>'decision' = teacher_label->>'decision')::int
		FROM evolution_training_example
		WHERE workspace_id = $1 AND model_kind = $2
		  AND ($3 = '' OR split = $3)
		  AND ($4 = '' OR status = $4)`, workspaceID, modelKind, split, status).Scan(&count, &predicted, &correct)
	if err != nil {
		return nil, 0, err
	}
	coverage := 0.0
	accuracy := 0.0
	if count > 0 {
		coverage = float64(predicted) / float64(count)
	}
	if predicted > 0 {
		accuracy = float64(correct) / float64(predicted)
	}
	return map[string]any{"example_count": count, "predicted_count": predicted, "correct_count": correct, "coverage": coverage, "accuracy": accuracy}, count, nil
}

type trainingExampleScanner interface {
	Scan(dest ...any) error
}

func scanEvolutionTrainingExample(row trainingExampleScanner) (evolutionTrainingExampleResponse, error) {
	var item evolutionTrainingExampleResponse
	var input, label, pred []byte
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ModelKind, &item.SourceKind, &item.SourceID, &item.AgentID, &item.ChannelID, &item.MessageID, &input, &label, &pred, &item.Split, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return item, err
	}
	item.Input = evolutionJSONObject(input)
	item.TeacherLabel = evolutionJSONObject(label)
	item.StudentPrediction = evolutionJSONObject(pred)
	return item, nil
}

func scanEvolutionModelRuntimeConfig(row trainingExampleScanner) (evolutionModelRuntimeConfigResponse, error) {
	var item evolutionModelRuntimeConfigResponse
	var config []byte
	if err := row.Scan(&item.WorkspaceID, &item.ModelKind, &item.Mode, &item.ActiveVersion, &item.CandidateVersion, &item.RolloutPercent, &config, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return item, err
	}
	item.Config = evolutionJSONObject(config)
	return item, nil
}

func scanEvolutionModelEvalRun(row trainingExampleScanner) (evolutionModelEvalRunResponse, error) {
	var item evolutionModelEvalRunResponse
	var filter, metrics []byte
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ModelKind, &item.ModelVersion, &item.Mode, &item.Status, &filter, &metrics, &item.ExampleCount, &item.CreatedAt); err != nil {
		return item, err
	}
	item.DatasetFilter = evolutionJSONObject(filter)
	item.Metrics = evolutionJSONObject(metrics)
	return item, nil
}

func evolutionTrainingWorkspace(w http.ResponseWriter, r *http.Request) (string, pgtype.UUID, bool) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	return workspaceID, wsUUID, ok
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dest any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func validEvolutionModelKind(kind string) bool {
	return kind == evolutionModelAttentionStudent || kind == evolutionModelContextFilter
}

func validTrainingExampleSource(source string) bool {
	return source == "attention_participant" || source == "manual" || source == "context_filter_teacher"
}

func validTrainingSplit(split string) bool {
	switch split {
	case "unassigned", "train", "validation", "test", "holdout":
		return true
	default:
		return false
	}
}

func validTrainingStatus(status string) bool {
	switch status {
	case "candidate", "gold", "rejected", "archived":
		return true
	default:
		return false
	}
}

func validEvalMode(mode string) bool {
	return mode == "offline" || mode == "shadow" || mode == "canary"
}

func validEvalStatus(status string) bool {
	return status == "completed" || status == "running" || status == "failed"
}

func optionalUUIDOrBadRequest(w http.ResponseWriter, raw, name string) (pgtype.UUID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, raw, name)
}

func marshalTrainingExamplePayloads(w http.ResponseWriter, input, label, pred map[string]any) ([]byte, []byte, []byte, bool) {
	inputJSON, err := json.Marshal(defaultObject(input))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid input")
		return nil, nil, nil, false
	}
	labelJSON, err := json.Marshal(defaultObject(label))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid teacher_label")
		return nil, nil, nil, false
	}
	predJSON, err := json.Marshal(defaultObject(pred))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid student_prediction")
		return nil, nil, nil, false
	}
	return inputJSON, labelJSON, predJSON, true
}

func marshalPatchPayloads(w http.ResponseWriter, label, pred map[string]any) ([]byte, []byte, bool) {
	labelJSON, err := json.Marshal(defaultObject(label))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid teacher_label")
		return nil, nil, false
	}
	predJSON, err := json.Marshal(defaultObject(pred))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid student_prediction")
		return nil, nil, false
	}
	return labelJSON, predJSON, true
}

func defaultObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func evolutionJSONObject(raw []byte) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func queryIntInRange(r *http.Request, key string, fallback, min, max int) int {
	value := fallback
	if raw := strings.TrimSpace(r.URL.Query().Get(key)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			value = parsed
		}
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
