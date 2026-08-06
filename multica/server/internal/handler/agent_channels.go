package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	agentCoordinationPurposeMaxLen = 120
	agentCoordinationRequestMaxLen = 200
)

type CreateAgentCoordinationChannelRequest struct {
	Name            string   `json:"name"`
	Description     *string  `json:"description"`
	MemberAgentIDs  []string `json:"member_agent_ids"`
	ParentChannelID *string  `json:"parent_channel_id"`
	Purpose         *string  `json:"purpose"`
	ClientRequestID string   `json:"client_request_id"`
}

type AgentCoordinationChannelResponse struct {
	ChannelID       string   `json:"channel_id"`
	Name            string   `json:"name"`
	MemberAgentIDs  []string `json:"member_agent_ids"`
	ParentChannelID *string  `json:"parent_channel_id,omitempty"`
	Purpose         *string  `json:"purpose,omitempty"`
	ClientRequestID string   `json:"client_request_id"`
	Temporary       bool     `json:"temporary"`
}

// AgentChannelListResponse is the agent data-plane channel list (slice1).
// Only channels where the agent is a current channel_member (type=agent).
type AgentChannelListItem struct {
	ID               string  `json:"id"`
	WorkspaceID      string  `json:"workspace_id"`
	Name             string  `json:"name"`
	Description      *string `json:"description,omitempty"`
	Kind             string  `json:"kind"`
	ArchivedAt       *string `json:"archived_at,omitempty"`
	Temporary        bool    `json:"temporary,omitempty"`
	ParentChannelID  *string `json:"parent_channel_id,omitempty"`
	Purpose          *string `json:"purpose,omitempty"`
	CreatedByAgentID *string `json:"created_by_agent_id,omitempty"`
}

