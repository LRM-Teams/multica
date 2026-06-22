package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const sharedSkillOriginType = "runtime_shared"

type RuntimeSharedSkillSyncRequest struct {
	Skills      []RuntimeSharedSkillBundle `json:"skills"`
	PresentKeys []string                   `json:"present_keys"`
}

type AgentSharedSkillSyncRequest struct {
	Agents []AgentSharedSkillSyncSet `json:"agents"`
}

type AgentSharedSkillSyncSet struct {
	AgentID     string                     `json:"agent_id"`
	Skills      []RuntimeSharedSkillBundle `json:"skills"`
	PresentKeys []string                   `json:"present_keys"`
}

type RuntimeSharedSkillBundle struct {
	Key         string                   `json:"key"`
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	Content     string                   `json:"content"`
	SourcePath  string                   `json:"source_path"`
	Provider    string                   `json:"provider"`
	ContentHash string                   `json:"content_hash,omitempty"`
	Files       []CreateSkillFileRequest `json:"files,omitempty"`
}

type RuntimeSharedSkillSyncResponse struct {
	Status    string                            `json:"status"`
	Created   int                               `json:"created"`
	Updated   int                               `json:"updated"`
	Unchanged int                               `json:"unchanged"`
	Deleted   int                               `json:"deleted"`
	Conflicts []RuntimeSharedSkillSyncConflict  `json:"conflicts,omitempty"`
	Errors    []RuntimeSharedSkillSyncItemError `json:"errors,omitempty"`
}

type RuntimeSharedSkillSyncConflict struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Skill  string `json:"skill_id,omitempty"`
	Reason string `json:"reason"`
}

type RuntimeSharedSkillSyncItemError struct {
	Key   string `json:"key"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error"`
}

func (h *Handler) SyncAgentSharedSkills(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}

	var req AgentSharedSkillSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp := RuntimeSharedSkillSyncResponse{Status: "ok"}
	for _, set := range req.Agents {
		agentID, err := util.ParseUUID(strings.TrimSpace(set.AgentID))
		if err != nil {
			resp.Errors = append(resp.Errors, RuntimeSharedSkillSyncItemError{Key: set.AgentID, Error: "invalid agent_id"})
			continue
		}
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: rt.WorkspaceID})
		if err != nil {
			resp.Errors = append(resp.Errors, RuntimeSharedSkillSyncItemError{Key: set.AgentID, Error: "agent not found"})
			continue
		}
		if uuidToString(agent.RuntimeID) != uuidToString(rt.ID) {
			resp.Errors = append(resp.Errors, RuntimeSharedSkillSyncItemError{Key: set.AgentID, Error: "agent is not bound to this runtime"})
			continue
		}
		for _, incoming := range set.Skills {
			result, err := h.syncAgentSharedSkill(r.Context(), rt, agent, incoming)
			if err != nil {
				if conflict := (*runtimeSharedSkillConflictError)(nil); errors.As(err, &conflict) {
					resp.Conflicts = append(resp.Conflicts, conflict.Conflict)
					continue
				}
				resp.Errors = append(resp.Errors, RuntimeSharedSkillSyncItemError{Key: incoming.Key, Name: incoming.Name, Error: err.Error()})
				continue
			}
			switch result.Status {
			case "created":
				resp.Created++
			case "updated":
				resp.Updated++
			case "unchanged":
				resp.Unchanged++
			}
		}
		presentKeys := make(map[string]struct{}, len(set.PresentKeys))
		for _, key := range set.PresentKeys {
			key = strings.TrimSpace(key)
			if key != "" {
				presentKeys[key] = struct{}{}
			}
		}
		deleted, err := h.deleteMissingAgentSharedSkills(r.Context(), agent, presentKeys)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete removed agent shared skills: "+err.Error())
			return
		}
		resp.Deleted += deleted
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) SyncRuntimeSharedSkills(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}

	var req RuntimeSharedSkillSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp := RuntimeSharedSkillSyncResponse{Status: "ok"}
	for _, incoming := range req.Skills {
		result, err := h.syncRuntimeSharedSkill(r.Context(), rt, incoming)
		if err != nil {
			if conflict := (*runtimeSharedSkillConflictError)(nil); errors.As(err, &conflict) {
				resp.Conflicts = append(resp.Conflicts, conflict.Conflict)
				continue
			}
			resp.Errors = append(resp.Errors, RuntimeSharedSkillSyncItemError{Key: incoming.Key, Name: incoming.Name, Error: err.Error()})
			continue
		}
		switch result.Status {
		case "created":
			resp.Created++
			h.publish(protocol.EventSkillCreated, uuidToString(rt.WorkspaceID), "daemon", uuidToString(rt.ID), map[string]any{"skill": result.Skill})
		case "updated":
			resp.Updated++
			h.publish(protocol.EventSkillUpdated, uuidToString(rt.WorkspaceID), "daemon", uuidToString(rt.ID), map[string]any{"skill": result.Skill})
		case "unchanged":
			resp.Unchanged++
		}
	}

	presentKeys := make(map[string]struct{}, len(req.PresentKeys))
	for _, key := range req.PresentKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		presentKeys[key] = struct{}{}
	}
	deleted, err := h.deleteMissingRuntimeSharedSkills(r.Context(), rt, presentKeys)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete removed shared skills: "+err.Error())
		return
	}
	resp.Deleted = deleted

	writeJSON(w, http.StatusOK, resp)
}

