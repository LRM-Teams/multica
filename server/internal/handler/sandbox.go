package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/sandboxws"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	defaultSandboxTokenTTL = 180 * 24 * time.Hour
	defaultSandboxJobTTL   = 24 * time.Hour
	defaultSandboxLeaseSec = 300
)

type SandboxNodeResponse struct {
	ID             string          `json:"id"`
	NodeKey        string          `json:"node_key"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	Capabilities   json.RawMessage `json:"capabilities"`
	MaxConcurrency int32           `json:"max_concurrency"`
	Metadata       json.RawMessage `json:"metadata"`
	LastSeenAt     *string         `json:"last_seen_at"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

type SandboxBindingResponse struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	NodeID      string          `json:"node_id"`
	NodeKey     string          `json:"node_key"`
	NodeName    string          `json:"node_name"`
	NodeStatus  string          `json:"node_status"`
	Enabled     bool            `json:"enabled"`
	Policy      json.RawMessage `json:"policy"`
	CreatedAt   string          `json:"created_at"`
}

type SandboxInstanceResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	CreatorUserID string          `json:"creator_user_id"`
	NodeID        string          `json:"node_id"`
	NodeKey       string          `json:"node_key,omitempty"`
	NodeName      string          `json:"node_name,omitempty"`
	NodeStatus    string          `json:"node_status,omitempty"`
	Status        string          `json:"status"`
	Template      string          `json:"template"`
	LocalRef      *string         `json:"local_ref"`
	EndpointInfo  json.RawMessage `json:"endpoint_info"`
	Limits        json.RawMessage `json:"limits"`
	Metadata      json.RawMessage `json:"metadata"`
	Error         *string         `json:"error"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

type SandboxJobResponse struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	InitiatorUserID string          `json:"initiator_user_id"`
	NodeID          string          `json:"node_id"`
	InstanceID      string          `json:"instance_id"`
	Type            string          `json:"type"`
	Status          string          `json:"status"`
	Payload         json.RawMessage `json:"payload"`
	Result          json.RawMessage `json:"result"`
	Error           *string         `json:"error"`
	TaskToken       string          `json:"task_token,omitempty"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

func sandboxNodeToResponse(n db.SandboxNode) SandboxNodeResponse {
	return SandboxNodeResponse{
		ID:             uuidToString(n.ID),
		NodeKey:        n.NodeKey,
		Name:           n.Name,
		Status:         n.Status,
		Capabilities:   rawOrEmptyArray(n.Capabilities),
		MaxConcurrency: n.MaxConcurrency,
		Metadata:       rawOrEmptyObject(n.Metadata),
		LastSeenAt:     timestampPtr(n.LastSeenAt),
		CreatedAt:      timestampToString(n.CreatedAt),
		UpdatedAt:      timestampToString(n.UpdatedAt),
	}
}

func sandboxInstanceToResponse(i db.SandboxInstance) SandboxInstanceResponse {
	return SandboxInstanceResponse{
		ID:            uuidToString(i.ID),
		WorkspaceID:   uuidToString(i.WorkspaceID),
		CreatorUserID: uuidToString(i.CreatorUserID),
		NodeID:        uuidToString(i.NodeID),
		Status:        i.Status,
		Template:      i.Template,
		LocalRef:      textToPtr(i.LocalRef),
		EndpointInfo:  rawOrEmptyObject(i.EndpointInfo),
		Limits:        rawOrEmptyObject(i.Limits),
		Metadata:      rawOrEmptyObject(i.Metadata),
		Error:         textToPtr(i.Error),
		CreatedAt:     timestampToString(i.CreatedAt),
		UpdatedAt:     timestampToString(i.UpdatedAt),
	}
}

func sandboxJobToResponse(j db.SandboxJob, token string) SandboxJobResponse {
	return SandboxJobResponse{
		ID:              uuidToString(j.ID),
		WorkspaceID:     uuidToString(j.WorkspaceID),
		InitiatorUserID: uuidToString(j.InitiatorUserID),
		NodeID:          uuidToString(j.NodeID),
		InstanceID:      uuidToString(j.InstanceID),
		Type:            j.Type,
		Status:          j.Status,
		Payload:         rawOrEmptyObject(j.Payload),
		Result:          rawOrEmptyObject(j.Result),
		Error:           textToPtr(j.Error),
		TaskToken:       token,
		CreatedAt:       timestampToString(j.CreatedAt),
		UpdatedAt:       timestampToString(j.UpdatedAt),
	}
}

