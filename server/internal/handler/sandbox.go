package handler

import (
	"context"
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
	// sandboxNodeStaleThreshold marks a node offline when last_seen_at is
	// older than this. sandboxd polls jobs every 5s by default; 30s tolerates
	// brief network hiccups without showing a dead node as online for long.
	sandboxNodeStaleThreshold = 30 * time.Second
)

type SandboxNodeResponse struct {
	ID             string          `json:"id"`
	NodeKey        string          `json:"node_key"`
	OwnerUserID    string          `json:"owner_user_id"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	Capabilities   json.RawMessage `json:"capabilities"`
	MaxConcurrency int32           `json:"max_concurrency"`
	Metadata       json.RawMessage `json:"metadata"`
	LastSeenAt     *string         `json:"last_seen_at"`
	InstanceCount  int64           `json:"instance_count"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

type SandboxBindingResponse struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	NodeID          string          `json:"node_id"`
	NodeKey         string          `json:"node_key"`
	NodeOwnerUserID string          `json:"node_owner_user_id"`
	NodeName        string          `json:"node_name"`
	NodeStatus      string          `json:"node_status"`
	NodeLastSeenAt  *string         `json:"node_last_seen_at"`
	Enabled         bool            `json:"enabled"`
	Policy          json.RawMessage `json:"policy"`
	CreatedAt       string          `json:"created_at"`
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

func effectiveSandboxNodeStatus(storedStatus string, lastSeen pgtype.Timestamptz) string {
	if storedStatus != "online" {
		return storedStatus
	}
	if !lastSeen.Valid {
		return "offline"
	}
	if time.Since(lastSeen.Time) > sandboxNodeStaleThreshold {
		return "offline"
	}
	return "online"
}

func sandboxNodeUnreachableForJobs(status string, lastSeen pgtype.Timestamptz, deletedAt pgtype.Timestamptz) bool {
	if deletedAt.Valid {
		return true
	}
	return effectiveSandboxNodeStatus(status, lastSeen) == "offline"
}

func sandboxNodeToResponse(n db.SandboxNode, instanceCount int64) SandboxNodeResponse {
	return SandboxNodeResponse{
		ID:             uuidToString(n.ID),
		NodeKey:        n.NodeKey,
		OwnerUserID:    uuidToString(n.OwnerUserID),
		Name:           n.Name,
		Status:         effectiveSandboxNodeStatus(n.Status, n.LastSeenAt),
		Capabilities:   rawOrEmptyArray(n.Capabilities),
		MaxConcurrency: n.MaxConcurrency,
		Metadata:       rawOrEmptyObject(n.Metadata),
		LastSeenAt:     timestampPtr(n.LastSeenAt),
		InstanceCount:  instanceCount,
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
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req CreateSandboxNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.NodeKey = strings.TrimSpace(req.NodeKey)
	req.Name = strings.TrimSpace(req.Name)
	if req.NodeKey == "" {
		generated, err := auth.GenerateSandboxNodeKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate sandbox node key")
			return
		}
		req.NodeKey = generated
	}
	if req.Name == "" {
		req.Name = req.NodeKey
	}
	if req.MaxConcurrency <= 0 {
		req.MaxConcurrency = 1
	}
	node, err := h.Queries.CreateSandboxNode(r.Context(), db.CreateSandboxNodeParams{
		NodeKey:        req.NodeKey,
		Name:           req.Name,
		OwnerUserID:    parseUUID(userID),
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
	writeJSON(w, http.StatusCreated, sandboxNodeToResponse(node, 0))
}

func (h *Handler) ListSandboxNodes(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	ownerUUID := parseUUID(userID)
	nodes, err := h.Queries.ListSandboxNodesByOwner(r.Context(), ownerUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sandbox nodes")
		return
	}
	instanceCounts := map[string]int64{}
	if len(nodes) > 0 {
		nodeIDs := make([]pgtype.UUID, 0, len(nodes))
		for _, node := range nodes {
			nodeIDs = append(nodeIDs, node.ID)
		}
		rows, err := h.Queries.CountSandboxInstancesGroupedByNode(r.Context(), nodeIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count sandbox instances")
			return
		}
		for _, row := range rows {
			instanceCounts[uuidToString(row.NodeID)] = row.InstanceCount
		}
	}
	resp := make([]SandboxNodeResponse, 0, len(nodes))
	for _, n := range nodes {
		resp = append(resp, sandboxNodeToResponse(n, instanceCounts[uuidToString(n.ID)]))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateSandboxNode(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	node, err := h.Queries.UpdateSandboxNodeNameForOwner(r.Context(), db.UpdateSandboxNodeNameForOwnerParams{ID: nodeID, OwnerUserID: parseUUID(userID), Name: req.Name})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update sandbox node")
		return
	}
	writeJSON(w, http.StatusOK, sandboxNodeToResponse(node, 0))
}

func (h *Handler) DeleteSandboxNode(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "node_id")
	if !ok {
		return
	}
	ownerUUID := parseUUID(userID)
	if _, err := h.Queries.GetSandboxNodeForOwner(r.Context(), db.GetSandboxNodeForOwnerParams{ID: nodeID, OwnerUserID: ownerUUID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load sandbox node")
		return
	}
	instanceCount, err := h.Queries.CountSandboxInstancesByNode(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count sandbox instances")
		return
	}
	if instanceCount > 0 {
		writeError(w, http.StatusConflict, "delete sandboxes on this node before deleting the node")
		return
	}
	if err := h.Queries.DeleteSandboxNodeForOwner(r.Context(), db.DeleteSandboxNodeForOwnerParams{ID: nodeID, OwnerUserID: ownerUUID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete sandbox node")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if _, err := h.Queries.GetSandboxNodeForOwner(r.Context(), db.GetSandboxNodeForOwnerParams{ID: nodeID, OwnerUserID: parseUUID(userID)}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get sandbox node")
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
			ID:              uuidToString(row.ID),
			WorkspaceID:     uuidToString(row.WorkspaceID),
			NodeID:          uuidToString(row.NodeID),
			NodeKey:         row.NodeKey,
			NodeOwnerUserID: uuidToString(row.OwnerUserID),
			NodeName:        row.Name,
			NodeStatus:      effectiveSandboxNodeStatus(row.Status, row.LastSeenAt),
			NodeLastSeenAt:  timestampPtr(row.LastSeenAt),
			Enabled:         row.Enabled,
			Policy:          rawOrEmptyObject(row.Policy),
			CreatedAt:       timestampToString(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

type CreateSandboxRequest struct {
	NodeID   string          `json:"node_id"`
	Template string          `json:"template"`
	Name     string          `json:"name"`
	Limits   json.RawMessage `json:"limits"`
	Metadata json.RawMessage `json:"metadata"`
	Runtime  json.RawMessage `json:"runtime"`
}

type UpdateSandboxRequest struct {
	Name    string          `json:"name"`
	Runtime json.RawMessage `json:"runtime"`
}

func buildSandboxMetadata(base json.RawMessage, name string, runtime json.RawMessage) json.RawMessage {
	meta := map[string]any{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &meta)
	}
	if strings.TrimSpace(name) != "" {
		meta["name"] = strings.TrimSpace(name)
	}
	if len(runtime) > 0 && string(runtime) != "null" {
		var rt map[string]any
		if json.Unmarshal(runtime, &rt) == nil && len(rt) > 0 {
			meta["runtime"] = rt
		}
	}
	b, _ := json.Marshal(meta)
	return b
}

func mergeRuntimeEnvMetadata(base json.RawMessage, runtimeEnv map[string]string) json.RawMessage {
	meta := map[string]any{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &meta)
	}
	if len(runtimeEnv) > 0 {
		meta["runtime_env"] = runtimeEnv
	}
	b, _ := json.Marshal(meta)
	return b
}

func runtimeEnvFromMetadata(raw []byte) map[string]string {
	meta := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &meta)
	}
	re, ok := meta["runtime_env"].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, v := range re {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out[k] = strings.TrimSpace(s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runtimeFromMetadata(raw []byte) json.RawMessage {
	meta := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &meta)
	}
	rt, ok := meta["runtime"]
	if !ok {
		return nil
	}
	b, err := json.Marshal(rt)
	if err != nil || len(b) == 0 || string(b) == "null" || string(b) == "{}" {
		return nil
	}
	return b
}

func shouldEnqueueSandboxReconfigure(localRef pgtype.Text, runtime json.RawMessage) bool {
	if !localRef.Valid || strings.TrimSpace(localRef.String) == "" {
		return false
	}
	if len(runtime) == 0 || string(runtime) == "null" {
		return false
	}
	var rt map[string]any
	if json.Unmarshal(runtime, &rt) != nil || len(rt) == 0 {
		return false
	}
	return true
}

func sandboxInstanceRowToResponse(row db.ListSandboxInstancesByWorkspaceRow) SandboxInstanceResponse {
	return SandboxInstanceResponse{
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
	var node db.SandboxNode
	var err error
	if strings.TrimSpace(req.NodeID) != "" {
		nodeID, ok := parseUUIDOrBadRequest(w, req.NodeID, "node_id")
		if !ok {
			return
		}
		node, err = h.Queries.PickSandboxNodeForWorkspace(r.Context(), db.PickSandboxNodeForWorkspaceParams{WorkspaceID: wsUUID, NodeID: nodeID})
	} else {
		node, err = h.Queries.PickAvailableSandboxNodeForWorkspace(r.Context(), wsUUID)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "no online sandbox node is bound to this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to select sandbox node")
		return
	}
	userUUID := parseUUID(userID)
	metadata := buildSandboxMetadata(req.Metadata, req.Name, req.Runtime)
	inst, err := h.Queries.CreateSandboxInstance(r.Context(), db.CreateSandboxInstanceParams{
		WorkspaceID:   wsUUID,
		CreatorUserID: userUUID,
		NodeID:        node.ID,
		Status:        "pending",
		Template:      req.Template,
		Limits:        jsonBytesOrDefault(req.Limits, "{}"),
		Metadata:      jsonBytesOrDefault(metadata, "{}"),
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
	metadata = mergeRuntimeEnvMetadata(metadata, runtimeEnv)
	if _, err := h.Queries.UpdateSandboxInstanceMetadata(r.Context(), db.UpdateSandboxInstanceMetadataParams{
		ID:       inst.ID,
		Metadata: jsonBytesOrDefault(metadata, "{}"),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist sandbox runtime env")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"template":    req.Template,
		"limits":      json.RawMessage(jsonBytesOrDefault(req.Limits, "{}")),
		"metadata":    json.RawMessage(jsonBytesOrDefault(metadata, "{}")),
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
		resp = append(resp, sandboxInstanceRowToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetSandboxInstance(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	instanceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "instanceId"), "instance_id")
	if !ok {
		return
	}
	row, err := h.Queries.GetSandboxInstanceForWorkspace(r.Context(), db.GetSandboxInstanceForWorkspaceParams{ID: instanceID, WorkspaceID: wsUUID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get sandbox")
		return
	}
	writeJSON(w, http.StatusOK, sandboxInstanceRowToResponse(row))
}

func (h *Handler) UpdateSandboxInstance(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	instanceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "instanceId"), "instance_id")
	if !ok {
		return
	}
	row, err := h.Queries.GetSandboxInstanceForWorkspace(r.Context(), db.GetSandboxInstanceForWorkspaceParams{ID: instanceID, WorkspaceID: wsUUID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get sandbox")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req UpdateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	metadata := buildSandboxMetadata(row.Metadata, req.Name, req.Runtime)
	inst, err := h.Queries.UpdateSandboxInstanceMetadata(r.Context(), db.UpdateSandboxInstanceMetadataParams{
		ID:       instanceID,
		Metadata: jsonBytesOrDefault(metadata, "{}"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update sandbox")
		return
	}
	if shouldEnqueueSandboxReconfigure(row.LocalRef, req.Runtime) {
		if err := h.enqueueSandboxReconfigureJob(r, wsUUID, parseUUID(userID), row, metadata); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enqueue sandbox reconfigure")
			return
		}
	}
	resp := sandboxInstanceToResponse(inst)
	resp.NodeKey = row.NodeKey
	resp.NodeName = row.NodeName
	resp.NodeStatus = row.NodeStatus
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) enqueueSandboxReconfigureJob(r *http.Request, wsUUID, userUUID pgtype.UUID, row db.GetSandboxInstanceForWorkspaceRow, metadata json.RawMessage) error {
	runtimeEnv := runtimeEnvFromMetadata(metadata)
	if runtimeEnv == nil {
		var err error
		runtimeEnv, err = h.sandboxRuntimeEnv(r, wsUUID, userUUID, uuidToString(row.ID))
		if err != nil {
			return err
		}
		metadata = mergeRuntimeEnvMetadata(metadata, runtimeEnv)
		if _, err := h.Queries.UpdateSandboxInstanceMetadata(r.Context(), db.UpdateSandboxInstanceMetadataParams{
			ID:       row.ID,
			Metadata: jsonBytesOrDefault(metadata, "{}"),
		}); err != nil {
			return err
		}
	}
	_, _ = h.Queries.UpdateSandboxInstanceStatus(r.Context(), db.UpdateSandboxInstanceStatusParams{
		ID:     row.ID,
		Status: "reconfiguring",
		Error:  pgtype.Text{},
	})
	payload, _ := json.Marshal(map[string]any{
		"instance_id": uuidToString(row.ID),
		"local_ref":   textValue(row.LocalRef),
		"metadata":    json.RawMessage(jsonBytesOrDefault(metadata, "{}")),
		"runtime":     json.RawMessage(jsonBytesOrDefault(runtimeFromMetadata(metadata), "{}")),
		"runtime_env": runtimeEnv,
	})
	job, err := h.Queries.CreateSandboxJob(r.Context(), db.CreateSandboxJobParams{
		WorkspaceID:     wsUUID,
		InitiatorUserID: userUUID,
		NodeID:          row.NodeID,
		InstanceID:      row.ID,
		Type:            "reconfigure",
		Payload:         payload,
	})
	if err != nil {
		return err
	}
	if h.SandboxHub != nil {
		h.SandboxHub.NotifyJobAvailable(uuidToString(row.NodeID), uuidToString(job.ID))
	}
	return nil
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get sandbox")
		return
	}
	if jobType == "delete" {
		if h.forceDeleteSandboxWhenNodeUnreachable(w, r.Context(), wsUUID, instanceID, inst.NodeID) {
			return
		}
	}
	status := "stopping"
	if jobType == "resume" {
		status = "resuming"
	}
	_, _ = h.Queries.UpdateSandboxInstanceStatus(r.Context(), db.UpdateSandboxInstanceStatusParams{ID: instanceID, Status: status, Error: pgtype.Text{}})
	payload := map[string]any{
		"instance_id": uuidToString(instanceID),
		"local_ref":   textValue(inst.LocalRef),
	}
	if jobType == "resume" {
		if rt := runtimeFromMetadata(inst.Metadata); len(rt) > 0 {
			payload["runtime"] = json.RawMessage(rt)
		}
		if env := runtimeEnvFromMetadata(inst.Metadata); len(env) > 0 {
			payload["runtime_env"] = env
		}
	}
	rawPayload, _ := json.Marshal(payload)
	job, err := h.Queries.CreateSandboxJob(r.Context(), db.CreateSandboxJobParams{
		WorkspaceID:     wsUUID,
		InitiatorUserID: parseUUID(userID),
		NodeID:          inst.NodeID,
		InstanceID:      instanceID,
		Type:            jobType,
		Payload:         rawPayload,
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

func (h *Handler) forceDeleteSandboxWhenNodeUnreachable(w http.ResponseWriter, ctx context.Context, wsUUID, instanceID, nodeID pgtype.UUID) bool {
	liveness, err := h.Queries.GetSandboxNodeLiveness(ctx, nodeID)
	unreachable := false
	switch {
	case err == nil:
		unreachable = sandboxNodeUnreachableForJobs(liveness.Status, liveness.LastSeenAt, liveness.DeletedAt)
	case errors.Is(err, pgx.ErrNoRows):
		unreachable = true
	default:
		writeError(w, http.StatusInternalServerError, "failed to load sandbox node")
		return true
	}
	if !unreachable {
		return false
	}
	if err := h.Queries.DeleteSandboxInstance(ctx, instanceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete sandbox")
		return true
	}
	h.publish(protocol.EventSandboxInstanceUpdated, uuidToString(wsUUID), "user", "", map[string]any{
		"instance_id": uuidToString(instanceID),
		"deleted":     true,
		"forced":      true,
	})
	w.WriteHeader(http.StatusNoContent)
	return true
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
		OwnerUserID    string          `json:"owner_user_id"`
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
	ownerUserID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.OwnerUserID), "owner_user_id")
	if !ok {
		return
	}
	if req.MaxConcurrency <= 0 {
		req.MaxConcurrency = 1
	}
	node, err := h.Queries.UpsertSandboxNodeRegistration(r.Context(), db.UpsertSandboxNodeRegistrationParams{
		NodeKey:        req.NodeKey,
		Name:           req.Name,
		OwnerUserID:    ownerUserID,
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
	writeJSON(w, http.StatusOK, sandboxNodeToResponse(node, 0))
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
	writeJSON(w, http.StatusOK, sandboxNodeToResponse(node, 0))
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
	if err := h.Queries.TouchSandboxNodeLiveness(r.Context(), nodeUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record sandbox node liveness")
		return
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
		if job.Type == "reconfigure" {
			_, _ = h.Queries.UpdateSandboxInstanceStatus(r.Context(), db.UpdateSandboxInstanceStatusParams{ID: job.InstanceID, Status: "reconfiguring", Error: pgtype.Text{}})
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
	case "reconfigure":
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
	var inst db.SandboxInstance
	var instErr error
	if job.Type == "reconfigure" {
		inst, instErr = h.Queries.UpdateSandboxInstanceStatus(r.Context(), db.UpdateSandboxInstanceStatusParams{
			ID:     job.InstanceID,
			Status: "running",
			Error:  pgtype.Text{String: errMsg, Valid: true},
		})
	} else {
		inst, instErr = h.Queries.MarkSandboxInstanceFailed(r.Context(), db.MarkSandboxInstanceFailedParams{ID: job.InstanceID, Error: pgtype.Text{String: errMsg, Valid: true}})
	}
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