// CreateAgentCoordinationChannel creates an idempotent temporary group owned
// by the human who initiated the active Agent run. Agents never choose or
// impersonate that owner.
func (h *Handler) CreateAgentCoordinationChannel(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	workspaceID, ok := p.WorkspaceUUID()
	if !ok || workspaceID != source.origin.workspaceID {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	sourceAgentID, ok := p.AgentUUID()
	if !ok || sourceAgentID != source.origin.agentID {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	initiatorUserID, ok := h.agentTransportInitiatorUserID(r, source)
	if !ok {
		writeError(w, http.StatusForbidden, "an initiating workspace member is required")
		return
	}

	var req CreateAgentCoordinationChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	requestID := strings.TrimSpace(req.ClientRequestID)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if name == "general" || len([]rune(name)) > channelNameMaxLen {
		writeError(w, http.StatusBadRequest, "invalid channel name")
		return
	}
	if requestID == "" || len([]rune(requestID)) > agentCoordinationRequestMaxLen {
		writeError(w, http.StatusBadRequest, "client_request_id is required and must be at most 200 characters")
		return
	}
	description := trimTextPtr(req.Description)
	purpose := trimTextPtr(req.Purpose)
	if purpose != nil && len([]rune(*purpose)) > agentCoordinationPurposeMaxLen {
		writeError(w, http.StatusBadRequest, "purpose is too long")
		return
	}
	parentID, parentText, valid := parseOptionalCoordinationParent(req.ParentChannelID)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid parent_channel_id")
		return
	}
	memberIDs, valid := parseCoordinationAgentIDs(req.MemberAgentIDs, sourceAgentID)
	if !valid {
		writeError(w, http.StatusBadRequest, "member_agent_ids must contain valid agent UUIDs")
		return
	}

	result, created, err := h.createAgentCoordinationChannel(
		r.Context(), workspaceID, sourceAgentID, initiatorUserID,
		name, description, parentID, parentText, purpose, requestID, memberIDs,
	)
	if err != nil {
		switch {
		case errors.Is(err, errAgentCoordinationConflict):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, errAgentCoordinationForbidden):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, errAgentCoordinationInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		case isChannelNameTakenError(err):
			writeCodedError(w, http.StatusConflict, channelNameTakenCode, "channel name already exists")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create coordination channel")
		}
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

// ArchiveAgentCoordinationChannel closes a temporary group created by the
// current Agent. The initiating human provenance must still match the group's
// human owner; Agents cannot archive ordinary or unrelated groups.
func (h *Handler) ArchiveAgentCoordinationChannel(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	workspaceID, ok := p.WorkspaceUUID()
	if !ok || workspaceID != source.origin.workspaceID {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	sourceAgentID, ok := p.AgentUUID()
	if !ok || sourceAgentID != source.origin.agentID {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	initiatorUserID, ok := h.agentTransportInitiatorUserID(r, source)
	if !ok {
		writeError(w, http.StatusForbidden, "an initiating workspace member is required")
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}

	ch, err := h.archiveAgentCoordinationChannel(
		r.Context(), workspaceID, channelID, sourceAgentID, initiatorUserID,
	)
	if err != nil {
		if errors.Is(err, errAgentCoordinationForbidden) {
			writeError(w, http.StatusForbidden, "only a temporary group created by this Agent can be archived")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to archive coordination channel")
		return
	}
	h.publish(protocol.EventChannelUpdated, p.WorkspaceID, "agent", p.AgentID, ch)
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) archiveAgentCoordinationChannel(
	ctx context.Context,
	workspaceID, channelID, sourceAgentID, initiatorUserID pgtype.UUID,
) (ChannelResponse, error) {
	row := h.DB.QueryRow(ctx, `
		UPDATE channel
		SET archived_at = COALESCE(archived_at, now()),
		    archived_by = COALESCE(archived_by, $4),
		    updated_at = now()
		WHERE id = $1 AND workspace_id = $2
		  AND kind = 'group' AND temporary = true
		  AND created_by_agent_id = $3 AND created_by = $4
		RETURNING id, workspace_id, name, description, lark_chat_id, project_id,
		          created_by, created_at, updated_at, kind, system_key,
		          archived_at, archived_by, avatar_url`,
		channelID, workspaceID, sourceAgentID, initiatorUserID)
	ch, err := scanChannel(row)
	if err != nil {
		if errorsIsNoRows(err) {
			return ChannelResponse{}, errAgentCoordinationForbidden
		}
		return ChannelResponse{}, err
	}
	return ch, nil
}

var (
	errAgentCoordinationConflict  = errors.New("client_request_id already identifies a different coordination channel request")
	errAgentCoordinationForbidden = errors.New("coordination channel access denied")
	errAgentCoordinationInvalid   = errors.New("invalid coordination channel request")
)

func parseOptionalCoordinationParent(raw *string) (pgtype.UUID, *string, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.UUID{}, nil, true
	}
	value := strings.TrimSpace(*raw)
	id, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, nil, false
	}
	parsed := parseUUID(id.String())
	return parsed, &value, true
}

func parseCoordinationAgentIDs(raw []string, sourceAgentID pgtype.UUID) ([]pgtype.UUID, bool) {
	byID := map[string]pgtype.UUID{uuidToString(sourceAgentID): sourceAgentID}
	for _, value := range raw {
		id, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil {
			return nil, false
		}
		parsed := parseUUID(id.String())
		byID[id.String()] = parsed
	}
	keys := make([]string, 0, len(byID))
	for id := range byID {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	out := make([]pgtype.UUID, 0, len(keys))
	for _, id := range keys {
		out = append(out, byID[id])
	}
	return out, true
}

func (h *Handler) createAgentCoordinationChannel(
	ctx context.Context,
	workspaceID, sourceAgentID, initiatorUserID pgtype.UUID,
	name string,
	description *string,
	parentID pgtype.UUID,
	parentText, purpose *string,
	requestID string,
	memberIDs []pgtype.UUID,
) (AgentCoordinationChannelResponse, bool, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		uuidToString(sourceAgentID), requestID)
	if err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	if existing, found, err := loadAgentCoordinationChannelTx(
		ctx, tx, workspaceID, sourceAgentID, requestID,
	); err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	} else if found {
		if !sameAgentCoordinationRequest(existing, name, parentText, purpose, memberIDs) {
			return AgentCoordinationChannelResponse{}, false, errAgentCoordinationConflict
		}
		return existing, false, nil
	}

	var initiatorExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM member
		  WHERE workspace_id = $1 AND user_id = $2
		)`, workspaceID, initiatorUserID).Scan(&initiatorExists); err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	if !initiatorExists {
		return AgentCoordinationChannelResponse{}, false, errAgentCoordinationForbidden
	}
	if parentID.Valid {
		var parentOK bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM channel ch
			  JOIN channel_member cm
			    ON cm.channel_id = ch.id AND cm.workspace_id = ch.workspace_id
			  WHERE ch.id = $1 AND ch.workspace_id = $2
			    AND ch.kind = 'group' AND ch.archived_at IS NULL
			    AND cm.member_type = 'agent' AND cm.member_id = $3
			)`, parentID, workspaceID, sourceAgentID).Scan(&parentOK); err != nil {
			return AgentCoordinationChannelResponse{}, false, err
		}
		if !parentOK {
			return AgentCoordinationChannelResponse{}, false, errAgentCoordinationForbidden
		}
	}
	var validAgents int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM agent
		WHERE workspace_id = $1 AND archived_at IS NULL AND id = ANY($2::uuid[])`,
		workspaceID, memberIDs).Scan(&validAgents); err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	if validAgents != len(memberIDs) {
		return AgentCoordinationChannelResponse{}, false, errAgentCoordinationInvalid
	}

	var channelID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO channel (
		  workspace_id, name, description, created_by, kind,
		  temporary, parent_channel_id, created_by_agent_id,
		  coordination_purpose, client_request_id
		)
		VALUES ($1, $2, $3, $4, 'group', true, $5, $6, $7, $8)
		RETURNING id::text`,
		workspaceID, name, description, initiatorUserID, nullableUUID(parentID),
		sourceAgentID, purpose, requestID,
	).Scan(&channelID); err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	if err := insertChannelHumanOwnerTx(ctx, tx, channelID, workspaceID, initiatorUserID); err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	for _, agentID := range memberIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_member (
			  channel_id, workspace_id, member_type, member_id, role,
			  added_by_type, added_by_id, join_source
			)
			VALUES ($1::uuid, $2, 'agent', $3, 'member', 'agent', $4, 'manual')`,
			channelID, workspaceID, agentID, sourceAgentID); err != nil {
			return AgentCoordinationChannelResponse{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	memberStrings := coordinationAgentIDStrings(memberIDs)
	return AgentCoordinationChannelResponse{
		ChannelID: channelID, Name: name, MemberAgentIDs: memberStrings,
		ParentChannelID: parentText, Purpose: purpose, ClientRequestID: requestID,
		Temporary: true,
	}, true, nil
}

func loadAgentCoordinationChannelTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sourceAgentID pgtype.UUID,
	requestID string,
) (AgentCoordinationChannelResponse, bool, error) {
	var result AgentCoordinationChannelResponse
	var parentID pgtype.UUID
	var purpose pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT id::text, name, parent_channel_id, coordination_purpose,
		       client_request_id, temporary
		FROM channel
		WHERE workspace_id = $1 AND created_by_agent_id = $2
		  AND client_request_id = $3`,
		workspaceID, sourceAgentID, requestID,
	).Scan(&result.ChannelID, &result.Name, &parentID, &purpose,
		&result.ClientRequestID, &result.Temporary)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentCoordinationChannelResponse{}, false, nil
	}
	if err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	result.ParentChannelID = uuidToPtr(parentID)
	result.Purpose = textToPtr(purpose)
	rows, err := tx.Query(ctx, `
		SELECT member_id::text
		FROM channel_member
		WHERE channel_id = $1::uuid AND member_type = 'agent'
		ORDER BY member_id::text`, result.ChannelID)
	if err != nil {
		return AgentCoordinationChannelResponse{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return AgentCoordinationChannelResponse{}, false, err
		}
		result.MemberAgentIDs = append(result.MemberAgentIDs, id)
	}
	return result, true, rows.Err()
}