func textValue(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func rawOrEmptyObject(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}

func rawOrEmptyArray(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`[]`)
	}
	return json.RawMessage(raw)
}

func timestampPtr(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	s := timestampToString(ts)
	return &s
}

func jsonBytesOrDefault(raw json.RawMessage, fallback string) []byte {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return []byte(fallback)
	}
	return raw
}

type CreateSandboxNodeRequest struct {
	NodeKey        string          `json:"node_key"`
	Name           string          `json:"name"`
	Capabilities   json.RawMessage `json:"capabilities"`
	MaxConcurrency int32           `json:"max_concurrency"`
	Metadata       json.RawMessage `json:"metadata"`
}

func (h *Handler) CreateSandboxNode(w http.ResponseWriter, r *http.Request) {
	var req CreateSandboxNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.NodeKey = strings.TrimSpace(req.NodeKey)
	req.Name = strings.TrimSpace(req.Name)
	if req.NodeKey == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "node_key and name are required")
		return
	}
	if req.MaxConcurrency <= 0 {
		req.MaxConcurrency = 1
	}
	node, err := h.Queries.CreateSandboxNode(r.Context(), db.CreateSandboxNodeParams{
		NodeKey:        req.NodeKey,
		Name:           req.Name,
		Capabilities:   jsonBytesOrDefault(req.Capabilities, "[]"),
		MaxConcurrency: req.MaxConcurrency,
		Metadata:       jsonBytesOrDefault(req.Metadata, "{}"),
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "sandbox node already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create sandbox node")
		return
	}
	writeJSON(w, http.StatusCreated, sandboxNodeToResponse(node))
}

func (h *Handler) ListSandboxNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.Queries.ListSandboxNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sandbox nodes")
		return
	}
	resp := make([]SandboxNodeResponse, 0, len(nodes))
	for _, n := range nodes {
		resp = append(resp, sandboxNodeToResponse(n))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CreateSandboxNodeToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "node_id")
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	raw, err := auth.GenerateSandboxNodeToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate sandbox node token")
		return
	}
	prefix := raw
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	expires := time.Now().Add(defaultSandboxTokenTTL)
	tok, err := h.Queries.CreateSandboxNodeToken(r.Context(), db.CreateSandboxNodeTokenParams{
		NodeID:      nodeID,
		Name:        strings.TrimSpace(req.Name),
		TokenHash:   auth.HashToken(raw),
		TokenPrefix: prefix,
		ExpiresAt:   pgtype.Timestamptz{Time: expires, Valid: true},
		CreatedBy:   parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create sandbox node token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           uuidToString(tok.ID),
		"node_id":      uuidToString(tok.NodeID),
		"token":        raw,
		"token_prefix": tok.TokenPrefix,
		"expires_at":   timestampToString(tok.ExpiresAt),
	})
}

