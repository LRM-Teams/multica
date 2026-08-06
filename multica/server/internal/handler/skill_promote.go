package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	skillGrantLevelAgent     = "agent"
	skillGrantLevelChannel   = "channel"
	skillGrantLevelWorkspace = "workspace"
)

// SkillCapabilities is the server-computed promote gate for the current caller (LRM-954).
type SkillCapabilities struct {
	CanPromoteToChannel   bool `json:"can_promote_to_channel"`
	CanPromoteToWorkspace bool `json:"can_promote_to_workspace"`
}

type PromoteSkillRequest struct {
	ToLevel   string `json:"to_level"`
	ChannelID string `json:"channel_id,omitempty"`
}

type SkillPromotionResponse struct {
	ID               string  `json:"id"`
	SkillID          string  `json:"skill_id"`
	FromLevel        string  `json:"from_level"`
	ToLevel          string  `json:"to_level"`
	ChannelID        *string `json:"channel_id"`
	ActorType        string  `json:"actor_type"`
	ActorID          string  `json:"actor_id"`
	ActorDisplayName *string `json:"actor_display_name,omitempty"`
	CreatedAt        string  `json:"created_at"`
}

type SkillPromotionsResponse struct {
	Items []SkillPromotionResponse `json:"items"`
	Total int                      `json:"total"`
}

type skillPromoteBaseCaps struct {
	canAnyChannel bool
	canWorkspace  bool
}

func normalizeSkillGrantLevel(level string) string {
	switch level {
	case skillGrantLevelChannel, skillGrantLevelWorkspace:
		return level
	default:
		return skillGrantLevelAgent
	}
}

func skillGrantChannelIDPtr(level string, channelID pgtype.UUID) *string {
	if normalizeSkillGrantLevel(level) != skillGrantLevelChannel || !channelID.Valid {
		return nil
	}
	id := uuidToString(channelID)
	return &id
}

func skillActorMemberType(actorType string) string {
	if actorType == "agent" {
		return "agent"
	}
	return "user"
}

func skillGrantCanPromote(fromLevel, toLevel string) bool {
	switch fromLevel {
	case skillGrantLevelAgent:
		return toLevel == skillGrantLevelChannel || toLevel == skillGrantLevelWorkspace
	case skillGrantLevelChannel:
		return toLevel == skillGrantLevelWorkspace
	default:
		return false
	}
}

func filterSkillCapabilities(base skillPromoteBaseCaps, grantLevel string) SkillCapabilities {
	level := normalizeSkillGrantLevel(grantLevel)
	return SkillCapabilities{
		CanPromoteToChannel:   base.canAnyChannel && level == skillGrantLevelAgent,
		CanPromoteToWorkspace: base.canWorkspace && level != skillGrantLevelWorkspace,
	}
}