type runtimeSharedSkillSyncResult struct {
	Status string
	Skill  SkillWithFilesResponse
}

type runtimeSharedSkillConflictError struct {
	Conflict RuntimeSharedSkillSyncConflict
}

func (e *runtimeSharedSkillConflictError) Error() string { return e.Conflict.Reason }

func (h *Handler) syncRuntimeSharedSkill(ctx context.Context, rt db.AgentRuntime, incoming RuntimeSharedSkillBundle) (runtimeSharedSkillSyncResult, error) {
	key := strings.TrimSpace(incoming.Key)
	name := sanitizeNullBytes(strings.TrimSpace(incoming.Name))
	if key == "" {
		return runtimeSharedSkillSyncResult{}, fmt.Errorf("skill key is required")
	}
	if name == "" {
		return runtimeSharedSkillSyncResult{}, fmt.Errorf("skill name is required")
	}
	if strings.TrimSpace(incoming.Content) == "" {
		return runtimeSharedSkillSyncResult{}, fmt.Errorf("skill content is required")
	}

	files, err := validateSharedSkillFiles(incoming.Files)
	if err != nil {
		return runtimeSharedSkillSyncResult{}, err
	}

	hash := strings.TrimSpace(incoming.ContentHash)
	if hash == "" {
		hash = hashRuntimeSharedSkill(incoming.Content, files)
	}
	config := map[string]any{
		"origin": map[string]any{
			"type":         sharedSkillOriginType,
			"runtime_id":   uuidToString(rt.ID),
			"provider":     strings.TrimSpace(incoming.Provider),
			"source_path":  strings.TrimSpace(incoming.SourcePath),
			"sync_key":     key,
			"content_hash": hash,
			"synced_at":    time.Now().UTC().Format(time.RFC3339Nano),
		},
	}

	runtimeID := uuidToString(rt.ID)
	existing, found, err := h.lookupSkillBySharedSyncKey(ctx, rt.WorkspaceID, runtimeID, key)
	if err != nil {
		return runtimeSharedSkillSyncResult{}, err
	}
	if found {
		return h.applyRuntimeSharedSkillUpdate(ctx, rt, existing, runtimeSharedSkillOverwriteInput{
			Name:        name,
			Description: incoming.Description,
			Content:     incoming.Content,
			Config:      config,
			Files:       files,
			ContentHash: hash,
		})
	}

	if byName, nameFound, err := h.lookupSkillByName(ctx, rt.WorkspaceID, name); err != nil {
		return runtimeSharedSkillSyncResult{}, err
	} else if nameFound {
		return runtimeSharedSkillSyncResult{}, &runtimeSharedSkillConflictError{Conflict: RuntimeSharedSkillSyncConflict{
			Key: key, Name: name, Skill: uuidToString(byName.ID), Reason: "a skill with this name already exists and is not managed by this shared sync key",
		}}
	}

	creator := pgtype.UUID{}
	if rt.OwnerID.Valid {
		creator = rt.OwnerID
	}
	resp, err := h.createSkillWithFiles(ctx, skillCreateInput{
		WorkspaceID: rt.WorkspaceID,
		CreatorID:   creator,
		Name:        name,
		Description: incoming.Description,
		Content:     incoming.Content,
		Config:      config,
		Files:       files,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return runtimeSharedSkillSyncResult{}, &runtimeSharedSkillConflictError{Conflict: RuntimeSharedSkillSyncConflict{
				Key: key, Name: name, Reason: "a skill with this name already exists",
			}}
		}
		return runtimeSharedSkillSyncResult{}, err
	}
	return runtimeSharedSkillSyncResult{Status: "created", Skill: resp}, nil
}