func (h *Handler) BindSandboxNodeToWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req struct {
		NodeID string          `json:"node_id"`
		Policy json.RawMessage `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, req.NodeID, "node_id")
	if !ok {
		return
	}
	binding, err := h.Queries.UpsertSandboxWorkspaceBinding(r.Context(), db.UpsertSandboxWorkspaceBindingParams{
		WorkspaceID: wsUUID,
		NodeID:      nodeID,
		Policy:      jsonBytesOrDefault(req.Policy, "{}"),
		CreatedBy:   parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to bind sandbox node")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           uuidToString(binding.ID),
		"workspace_id": uuidToString(binding.WorkspaceID),
		"node_id":      uuidToString(binding.NodeID),
		"enabled":      binding.Enabled,
		"policy":       rawOrEmptyObject(binding.Policy),
		"created_at":   timestampToString(binding.CreatedAt),
	})
}

func (h *Handler) ListWorkspaceSandboxBindings(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListSandboxWorkspaceBindings(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sandbox bindings")
		return
	}
	resp := make([]SandboxBindingResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, SandboxBindingResponse{
			ID:          uuidToString(row.ID),
			WorkspaceID: uuidToString(row.WorkspaceID),
			NodeID:      uuidToString(row.NodeID),
			NodeKey:     row.NodeKey,
			NodeName:    row.Name,
			NodeStatus:  row.Status,
			Enabled:     row.Enabled,
			Policy:      rawOrEmptyObject(row.Policy),
			CreatedAt:   timestampToString(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

type CreateSandboxRequest struct {
	Template string          `json:"template"`
	Limits   json.RawMessage `json:"limits"`
	Metadata json.RawMessage `json:"metadata"`
	Runtime  json.RawMessage `json:"runtime"`
}

func (h *Handler) CreateSandboxInstance(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = r.Header.Get("X-Workspace-ID")
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req CreateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Template = strings.TrimSpace(req.Template)
	if req.Template == "" {
		req.Template = "default"
	}
	node, err := h.Queries.PickAvailableSandboxNodeForWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "no online sandbox node is bound to this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to select sandbox node")
		return
	}
	userUUID := parseUUID(userID)
	inst, err := h.Queries.CreateSandboxInstance(r.Context(), db.CreateSandboxInstanceParams{
		WorkspaceID:   wsUUID,
		CreatorUserID: userUUID,
		NodeID:        node.ID,
		Status:        "pending",
		Template:      req.Template,
		Limits:        jsonBytesOrDefault(req.Limits, "{}"),
		Metadata:      jsonBytesOrDefault(req.Metadata, "{}"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create sandbox")
		return
	}
	runtimeEnv, err := h.sandboxRuntimeEnv(r, wsUUID, userUUID, uuidToString(inst.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare sandbox runtime")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"template":    req.Template,
		"limits":      json.RawMessage(jsonBytesOrDefault(req.Limits, "{}")),
		"metadata":    json.RawMessage(jsonBytesOrDefault(req.Metadata, "{}")),
		"runtime":     json.RawMessage(jsonBytesOrDefault(req.Runtime, "{}")),
		"runtime_env": runtimeEnv,
		"instance_id": uuidToString(inst.ID),
	})
	job, err := h.Queries.CreateSandboxJob(r.Context(), db.CreateSandboxJobParams{
		WorkspaceID:     wsUUID,
		InitiatorUserID: userUUID,
		NodeID:          node.ID,
		InstanceID:      inst.ID,
		Type:            "create",
		Payload:         payload,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue sandbox creation")
		return
	}
	resp := sandboxInstanceToResponse(inst)
	h.publish(protocol.EventSandboxInstanceCreated, workspaceID, "member", userID, map[string]any{"instance": resp})
	if h.SandboxHub != nil {
		h.SandboxHub.NotifyJobAvailable(uuidToString(node.ID), uuidToString(job.ID))
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) sandboxRuntimeEnv(r *http.Request, workspaceID, userID pgtype.UUID, instanceID string) (map[string]string, error) {
	profile := "sandbox-" + instanceID
	rawToken, err := auth.GeneratePATToken()
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(defaultSandboxTokenTTL)
	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	_, err = h.Queries.CreatePersonalAccessToken(r.Context(), db.CreatePersonalAccessTokenParams{
		UserID:      userID,
		Name:        "sandbox runtime " + instanceID,
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: prefix,
		ExpiresAt:   pgtype.Timestamptz{Time: expires, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	serverURL := firstNonEmptyString(os.Getenv("MULTICA_PUBLIC_URL"), os.Getenv("MULTICA_APP_URL"), os.Getenv("MULTICA_SERVER_URL"))
	if serverURL == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		serverURL = scheme + "://" + r.Host
	}
	return map[string]string{
		"MULTICA_SERVER_URL":     serverURL,
		"MULTICA_APP_URL":        firstNonEmptyString(os.Getenv("MULTICA_APP_URL"), serverURL),
		"MULTICA_WORKSPACE_ID":   uuidToString(workspaceID),
		"MULTICA_TOKEN":          rawToken,
		"MULTICA_DAEMON_ENABLED": "1",
		"MULTICA_PROFILE":        profile,
	}, nil
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (h *Handler) ListSandboxInstances(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListSandboxInstancesByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sandboxes")
		return
	}
	resp := make([]SandboxInstanceResponse, 0, len(rows))
	for _, row := range rows {
		item := SandboxInstanceResponse{
			ID:            uuidToString(row.ID),
			WorkspaceID:   uuidToString(row.WorkspaceID),
			CreatorUserID: uuidToString(row.CreatorUserID),
			NodeID:        uuidToString(row.NodeID),
			NodeKey:       row.NodeKey,
			NodeName:      row.NodeName,
			NodeStatus:    row.NodeStatus,
			Status:        row.Status,
			Template:      row.Template,
			LocalRef:      textToPtr(row.LocalRef),
			EndpointInfo:  rawOrEmptyObject(row.EndpointInfo),
			Limits:        rawOrEmptyObject(row.Limits),
			Metadata:      rawOrEmptyObject(row.Metadata),
			Error:         textToPtr(row.Error),
			CreatedAt:     timestampToString(row.CreatedAt),
			UpdatedAt:     timestampToString(row.UpdatedAt),
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) enqueueSandboxInstanceJob(w http.ResponseWriter, r *http.Request, jobType string) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	instanceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "instanceId"), "instance_id")
	if !ok {
		return
	}
	inst, err := h.Queries.GetSandboxInstanceForWorkspace(r.Context(), db.GetSandboxInstanceForWorkspaceParams{ID: instanceID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	status := "stopping"
	if jobType == "resume" {
		status = "resuming"
	}
	_, _ = h.Queries.UpdateSandboxInstanceStatus(r.Context(), db.UpdateSandboxInstanceStatusParams{ID: instanceID, Status: status, Error: pgtype.Text{}})
	payload, _ := json.Marshal(map[string]any{
		"instance_id": uuidToString(instanceID),
		"local_ref":   textValue(inst.LocalRef),
	})
	job, err := h.Queries.CreateSandboxJob(r.Context(), db.CreateSandboxJobParams{
		WorkspaceID:     wsUUID,
		InitiatorUserID: parseUUID(userID),
		NodeID:          inst.NodeID,
		InstanceID:      instanceID,
		Type:            jobType,
		Payload:         payload,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue sandbox job")
		return
	}
	if h.SandboxHub != nil {
		h.SandboxHub.NotifyJobAvailable(uuidToString(inst.NodeID), uuidToString(job.ID))
	}
	writeJSON(w, http.StatusAccepted, sandboxJobToResponse(job, ""))
}

func (h *Handler) StopSandboxInstance(w http.ResponseWriter, r *http.Request) {
	h.enqueueSandboxInstanceJob(w, r, "stop")
}

func (h *Handler) ResumeSandboxInstance(w http.ResponseWriter, r *http.Request) {
	h.enqueueSandboxInstanceJob(w, r, "resume")
}

func (h *Handler) DeleteSandboxInstance(w http.ResponseWriter, r *http.Request) {
	h.enqueueSandboxInstanceJob(w, r, "delete")
}

func (h *Handler) SandboxNodeRegister(w http.ResponseWriter, r *http.Request) {
	nodeID := middleware.SandboxNodeIDFromContext(r.Context())
	if nodeID == "" {
		writeError(w, http.StatusUnauthorized, "missing sandbox node identity")
		return
	}
	var req struct {
		NodeKey        string          `json:"node_key"`
		Name           string          `json:"name"`
		Capabilities   json.RawMessage `json:"capabilities"`
		MaxConcurrency int32           `json:"max_concurrency"`
		Metadata       json.RawMessage `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.NodeKey = strings.TrimSpace(req.NodeKey)
	req.Name = strings.TrimSpace(req.Name)
	if req.NodeKey == "" {
		req.NodeKey = middleware.SandboxNodeKeyFromContext(r.Context())
	}
	if req.Name == "" {
		req.Name = req.NodeKey
	}
	if req.MaxConcurrency <= 0 {
		req.MaxConcurrency = 1
	}
	node, err := h.Queries.UpsertSandboxNodeRegistration(r.Context(), db.UpsertSandboxNodeRegistrationParams{
		NodeKey:        req.NodeKey,
		Name:           req.Name,
		Capabilities:   jsonBytesOrDefault(req.Capabilities, "[]"),
		MaxConcurrency: req.MaxConcurrency,
		Metadata:       jsonBytesOrDefault(req.Metadata, "{}"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register sandbox node")
		return
	}
	if uuidToString(node.ID) != nodeID {
		writeError(w, http.StatusForbidden, "sandbox node token does not match node_key")
		return
	}
	writeJSON(w, http.StatusOK, sandboxNodeToResponse(node))
}

func (h *Handler) SandboxNodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	nodeUUID, ok := sandboxNodeUUIDFromContext(w, r)
	if !ok {
		return
	}
	var req struct {
		Metadata json.RawMessage `json:"metadata"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	node, err := h.Queries.TouchSandboxNodeHeartbeat(r.Context(), db.TouchSandboxNodeHeartbeatParams{ID: nodeUUID, Metadata: jsonBytesOrDefault(req.Metadata, "{}")})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record sandbox node heartbeat")
		return
	}
	writeJSON(w, http.StatusOK, sandboxNodeToResponse(node))
}

func (h *Handler) SandboxNodeWebSocket(w http.ResponseWriter, r *http.Request) {
	if h.SandboxHub == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox websocket unavailable")
		return
	}
	nodeID := middleware.SandboxNodeIDFromContext(r.Context())
	if nodeID == "" {
		writeError(w, http.StatusUnauthorized, "missing sandbox node identity")
		return
	}
	h.SandboxHub.HandleWebSocket(w, r, sandboxws.ClientIdentity{NodeID: nodeID, NodeKey: middleware.SandboxNodeKeyFromContext(r.Context())})
}

func (h *Handler) ClaimSandboxJobs(w http.ResponseWriter, r *http.Request) {
	nodeUUID, ok := sandboxNodeUUIDFromContext(w, r)
	if !ok {
		return
	}
	var req struct {
		Capacity int32 `json:"capacity"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Capacity <= 0 || req.Capacity > 16 {
		req.Capacity = 1
	}
	jobs, err := h.Queries.ClaimSandboxJobsForNode(r.Context(), db.ClaimSandboxJobsForNodeParams{
		NodeID:       nodeUUID,
		LimitCount:   req.Capacity,
		LeaseSeconds: defaultSandboxLeaseSec,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to claim sandbox jobs")
		return
	}
	resp := make([]SandboxJobResponse, 0, len(jobs))
	for _, job := range jobs {
		rawToken, err := auth.GenerateSandboxJobToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate sandbox job token")
			return
		}
		expires := time.Now().Add(defaultSandboxJobTTL)
		if err := h.Queries.SetSandboxJobToken(r.Context(), db.SetSandboxJobTokenParams{ID: job.ID, JobTokenHash: pgtype.Text{String: auth.HashToken(rawToken), Valid: true}, JobTokenExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist sandbox job token")
			return
		}
		if job.Type == "create" {
			_, _ = h.Queries.UpdateSandboxInstanceStatus(r.Context(), db.UpdateSandboxInstanceStatusParams{ID: job.InstanceID, Status: "creating", Error: pgtype.Text{}})
		}
		resp = append(resp, sandboxJobToResponse(job, rawToken))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": resp})
}

func (h *Handler) StartSandboxJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireSandboxJobTokenJob(w, r)
	if !ok {
		return
	}
	updated, err := h.Queries.SetSandboxJobRunning(r.Context(), job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark sandbox job running")
		return
	}
	writeJSON(w, http.StatusOK, sandboxJobToResponse(updated, ""))
}

func (h *Handler) CompleteSandboxJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireSandboxJobTokenJob(w, r)
	if !ok {
		return
	}
	var req struct {
		Result       json.RawMessage `json:"result"`
		LocalRef     string          `json:"local_ref"`
		EndpointInfo json.RawMessage `json:"endpoint_info"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated, err := h.Queries.CompleteSandboxJob(r.Context(), db.CompleteSandboxJobParams{ID: job.ID, Result: jsonBytesOrDefault(req.Result, "{}")})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete sandbox job")
		return
	}
	var inst db.SandboxInstance
	switch job.Type {
	case "create":
		inst, err = h.Queries.CompleteSandboxInstanceCreate(r.Context(), db.CompleteSandboxInstanceCreateParams{ID: job.InstanceID, LocalRef: strToText(req.LocalRef), EndpointInfo: jsonBytesOrDefault(req.EndpointInfo, "{}")})
	case "stop":
		inst, err = h.Queries.MarkSandboxInstanceStopped(r.Context(), job.InstanceID)
	case "resume":
		inst, err = h.Queries.MarkSandboxInstanceRunning(r.Context(), job.InstanceID)
	case "delete":
		err = h.Queries.DeleteSandboxInstance(r.Context(), job.InstanceID)
	default:
		inst, err = h.Queries.UpdateSandboxInstanceStatus(r.Context(), db.UpdateSandboxInstanceStatusParams{ID: job.InstanceID, Status: "running", Error: pgtype.Text{}})
	}
	if err == nil {
		payload := map[string]any{"instance_id": uuidToString(job.InstanceID)}
		if job.Type == "delete" {
			payload["deleted"] = true
		} else {
			payload["instance"] = sandboxInstanceToResponse(inst)
		}
		h.publish(protocol.EventSandboxInstanceUpdated, uuidToString(job.WorkspaceID), "sandbox_node", uuidToString(job.NodeID), payload)
	}
	writeJSON(w, http.StatusOK, sandboxJobToResponse(updated, ""))
}

func (h *Handler) FailSandboxJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.requireSandboxJobTokenJob(w, r)
	if !ok {
		return
	}
	var req struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	errMsg := strings.TrimSpace(req.Error)
	if errMsg == "" {
		errMsg = "sandbox job failed"
	}
	updated, err := h.Queries.FailSandboxJob(r.Context(), db.FailSandboxJobParams{ID: job.ID, Error: pgtype.Text{String: errMsg, Valid: true}})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark sandbox job failed")
		return
	}
	inst, instErr := h.Queries.MarkSandboxInstanceFailed(r.Context(), db.MarkSandboxInstanceFailedParams{ID: job.InstanceID, Error: pgtype.Text{String: errMsg, Valid: true}})
	if instErr == nil {
		h.publish(protocol.EventSandboxInstanceUpdated, uuidToString(inst.WorkspaceID), "sandbox_node", uuidToString(job.NodeID), map[string]any{"instance": sandboxInstanceToResponse(inst)})
	}
	writeJSON(w, http.StatusOK, sandboxJobToResponse(updated, ""))
}

func sandboxNodeUUIDFromContext(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	nodeID := middleware.SandboxNodeIDFromContext(r.Context())
	if nodeID == "" {
		writeError(w, http.StatusUnauthorized, "missing sandbox node identity")
		return pgtype.UUID{}, false
	}
	nodeUUID, err := util.ParseUUID(nodeID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid sandbox node identity")
		return pgtype.UUID{}, false
	}
	return nodeUUID, true
}

func (h *Handler) requireSandboxJobTokenJob(w http.ResponseWriter, r *http.Request) (db.SandboxJob, bool) {
	ctxJobID := middleware.SandboxJobIDFromContext(r.Context())
	pathJobID := chi.URLParam(r, "jobId")
	if ctxJobID == "" {
		writeError(w, http.StatusUnauthorized, "missing sandbox job identity")
		return db.SandboxJob{}, false
	}
	if pathJobID != "" && pathJobID != ctxJobID {
		writeError(w, http.StatusNotFound, "sandbox job not found")
		return db.SandboxJob{}, false
	}
	authHeader := r.Header.Get("Authorization")
	raw := strings.TrimPrefix(authHeader, "Bearer ")
	job, err := h.Queries.GetSandboxJobByTokenHash(r.Context(), auth.HashToken(raw))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid sandbox job token")
		return db.SandboxJob{}, false
	}
	if uuidToString(job.ID) != ctxJobID {
		writeError(w, http.StatusUnauthorized, "invalid sandbox job token")
		return db.SandboxJob{}, false
	}
	return job, true
}

func (h *Handler) sandboxJobPayloadForInstance(instanceID pgtype.UUID) []byte {
	payload, _ := json.Marshal(map[string]string{"instance_id": uuidToString(instanceID)})
	return payload
}

func validateSandboxResult(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("invalid result json")
	}
	return nil
}