func (h *Handler) skillPromoteBaseCapabilities(r *http.Request, workspaceID string) skillPromoteBaseCaps {
	userID := requestUserID(r)
	if userID == "" || workspaceID == "" {
		return skillPromoteBaseCaps{}
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorID == "" {
		actorType, actorID = "member", userID
	}

	base := skillPromoteBaseCaps{}
	allowed, err := h.Queries.ActorCanPromoteSkillToAnyChannel(r.Context(), db.ActorCanPromoteSkillToAnyChannelParams{
		WorkspaceID: parseUUID(workspaceID),
		MemberType:  skillActorMemberType(actorType),
		MemberID:    parseUUID(actorID),
	})
	if err == nil && allowed {
		base.canAnyChannel = true
	}

	// L3 is workspace owner/admin (human members only).
	if actorType == "member" {
		member, err := h.getWorkspaceMember(r.Context(), actorID, workspaceID)
		if err == nil && roleAllowed(member.Role, "owner", "admin") {
			base.canWorkspace = true
		}
	}
	return base
}

func (h *Handler) PromoteSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	skill, ok := h.loadSkillForUser(w, r, id)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := uuidToString(skill.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorID == "" {
		actorType, actorID = "member", userID
	}

	var req PromoteSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	fromLevel := normalizeSkillGrantLevel(skill.GrantLevel)
	toLevel := req.ToLevel
	switch toLevel {
	case skillGrantLevelChannel, skillGrantLevelWorkspace:
	default:
		writeError(w, http.StatusBadRequest, "to_level must be channel or workspace")
		return
	}

	if !skillGrantCanPromote(fromLevel, toLevel) {
		writeError(w, http.StatusBadRequest, "cannot promote from "+fromLevel+" to "+toLevel)
		return
	}

	var targetChannel pgtype.UUID
	if toLevel == skillGrantLevelChannel {
		if req.ChannelID == "" {
			writeError(w, http.StatusBadRequest, "channel_id is required when to_level is channel")
			return
		}
		channelUUID, ok := parseUUIDOrBadRequest(w, req.ChannelID, "channel_id")
		if !ok {
			return
		}
		var channelKind string
		var archivedAt pgtype.Timestamptz
		err := h.DB.QueryRow(r.Context(), `
			SELECT kind, archived_at
			FROM channel
			WHERE id = $1 AND workspace_id = $2
		`, channelUUID, skill.WorkspaceID).Scan(&channelKind, &archivedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "channel not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load channel")
			return
		}
		if channelKind != "group" || archivedAt.Valid {
			writeError(w, http.StatusBadRequest, "channel must be an active group channel")
			return
		}
		allowed, err := h.Queries.ActorCanPromoteSkillToChannel(r.Context(), db.ActorCanPromoteSkillToChannelParams{
			WorkspaceID: skill.WorkspaceID,
			ChannelID:   channelUUID,
			MemberType:  skillActorMemberType(actorType),
			MemberID:    parseUUID(actorID),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check channel promote permission")
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "only the channel owner or group manager can promote to channel")
			return
		}
		targetChannel = channelUUID
	} else {
		if actorType != "member" {
			writeError(w, http.StatusForbidden, "only workspace owner or admin can promote to workspace")
			return
		}
		if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "skill not found", "owner", "admin"); !ok {
			return
		}
		targetChannel = pgtype.UUID{}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to promote skill")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	updated, err := qtx.UpdateSkillGrantLevel(r.Context(), db.UpdateSkillGrantLevelParams{
		ID:          skill.ID,
		GrantLevel:  toLevel,
		ChannelID:   targetChannel,
		WorkspaceID: skill.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "skill not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to promote skill")
		return
	}

	auditChannel := targetChannel
	if toLevel != skillGrantLevelChannel {
		auditChannel = skill.ChannelID
	}
	if _, err := qtx.CreateSkillPromotion(r.Context(), db.CreateSkillPromotionParams{
		SkillID:     skill.ID,
		WorkspaceID: skill.WorkspaceID,
		FromLevel:   fromLevel,
		ToLevel:     toLevel,
		ChannelID:   auditChannel,
		ActorType:   actorType,
		ActorID:     parseUUID(actorID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record skill promotion")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to promote skill")
		return
	}

	files, err := h.Queries.ListSkillFiles(r.Context(), updated.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skill files")
		return
	}
	fileResps := make([]SkillFileResponse, len(files))
	for i, f := range files {
		fileResps[i] = skillFileToResponse(f)
	}
	resp := SkillWithFilesResponse{
		SkillResponse: skillToResponse(updated),
		Files:         fileResps,
	}
	caps := filterSkillCapabilities(h.skillPromoteBaseCapabilities(r, workspaceID), updated.GrantLevel)
	resp.Capabilities = &caps
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListSkillPromotions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	skill, ok := h.loadSkillForUser(w, r, id)
	if !ok {
		return
	}

	rows, err := h.Queries.ListSkillPromotionsBySkill(r.Context(), db.ListSkillPromotionsBySkillParams{
		SkillID:     skill.ID,
		WorkspaceID: skill.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skill promotions")
		return
	}

	items := make([]SkillPromotionResponse, len(rows))
	for i, row := range rows {
		items[i] = SkillPromotionResponse{
			ID:        uuidToString(row.ID),
			SkillID:   uuidToString(row.SkillID),
			FromLevel: row.FromLevel,
			ToLevel:   row.ToLevel,
			ChannelID: uuidToPtr(row.ChannelID),
			ActorType: row.ActorType,
			ActorID:   uuidToString(row.ActorID),
			CreatedAt: timestampToString(row.CreatedAt),
		}
		if row.ActorDisplayName != "" {
			name := row.ActorDisplayName
			items[i].ActorDisplayName = &name
		}
	}
	writeJSON(w, http.StatusOK, SkillPromotionsResponse{
		Items: items,
		Total: len(items),
	})
}