func (h *Handler) applyRuntimeSharedSkillUpdate(
	ctx context.Context,
	rt db.AgentRuntime,
	existing db.Skill,
	input runtimeSharedSkillOverwriteInput,
) (runtimeSharedSkillSyncResult, error) {
	origin := runtimeSharedSkillOrigin(existing.Config)
	if origin == nil || origin.SyncKey == "" {
		return runtimeSharedSkillSyncResult{}, fmt.Errorf("target skill is no longer a shared runtime skill")
	}
	if origin.ContentHash == input.ContentHash && existing.Name == input.Name {
		return runtimeSharedSkillSyncResult{Status: "unchanged", Skill: SkillWithFilesResponse{SkillResponse: skillToResponse(existing)}}, nil
	}

	if input.Name != existing.Name {
		if byName, found, err := h.lookupSkillByName(ctx, rt.WorkspaceID, input.Name); err != nil {
			return runtimeSharedSkillSyncResult{}, err
		} else if found && uuidToString(byName.ID) != uuidToString(existing.ID) {
			return runtimeSharedSkillSyncResult{}, &runtimeSharedSkillConflictError{Conflict: RuntimeSharedSkillSyncConflict{
				Key: origin.SyncKey, Name: input.Name, Skill: uuidToString(byName.ID), Reason: "cannot rename shared skill: target name is already taken",
			}}
		}
	}

	resp, err := h.overwriteRuntimeSharedSkillWithFiles(ctx, runtimeSharedSkillOverwriteInput{
		WorkspaceID:   rt.WorkspaceID,
		TargetSkillID: existing.ID,
		Name:          input.Name,
		Description:   input.Description,
		Content:       input.Content,
		Config:        input.Config,
		Files:         input.Files,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return runtimeSharedSkillSyncResult{}, &runtimeSharedSkillConflictError{Conflict: RuntimeSharedSkillSyncConflict{
				Key: origin.SyncKey, Name: input.Name, Skill: uuidToString(existing.ID), Reason: "cannot rename shared skill: target name is already taken",
			}}
		}
		return runtimeSharedSkillSyncResult{}, err
	}
	return runtimeSharedSkillSyncResult{Status: "updated", Skill: resp}, nil
}

func validateSharedSkillFiles(files []CreateSkillFileRequest) ([]CreateSkillFileRequest, error) {
	valid := make([]CreateSkillFileRequest, 0, len(files))
	invalid := make([]string, 0)
	for _, f := range files {
		if !validateFilePath(f.Path) {
			invalid = append(invalid, f.Path)
			continue
		}
		valid = append(valid, f)
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return nil, fmt.Errorf("invalid file paths: %s", strings.Join(invalid, ", "))
	}
	return valid, nil
}

func (h *Handler) lookupSkillBySharedSyncKey(ctx context.Context, workspaceID pgtype.UUID, runtimeID, syncKey string) (db.Skill, bool, error) {
	skills, err := h.Queries.ListSkillsByWorkspace(ctx, workspaceID)
	if err != nil {
		return db.Skill{}, false, err
	}
	for _, skill := range skills {
		origin := runtimeSharedSkillOrigin(skill.Config)
		if origin == nil {
			continue
		}
		if origin.RuntimeID == runtimeID && origin.SyncKey == syncKey {
			return skill, true, nil
		}
	}
	return db.Skill{}, false, nil
}