func sameAgentCoordinationRequest(
	existing AgentCoordinationChannelResponse,
	name string,
	parentID, purpose *string,
	memberIDs []pgtype.UUID,
) bool {
	if existing.Name != name || !equalOptionalText(existing.ParentChannelID, parentID) ||
		!equalOptionalText(existing.Purpose, purpose) {
		return false
	}
	return slices.Equal(existing.MemberAgentIDs, coordinationAgentIDStrings(memberIDs))
}

func equalOptionalText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func coordinationAgentIDStrings(ids []pgtype.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, uuidToString(id))
	}
	slices.Sort(out)
	return out
}

// ListAgentChannels — GET /api/agent/channels
// Auth: AgentPrincipal only. Never lists owner-only channels.
func (h *Handler) ListAgentChannels(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	agentID, ok := p.AgentUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	wsID := parseUUID(p.WorkspaceID)
	archivedOnly := queryBool(r, "archived")

	rows, err := h.DB.Query(r.Context(), `
		SELECT ch.id, ch.workspace_id, ch.name, ch.description, ch.kind, ch.archived_at,
		       ch.temporary, ch.parent_channel_id, ch.coordination_purpose,
		       ch.created_by_agent_id
		FROM channel ch
		JOIN channel_member cm
		  ON cm.channel_id = ch.id
		 AND cm.workspace_id = ch.workspace_id
		 AND cm.member_type = 'agent'
		 AND cm.member_id = $2
		WHERE ch.workspace_id = $1
		  AND ch.kind = 'group'
		  AND (($3 AND ch.archived_at IS NOT NULL) OR (NOT $3 AND ch.archived_at IS NULL))
		ORDER BY ch.updated_at DESC, ch.created_at DESC`,
		wsID, agentID, archivedOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	defer rows.Close()

	out := []AgentChannelListItem{}
	for rows.Next() {
		var id, workspaceID, parentChannelID, createdByAgentID pgtype.UUID
		var name, kind string
		var desc, purpose pgtype.Text
		var archivedAt pgtype.Timestamptz
		var temporary bool
		if err := rows.Scan(
			&id, &workspaceID, &name, &desc, &kind, &archivedAt,
			&temporary, &parentChannelID, &purpose, &createdByAgentID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channels")
			return
		}
		item := AgentChannelListItem{
			ID:               uuidToString(id),
			WorkspaceID:      uuidToString(workspaceID),
			Name:             name,
			Description:      textToPtr(desc),
			Kind:             kind,
			ArchivedAt:       timestampToPtr(archivedAt),
			Temporary:        temporary,
			ParentChannelID:  uuidToPtr(parentChannelID),
			Purpose:          textToPtr(purpose),
			CreatedByAgentID: uuidToPtr(createdByAgentID),
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

// ListAgentChannelMembers — GET /api/agent/channels/{channelId}/members
func (h *Handler) ListAgentChannelMembers(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireAgentSurfaceAccessHTTP(w, r, p, channelID) {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       CASE WHEN cm.member_type = 'user' THEN u.avatar_url ELSE a.avatar_url END,
		       cs.runtime_token_stats,
		       cm.created_at,
		       cm.role
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		LEFT JOIN channel_agent_session cas ON cm.member_type = 'agent' AND cas.channel_id = cm.channel_id AND cas.agent_id = cm.member_id
		LEFT JOIN chat_session cs ON cs.id = cas.chat_session_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		ORDER BY
		  CASE cm.role
		    WHEN 'owner' THEN 0
		    WHEN 'manager' THEN 1
		    ELSE 2
		  END,
		  cm.created_at ASC,
		  cm.member_type ASC,
		  cm.member_id ASC`, channelID, parseUUID(p.WorkspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel members")
		return
	}
	defer rows.Close()
	out := []ChannelMemberResponse{}
	for rows.Next() {
		var typ, name, displayName, role string
		var id pgtype.UUID
		var avatarURL pgtype.Text
		var runtimeStatsRaw []byte
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &displayName, &avatarURL, &runtimeStatsRaw, &createdAt, &role); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel members")
			return
		}
		if role == "" {
			role = "member"
		}
		member := ChannelMemberResponse{
			MemberType:  typ,
			MemberID:    uuidToString(id),
			Name:        name,
			DisplayName: firstNonEmpty(displayName, name),
			AvatarURL:   textToPtr(avatarURL),
			Role:        role,
			CreatedAt:   timestampToString(createdAt),
		}
		if len(runtimeStatsRaw) > 0 {
			var stats protocol.RuntimeTokenStats
			if json.Unmarshal(runtimeStatsRaw, &stats) == nil {
				member.RuntimeStats = &stats
			}
		}
		out = append(out, member)
	}
	writeJSON(w, http.StatusOK, out)
}

// MuteAgentChannel — PUT /api/agent/channels/{channelId}/mute
func (h *Handler) MuteAgentChannel(w http.ResponseWriter, r *http.Request) {
	h.setAgentChannelMuted(w, r, true)
}

// UnmuteAgentChannel — DELETE /api/agent/channels/{channelId}/mute
func (h *Handler) UnmuteAgentChannel(w http.ResponseWriter, r *http.Request) {
	h.setAgentChannelMuted(w, r, false)
}

func (h *Handler) setAgentChannelMuted(w http.ResponseWriter, r *http.Request, muted bool) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireAgentSurfaceAccessHTTP(w, r, p, channelID) {
		return
	}
	agentUUID, ok := p.AgentUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	wsUUID := parseUUID(p.WorkspaceID)

	_, err := h.DB.Exec(r.Context(), `
		WITH updated_cm AS (
		  UPDATE channel_member
		  SET muted_at = CASE WHEN $4 THEN now() ELSE NULL END
		  WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3
		  RETURNING channel_id, workspace_id, member_id, muted_at
		),
		conv AS (
		  SELECT id FROM conversation WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET muted_at = updated_cm.muted_at, updated_at = now()
		FROM updated_cm, conv
		WHERE cm.conversation_id = conv.id
		  AND cm.workspace_id = updated_cm.workspace_id
		  AND cm.member_type = 'agent'
		  AND cm.member_id = updated_cm.member_id`,
		channelID, wsUUID, agentUUID, muted)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent mute state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "muted": muted})
}