func (h *Handler) deleteMissingRuntimeSharedSkills(ctx context.Context, rt db.AgentRuntime, presentKeys map[string]struct{}) (int, error) {
	skills, err := h.Queries.ListSkillsByWorkspace(ctx, rt.WorkspaceID)
	if err != nil {
		return 0, err
	}
	runtimeID := uuidToString(rt.ID)
	deleted := 0
	for _, skill := range skills {
		origin := runtimeSharedSkillOrigin(skill.Config)
		if origin == nil || origin.RuntimeID != runtimeID {
			continue
		}
		if _, ok := presentKeys[origin.SyncKey]; ok {
			continue
		}
		if err := h.Queries.DeleteSkill(ctx, db.DeleteSkillParams{
			ID:          skill.ID,
			WorkspaceID: rt.WorkspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return deleted, err
		}
		deleted++
		h.publish(protocol.EventSkillDeleted, uuidToString(rt.WorkspaceID), "daemon", runtimeID, map[string]any{
			"skill_id": uuidToString(skill.ID),
		})
	}
	return deleted, nil
}

type runtimeSharedSkillOriginInfo struct {
	RuntimeID   string
	SyncKey     string
	ContentHash string
}

func runtimeSharedSkillOrigin(raw []byte) *runtimeSharedSkillOriginInfo {
	var config struct {
		Origin struct {
			Type        string `json:"type"`
			RuntimeID   string `json:"runtime_id"`
			SyncKey     string `json:"sync_key"`
			ContentHash string `json:"content_hash"`
		} `json:"origin"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil
	}
	if config.Origin.Type != sharedSkillOriginType {
		return nil
	}
	if strings.TrimSpace(config.Origin.SyncKey) == "" {
		return nil
	}
	return &runtimeSharedSkillOriginInfo{
		RuntimeID:   strings.TrimSpace(config.Origin.RuntimeID),
		SyncKey:     strings.TrimSpace(config.Origin.SyncKey),
		ContentHash: strings.TrimSpace(config.Origin.ContentHash),
	}
}

type agentSharedSkillSyncResult struct {
	Status string
	Skill  db.AgentSharedSkill
}

func (h *Handler) syncAgentSharedSkill(ctx context.Context, rt db.AgentRuntime, agent db.Agent, incoming RuntimeSharedSkillBundle) (agentSharedSkillSyncResult, error) {
	key := strings.TrimSpace(incoming.Key)
	name := sanitizeNullBytes(strings.TrimSpace(incoming.Name))
	if key == "" {
		return agentSharedSkillSyncResult{}, fmt.Errorf("skill key is required")
	}
	if name == "" {
		return agentSharedSkillSyncResult{}, fmt.Errorf("skill name is required")
	}
	if strings.TrimSpace(incoming.Content) == "" {
		return agentSharedSkillSyncResult{}, fmt.Errorf("skill content is required")
	}
	files, err := validateSharedSkillFiles(incoming.Files)
	if err != nil {
		return agentSharedSkillSyncResult{}, err
	}
	hash := strings.TrimSpace(incoming.ContentHash)
	if hash == "" {
		hash = hashRuntimeSharedSkill(incoming.Content, files)
	}
	config, err := json.Marshal(map[string]any{
		"origin": map[string]any{
			"type":         "agent_shared_runtime",
			"runtime_id":   uuidToString(rt.ID),
			"agent_id":     uuidToString(agent.ID),
			"provider":     strings.TrimSpace(incoming.Provider),
			"source_path":  strings.TrimSpace(incoming.SourcePath),
			"sync_key":     key,
			"content_hash": hash,
			"synced_at":    time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		return agentSharedSkillSyncResult{}, err
	}

	existing, found, err := h.lookupAgentSharedSkillBySyncKey(ctx, agent.ID, key)
	if err != nil {
		return agentSharedSkillSyncResult{}, err
	}
	if found {
		if existing.ContentHash == hash && existing.Name == name {
			return agentSharedSkillSyncResult{Status: "unchanged", Skill: existing}, nil
		}
		if name != existing.Name {
			if byName, found, err := h.lookupAgentSharedSkillByName(ctx, agent.ID, name); err != nil {
				return agentSharedSkillSyncResult{}, err
			} else if found && uuidToString(byName.ID) != uuidToString(existing.ID) {
				return agentSharedSkillSyncResult{}, &runtimeSharedSkillConflictError{Conflict: RuntimeSharedSkillSyncConflict{Key: key, Name: name, Skill: uuidToString(byName.ID), Reason: "cannot rename agent shared skill: target name is already taken"}}
			}
		}
		updated, err := h.overwriteAgentSharedSkillWithFiles(ctx, existing, name, incoming.Description, incoming.Content, config, hash, files)
		if err != nil {
			return agentSharedSkillSyncResult{}, err
		}
		return agentSharedSkillSyncResult{Status: "updated", Skill: updated}, nil
	}

	if byName, found, err := h.lookupAgentSharedSkillByName(ctx, agent.ID, name); err != nil {
		return agentSharedSkillSyncResult{}, err
	} else if found {
		return agentSharedSkillSyncResult{}, &runtimeSharedSkillConflictError{Conflict: RuntimeSharedSkillSyncConflict{Key: key, Name: name, Skill: uuidToString(byName.ID), Reason: "an agent shared skill with this name already exists and is not managed by this sync key"}}
	}

	creator := pgtype.UUID{}
	if rt.OwnerID.Valid {
		creator = rt.OwnerID
	}
	created, err := h.Queries.CreateAgentSharedSkill(ctx, db.CreateAgentSharedSkillParams{WorkspaceID: rt.WorkspaceID, AgentID: agent.ID, Name: name, Description: sanitizeNullBytes(incoming.Description), Content: sanitizeNullBytes(incoming.Content), Config: config, SyncKey: key, ContentHash: hash, CreatedBy: creator})
	if err != nil {
		if isUniqueViolation(err) {
			return agentSharedSkillSyncResult{}, &runtimeSharedSkillConflictError{Conflict: RuntimeSharedSkillSyncConflict{Key: key, Name: name, Reason: "an agent shared skill with this name or sync key already exists"}}
		}
		return agentSharedSkillSyncResult{}, err
	}
	if _, err := h.overwriteAgentSharedSkillWithFiles(ctx, created, name, incoming.Description, incoming.Content, config, hash, files); err != nil {
		return agentSharedSkillSyncResult{}, err
	}
	return agentSharedSkillSyncResult{Status: "created", Skill: created}, nil
}

func (h *Handler) lookupAgentSharedSkillBySyncKey(ctx context.Context, agentID pgtype.UUID, syncKey string) (db.AgentSharedSkill, bool, error) {
	skill, err := h.Queries.GetAgentSharedSkillByAgentAndSyncKey(ctx, db.GetAgentSharedSkillByAgentAndSyncKeyParams{AgentID: agentID, SyncKey: syncKey})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.AgentSharedSkill{}, false, nil
		}
		return db.AgentSharedSkill{}, false, err
	}
	return skill, true, nil
}

func (h *Handler) lookupAgentSharedSkillByName(ctx context.Context, agentID pgtype.UUID, name string) (db.AgentSharedSkill, bool, error) {
	skill, err := h.Queries.GetAgentSharedSkillByAgentAndName(ctx, db.GetAgentSharedSkillByAgentAndNameParams{AgentID: agentID, Name: name})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.AgentSharedSkill{}, false, nil
		}
		return db.AgentSharedSkill{}, false, err
	}
	return skill, true, nil
}

func (h *Handler) overwriteAgentSharedSkillWithFiles(ctx context.Context, existing db.AgentSharedSkill, name, description, content string, config []byte, hash string, files []CreateSkillFileRequest) (db.AgentSharedSkill, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.AgentSharedSkill{}, err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	update := db.UpdateAgentSharedSkillParams{ID: existing.ID, Description: pgtype.Text{String: sanitizeNullBytes(description), Valid: true}, Content: pgtype.Text{String: sanitizeNullBytes(content), Valid: true}, Config: config, ContentHash: pgtype.Text{String: hash, Valid: true}}
	if trimmedName := sanitizeNullBytes(strings.TrimSpace(name)); trimmedName != "" && trimmedName != existing.Name {
		update.Name = pgtype.Text{String: trimmedName, Valid: true}
	}
	updated, err := qtx.UpdateAgentSharedSkill(ctx, update)
	if err != nil {
		return db.AgentSharedSkill{}, err
	}
	if err := qtx.DeleteAgentSharedSkillFilesBySkill(ctx, updated.ID); err != nil {
		return db.AgentSharedSkill{}, err
	}
	for _, f := range files {
		if _, err := qtx.UpsertAgentSharedSkillFile(ctx, db.UpsertAgentSharedSkillFileParams{AgentSharedSkillID: updated.ID, AgentID: updated.AgentID, Path: sanitizeNullBytes(f.Path), Content: sanitizeNullBytes(f.Content)}); err != nil {
			return db.AgentSharedSkill{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AgentSharedSkill{}, err
	}
	return updated, nil
}

func (h *Handler) deleteMissingAgentSharedSkills(ctx context.Context, agent db.Agent, presentKeys map[string]struct{}) (int, error) {
	skills, err := h.Queries.ListAgentSharedSkillsByAgent(ctx, agent.ID)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, skill := range skills {
		if _, ok := presentKeys[skill.SyncKey]; ok {
			continue
		}
		if err := h.Queries.DeleteAgentSharedSkill(ctx, db.DeleteAgentSharedSkillParams{ID: skill.ID, AgentID: agent.ID}); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func hashRuntimeSharedSkill(content string, files []CreateSkillFileRequest) string {
	sorted := append([]CreateSkillFileRequest(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	_, _ = h.Write([]byte(content))
	for _, f := range sorted {
		_, _ = h.Write([]byte("\x00" + f.Path + "\x00" + f.Content))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

type runtimeSharedSkillOverwriteInput struct {
	WorkspaceID   pgtype.UUID
	TargetSkillID pgtype.UUID
	Name          string
	Description   string
	Content       string
	Config        any
	Files         []CreateSkillFileRequest
	ContentHash   string
}

func (h *Handler) overwriteRuntimeSharedSkillWithFiles(ctx context.Context, input runtimeSharedSkillOverwriteInput) (SkillWithFilesResponse, error) {
	config, err := json.Marshal(input.Config)
	if err != nil {
		return SkillWithFilesResponse{}, err
	}
	if input.Config == nil {
		config = []byte("{}")
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return SkillWithFilesResponse{}, err
	}
	defer tx.Rollback(ctx)

	qtx := h.Queries.WithTx(tx)
	existing, err := qtx.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{ID: input.TargetSkillID, WorkspaceID: input.WorkspaceID})
	if err != nil {
		return SkillWithFilesResponse{}, err
	}
	if runtimeSharedSkillOrigin(existing.Config) == nil {
		return SkillWithFilesResponse{}, fmt.Errorf("target skill is no longer a shared runtime skill")
	}

	update := db.UpdateSkillParams{
		ID:          existing.ID,
		Description: pgtype.Text{String: sanitizeNullBytes(input.Description), Valid: true},
		Content:     pgtype.Text{String: sanitizeNullBytes(input.Content), Valid: true},
		Config:      config,
	}
	if trimmedName := sanitizeNullBytes(strings.TrimSpace(input.Name)); trimmedName != "" && trimmedName != existing.Name {
		update.Name = pgtype.Text{String: trimmedName, Valid: true}
	}

	skill, err := qtx.UpdateSkill(ctx, update)
	if err != nil {
		return SkillWithFilesResponse{}, err
	}
	if err := qtx.DeleteSkillFilesBySkill(ctx, skill.ID); err != nil {
		return SkillWithFilesResponse{}, err
	}
	fileResps := make([]SkillFileResponse, 0, len(input.Files))
	for _, f := range input.Files {
		sf, err := qtx.UpsertSkillFile(ctx, db.UpsertSkillFileParams{SkillID: skill.ID, Path: sanitizeNullBytes(f.Path), Content: sanitizeNullBytes(f.Content)})
		if err != nil {
			return SkillWithFilesResponse{}, err
		}
		fileResps = append(fileResps, skillFileToResponse(sf))
	}
	if err := tx.Commit(ctx); err != nil {
		return SkillWithFilesResponse{}, err
	}
	return SkillWithFilesResponse{SkillResponse: skillToResponse(skill), Files: fileResps}, nil
}
