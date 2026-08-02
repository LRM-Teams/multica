package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	subgoalStatusCaptured   = "captured"
	subgoalStatusInProgress = "in_progress"
	subgoalStatusWaiting    = "waiting"
	subgoalStatusResolved   = "resolved"
	subgoalStatusCancelled  = "cancelled"

	subgoalBatchMax    = 20
	subgoalInjectMax   = 8
	subgoalActivityMax = 5
)

type ChannelGoalSubgoalResponse struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	ChannelID          string          `json:"channel_id"`
	GoalID             string          `json:"goal_id"`
	Title              string          `json:"title"`
	Purpose            string          `json:"purpose"`
	CompletionBoundary string          `json:"completion_boundary"`
	Brief              string          `json:"brief"`
	CurrentConclusion  string          `json:"current_conclusion"`
	Status             string          `json:"status"`
	Version            int64           `json:"version"`
	ResponsibleType    string          `json:"responsible_type"`
	ResponsibleID      string          `json:"responsible_id"`
	Participants       []subgoalActor  `json:"participants"`
	DependsOn          []string        `json:"depends_on"`
	WaitingOn          json.RawMessage `json:"waiting_on"`
	ArtifactRefs       []string        `json:"artifact_refs"`
	ActivityDelta      []string        `json:"activity_delta"`
	SourceMessageID    *string         `json:"source_message_id,omitempty"`
	CreatedByType      string          `json:"created_by_type"`
	CreatedByID        string          `json:"created_by_id"`
	UpdatedByType      string          `json:"updated_by_type"`
	UpdatedByID        string          `json:"updated_by_id"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	ResolvedAt         *time.Time      `json:"resolved_at,omitempty"`
}

type subgoalActor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type subgoalWaitingOn struct {
	Kind     string `json:"kind"` // member | issue | pr | lock | external
	TargetID string `json:"target_id,omitempty"`
	Note     string `json:"note,omitempty"`
}

type createSubgoalRequest struct {
	Title              string         `json:"title"`
	Purpose            string         `json:"purpose"`
	CompletionBoundary string         `json:"completion_boundary"`
	Brief              string         `json:"brief"`
	Responsible        subgoalActor   `json:"responsible"`
	Participants       []subgoalActor `json:"participants"`
	DependsOn          []string       `json:"depends_on"`
	ArtifactRefs       []string       `json:"artifact_refs"`
	SourceMessageID    *string        `json:"source_message_id,omitempty"`
}

type batchCreateSubgoalsRequest struct {
	Items []createSubgoalRequest `json:"items"`
}

type updateSubgoalRequest struct {
	ExpectedVersion    int64           `json:"expected_version"`
	Title              *string         `json:"title,omitempty"`
	Purpose            *string         `json:"purpose,omitempty"`
	CompletionBoundary *string         `json:"completion_boundary,omitempty"`
	Brief              *string         `json:"brief,omitempty"`
	CurrentConclusion  *string         `json:"current_conclusion,omitempty"`
	Status             *string         `json:"status,omitempty"`
	Responsible        *subgoalActor   `json:"responsible,omitempty"`
	Participants       *[]subgoalActor `json:"participants,omitempty"`
	DependsOn          *[]string       `json:"depends_on,omitempty"`
	ArtifactRefs       *[]string       `json:"artifact_refs,omitempty"`
	ActivityDelta      *[]string       `json:"activity_delta,omitempty"`
	WaitingOn          json.RawMessage `json:"waiting_on,omitempty"`
	// SourceMessageID: omit = unchanged; ""/null = clear; UUID = set (same-channel).
	SourceMessageID *string `json:"source_message_id,omitempty"`
}

type clearWaitingOnRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
	// Verification forces re-check against source before clearing (LRM-1004).
	Verification struct {
		Kind          string `json:"kind"`
		TargetID      string `json:"target_id,omitempty"`
		IssueStatusOK bool   `json:"issue_status_ok,omitempty"`
		Acknowledged  bool   `json:"acknowledged,omitempty"`
		Released      bool   `json:"released,omitempty"`
		ExternalOK    bool   `json:"external_ok,omitempty"`
	} `json:"verification"`
}

type resolveSubgoalRequest struct {
	ExpectedVersion   int64  `json:"expected_version"`
	CurrentConclusion string `json:"current_conclusion"`
}

type subgoalListEnvelope struct {
	Subgoals []ChannelGoalSubgoalResponse `json:"subgoals"`
}

type subgoalEnvelope struct {
	Subgoal *ChannelGoalSubgoalResponse `json:"subgoal"`
}

type subgoalBatchEnvelope struct {
	Subgoals []ChannelGoalSubgoalResponse `json:"subgoals"`
}

const channelGoalSubgoalColumns = `
	id::text, workspace_id, channel_id, goal_id, title, purpose, completion_boundary,
	brief, current_conclusion, status, version, responsible_type, responsible_id,
	waiting_on, artifact_refs, activity_delta, source_message_id, created_by_type, created_by_id,
	updated_by_type, updated_by_id, created_at, updated_at, resolved_at`

func scanChannelGoalSubgoal(row pgx.Row) (ChannelGoalSubgoalResponse, error) {
	var sg ChannelGoalSubgoalResponse
	var workspaceID, channelID, goalID, responsibleID, createdByID, updatedByID, sourceMessageID pgtype.UUID
	var waitingOn, artifactRefs, activityDelta []byte
	var resolvedAt pgtype.Timestamptz
	err := row.Scan(
		&sg.ID, &workspaceID, &channelID, &goalID, &sg.Title, &sg.Purpose, &sg.CompletionBoundary,
		&sg.Brief, &sg.CurrentConclusion, &sg.Status, &sg.Version, &sg.ResponsibleType, &responsibleID,
		&waitingOn, &artifactRefs, &activityDelta, &sourceMessageID, &sg.CreatedByType, &createdByID,
		&sg.UpdatedByType, &updatedByID, &sg.CreatedAt, &sg.UpdatedAt, &resolvedAt,
	)
	if err != nil {
		return sg, err
	}
	sg.WorkspaceID = uuidToString(workspaceID)
	sg.ChannelID = uuidToString(channelID)
	sg.GoalID = uuidToString(goalID)
	sg.ResponsibleID = uuidToString(responsibleID)
	sg.CreatedByID = uuidToString(createdByID)
	sg.UpdatedByID = uuidToString(updatedByID)
	if sourceMessageID.Valid {
		id := uuidToString(sourceMessageID)
		sg.SourceMessageID = &id
	}
	if len(waitingOn) == 0 || string(waitingOn) == "null" {
		sg.WaitingOn = json.RawMessage("null")
	} else {
		sg.WaitingOn = json.RawMessage(waitingOn)
	}
	_ = json.Unmarshal(artifactRefs, &sg.ArtifactRefs)
	_ = json.Unmarshal(activityDelta, &sg.ActivityDelta)
	if sg.ArtifactRefs == nil {
		sg.ArtifactRefs = []string{}
	}
	if sg.ActivityDelta == nil {
		sg.ActivityDelta = []string{}
	}
	sg.Participants = []subgoalActor{}
	sg.DependsOn = []string{}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		sg.ResolvedAt = &t
	}
	return sg, nil
}

// resolveSubgoalSourceMessageID validates optional source_message_id against the
// same channel. Empty string → null UUID (clear / unset).
func (h *Handler) resolveSubgoalSourceMessageID(ctx context.Context, channelID pgtype.UUID, raw string) (pgtype.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	msgID, err := util.ParseUUID(raw)
	if err != nil || !msgID.Valid {
		return pgtype.UUID{}, errors.New("invalid source_message_id")
	}
	var ok bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND deleted_at IS NULL
		)`, msgID, channelID).Scan(&ok); err != nil {
		return pgtype.UUID{}, err
	}
	if !ok {
		return pgtype.UUID{}, errors.New("source_message_id must reference a message in this channel")
	}
	return msgID, nil
}

func normalizeSubgoalActor(actor subgoalActor) (subgoalActor, bool) {
	actor.Type = strings.ToLower(strings.TrimSpace(actor.Type))
	actor.ID = strings.TrimSpace(actor.ID)
	if (actor.Type != "agent" && actor.Type != "member") || actor.ID == "" {
		return actor, false
	}
	id, err := util.ParseUUID(actor.ID)
	if err != nil || !id.Valid {
		return actor, false
	}
	actor.ID = uuidToString(id)
	return actor, true
}

func normalizeParticipants(items []subgoalActor, responsible subgoalActor) ([]subgoalActor, bool) {
	out := make([]subgoalActor, 0, len(items))
	seen := map[string]struct{}{}
	responsibleKey := responsible.Type + ":" + responsible.ID
	for _, item := range items {
		actor, ok := normalizeSubgoalActor(item)
		if !ok {
			return nil, false
		}
		key := actor.Type + ":" + actor.ID
		if key == responsibleKey {
			// Responsible is not a participant row; ignore duplicates silently.
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, actor)
	}
	return out, true
}

// listChannelGoalSubgoalsHydrated drains the list cursor before hydrateSubgoalRelations
// so nested Query() calls cannot hold two pool connections (cursordeadlock / #1803).
func (h *Handler) listChannelGoalSubgoalsHydrated(ctx context.Context, goalID string) ([]ChannelGoalSubgoalResponse, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT `+channelGoalSubgoalColumns+`
		FROM channel_goal_subgoal
		WHERE goal_id = $1::uuid
		ORDER BY created_at ASC, id ASC`, goalID)
	if err != nil {
		return nil, err
	}
	items := make([]ChannelGoalSubgoalResponse, 0)
	for rows.Next() {
		sg, err := scanChannelGoalSubgoal(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, sg)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range items {
		_ = h.hydrateSubgoalRelations(ctx, &items[i])
	}
	return items, nil
}

func (h *Handler) hydrateSubgoalRelations(ctx context.Context, sg *ChannelGoalSubgoalResponse) error {
	rows, err := h.DB.Query(ctx, `
		SELECT participant_type, participant_id::text
		FROM channel_goal_subgoal_participant
		WHERE subgoal_id = $1::uuid
		ORDER BY created_at, id`, sg.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	sg.Participants = []subgoalActor{}
	for rows.Next() {
		var actor subgoalActor
		if err := rows.Scan(&actor.Type, &actor.ID); err != nil {
			return err
		}
		sg.Participants = append(sg.Participants, actor)
	}
	depRows, err := h.DB.Query(ctx, `
		SELECT depends_on_subgoal_id::text
		FROM channel_goal_subgoal_dep
		WHERE subgoal_id = $1::uuid
		ORDER BY created_at, id`, sg.ID)
	if err != nil {
		return err
	}
	defer depRows.Close()
	sg.DependsOn = []string{}
	for depRows.Next() {
		var id string
		if err := depRows.Scan(&id); err != nil {
			return err
		}
		sg.DependsOn = append(sg.DependsOn, id)
	}
	return nil
}

func (h *Handler) subgoalDepsSatisfied(ctx context.Context, subgoalID string) (bool, error) {
	var unmet int
	err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_goal_subgoal_dep d
		JOIN channel_goal_subgoal dep ON dep.id = d.depends_on_subgoal_id
		WHERE d.subgoal_id = $1::uuid
		  AND dep.status NOT IN ('resolved', 'cancelled')`, subgoalID).Scan(&unmet)
	return unmet == 0, err
}

func (h *Handler) replaceSubgoalParticipants(ctx context.Context, workspaceID pgtype.UUID, subgoalID string, participants []subgoalActor) error {
	if _, err := h.DB.Exec(ctx, `DELETE FROM channel_goal_subgoal_participant WHERE subgoal_id = $1::uuid`, subgoalID); err != nil {
		return err
	}
	for _, p := range participants {
		if _, err := h.DB.Exec(ctx, `
			INSERT INTO channel_goal_subgoal_participant (workspace_id, subgoal_id, participant_type, participant_id)
			VALUES ($1, $2::uuid, $3, $4::uuid)`, workspaceID, subgoalID, p.Type, parseUUID(p.ID)); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) replaceSubgoalDeps(ctx context.Context, workspaceID, goalID pgtype.UUID, subgoalID string, dependsOn []string) error {
	if _, err := h.DB.Exec(ctx, `DELETE FROM channel_goal_subgoal_dep WHERE subgoal_id = $1::uuid`, subgoalID); err != nil {
		return err
	}
	for _, depID := range dependsOn {
		depID = strings.TrimSpace(depID)
		if depID == "" || depID == subgoalID {
			continue
		}
		var sameGoal bool
		if err := h.DB.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM channel_goal_subgoal
				WHERE id = $1::uuid AND goal_id = $2
			)`, depID, goalID).Scan(&sameGoal); err != nil || !sameGoal {
			return errors.New("depends_on must reference a subgoal under the same goal")
		}
		if _, err := h.DB.Exec(ctx, `
			INSERT INTO channel_goal_subgoal_dep (workspace_id, subgoal_id, depends_on_subgoal_id)
			VALUES ($1, $2::uuid, $3::uuid)`, workspaceID, subgoalID, parseUUID(depID)); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) insertSubgoal(ctx context.Context, workspaceID, channelID, goalID pgtype.UUID, actorType string, actorID pgtype.UUID, req createSubgoalRequest) (ChannelGoalSubgoalResponse, error) {
	title := strings.TrimSpace(req.Title)
	purpose := strings.TrimSpace(req.Purpose)
	boundary := strings.TrimSpace(req.CompletionBoundary)
	brief := strings.TrimSpace(req.Brief)
	responsible, ok := normalizeSubgoalActor(req.Responsible)
	if !ok || title == "" || len(title) > 200 || purpose == "" || len(purpose) > 4000 || len(boundary) > 4000 || len(brief) > 16000 {
		return ChannelGoalSubgoalResponse{}, errors.New("invalid subgoal fields or responsible")
	}
	participants, ok := normalizeParticipants(req.Participants, responsible)
	if !ok {
		return ChannelGoalSubgoalResponse{}, errors.New("invalid participants")
	}
	// Reject "multiple responsible" disguised as participants of role responsible — participants are never responsible.
	for _, p := range req.Participants {
		if strings.EqualFold(strings.TrimSpace(p.Type), "responsible") {
			return ChannelGoalSubgoalResponse{}, errors.New("only one responsible is allowed; use responsible field")
		}
	}
	refs, valid := normalizeOptionalGoalStrings(req.ArtifactRefs, 50, 2000)
	if !valid {
		return ChannelGoalSubgoalResponse{}, errors.New("invalid artifact_refs")
	}
	refsJSON, _ := json.Marshal(refs)
	var sourceMessageID pgtype.UUID
	if req.SourceMessageID != nil {
		resolved, err := h.resolveSubgoalSourceMessageID(ctx, channelID, *req.SourceMessageID)
		if err != nil {
			return ChannelGoalSubgoalResponse{}, err
		}
		sourceMessageID = resolved
	}
	sg, err := scanChannelGoalSubgoal(h.DB.QueryRow(ctx, `
		INSERT INTO channel_goal_subgoal (
			workspace_id, channel_id, goal_id, title, purpose, completion_boundary, brief,
			responsible_type, responsible_id, artifact_refs, source_message_id,
			created_by_type, created_by_id, updated_by_type, updated_by_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$12,$13)
		RETURNING `+channelGoalSubgoalColumns,
		workspaceID, channelID, goalID, title, purpose, boundary, brief,
		responsible.Type, parseUUID(responsible.ID), refsJSON, nullableUUIDArg(sourceMessageID), actorType, actorID))
	if err != nil {
		return sg, err
	}
	if err := h.replaceSubgoalParticipants(ctx, workspaceID, sg.ID, participants); err != nil {
		return sg, err
	}
	if err := h.replaceSubgoalDeps(ctx, workspaceID, goalID, sg.ID, req.DependsOn); err != nil {
		return sg, err
	}
	_ = h.hydrateSubgoalRelations(ctx, &sg)
	return sg, nil
}

func (h *Handler) ListChannelGoalSubgoals(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(ctxWorkspaceID(r.Context()))
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok || !h.requireChannelUserMember(w, r.Context(), uuidToString(workspaceID), channelID, parseUUID(userID)) {
		return
	}
	goal, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, subgoalListEnvelope{Subgoals: []ChannelGoalSubgoalResponse{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	items, err := h.listChannelGoalSubgoalsHydrated(r.Context(), goal.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list subgoals")
		return
	}
	writeJSON(w, http.StatusOK, subgoalListEnvelope{Subgoals: items})
}

func (h *Handler) CreateChannelGoalSubgoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceIDStr := ctxWorkspaceID(r.Context())
	workspaceID := parseUUID(workspaceIDStr)
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok || !h.requireChannelWritable(w, r.Context(), workspaceIDStr, channelID) ||
		!h.requireChannelManager(w, r, workspaceIDStr, channelID, parseUUID(userID)) {
		return
	}
	goal, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel goal not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	var req createSubgoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sg, err := h.insertSubgoal(r.Context(), workspaceID, channelID, parseUUID(goal.ID), "user", parseUUID(userID), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, subgoalEnvelope{Subgoal: &sg})
}

func (h *Handler) BatchCreateChannelGoalSubgoals(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceIDStr := ctxWorkspaceID(r.Context())
	workspaceID := parseUUID(workspaceIDStr)
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok || !h.requireChannelWritable(w, r.Context(), workspaceIDStr, channelID) ||
		!h.requireChannelManager(w, r, workspaceIDStr, channelID, parseUUID(userID)) {
		return
	}
	goal, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel goal not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	var req batchCreateSubgoalsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Items) == 0 || len(req.Items) > subgoalBatchMax {
		writeError(w, http.StatusBadRequest, "items required (1-20)")
		return
	}
	// Short-window capture: sequential inserts so a burst of tasks is not lost.
	created := make([]ChannelGoalSubgoalResponse, 0, len(req.Items))
	for _, item := range req.Items {
		sg, err := h.insertSubgoal(r.Context(), workspaceID, channelID, parseUUID(goal.ID), "user", parseUUID(userID), item)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		created = append(created, sg)
	}
	writeJSON(w, http.StatusCreated, subgoalBatchEnvelope{Subgoals: created})
}

func (h *Handler) UpdateChannelGoalSubgoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceIDStr := ctxWorkspaceID(r.Context())
	workspaceID := parseUUID(workspaceIDStr)
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	subgoalID, ok2 := parseUUIDOrBadRequest(w, chi.URLParam(r, "subgoalId"), "subgoal id")
	if !ok || !ok2 || !h.requireChannelWritable(w, r.Context(), workspaceIDStr, channelID) ||
		!h.requireChannelManager(w, r, workspaceIDStr, channelID, parseUUID(userID)) {
		return
	}
	var req updateSubgoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version is required")
		return
	}
	current, err := scanChannelGoalSubgoal(h.DB.QueryRow(r.Context(), `
		SELECT `+channelGoalSubgoalColumns+`
		FROM channel_goal_subgoal
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3`, subgoalID, workspaceID, channelID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "subgoal not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load subgoal")
		return
	}
	if current.Version != req.ExpectedVersion {
		writeError(w, http.StatusConflict, "subgoal version is stale")
		return
	}
	_ = h.hydrateSubgoalRelations(r.Context(), &current)

	if req.Title != nil {
		current.Title = strings.TrimSpace(*req.Title)
	}
	if req.Purpose != nil {
		current.Purpose = strings.TrimSpace(*req.Purpose)
	}
	if req.CompletionBoundary != nil {
		current.CompletionBoundary = strings.TrimSpace(*req.CompletionBoundary)
	}
	if req.Brief != nil {
		current.Brief = strings.TrimSpace(*req.Brief)
	}
	if req.CurrentConclusion != nil {
		current.CurrentConclusion = strings.TrimSpace(*req.CurrentConclusion)
	}
	if req.Status != nil {
		switch strings.TrimSpace(*req.Status) {
		case subgoalStatusCaptured, subgoalStatusInProgress, subgoalStatusWaiting, subgoalStatusResolved, subgoalStatusCancelled:
			current.Status = strings.TrimSpace(*req.Status)
		default:
			writeError(w, http.StatusBadRequest, "invalid subgoal status")
			return
		}
	}
	if req.Responsible != nil {
		actor, ok := normalizeSubgoalActor(*req.Responsible)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid responsible")
			return
		}
		current.ResponsibleType = actor.Type
		current.ResponsibleID = actor.ID
	}
	if req.ArtifactRefs != nil {
		refs, valid := normalizeOptionalGoalStrings(*req.ArtifactRefs, 50, 2000)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid artifact_refs")
			return
		}
		current.ArtifactRefs = refs
	}
	sourceMessageID := pgtype.UUID{}
	if current.SourceMessageID != nil {
		sourceMessageID = parseUUID(*current.SourceMessageID)
	}
	if req.SourceMessageID != nil {
		resolved, err := h.resolveSubgoalSourceMessageID(r.Context(), channelID, *req.SourceMessageID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sourceMessageID = resolved
		if resolved.Valid {
			id := uuidToString(resolved)
			current.SourceMessageID = &id
		} else {
			current.SourceMessageID = nil
		}
	}
	if req.ActivityDelta != nil {
		delta, valid := normalizeOptionalGoalStrings(*req.ActivityDelta, 20, 500)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid activity_delta")
			return
		}
		if len(delta) > subgoalActivityMax {
			delta = delta[len(delta)-subgoalActivityMax:]
		}
		current.ActivityDelta = delta
	}
	waitingJSON := []byte(current.WaitingOn)
	if len(req.WaitingOn) > 0 {
		if string(req.WaitingOn) == "null" {
			waitingJSON = []byte("null")
			current.WaitingOn = json.RawMessage("null")
		} else {
			var wo subgoalWaitingOn
			if err := json.Unmarshal(req.WaitingOn, &wo); err != nil || strings.TrimSpace(wo.Kind) == "" {
				writeError(w, http.StatusBadRequest, "invalid waiting_on")
				return
			}
			wo.Kind = strings.ToLower(strings.TrimSpace(wo.Kind))
			switch wo.Kind {
			case "member", "issue", "pr", "lock", "external":
			default:
				writeError(w, http.StatusBadRequest, "invalid waiting_on.kind")
				return
			}
			waitingJSON, _ = json.Marshal(wo)
			current.WaitingOn = json.RawMessage(waitingJSON)
			if current.Status != subgoalStatusResolved && current.Status != subgoalStatusCancelled {
				current.Status = subgoalStatusWaiting
			}
		}
	}
	// Apply depends_on before the in_progress gate so a same-request
	// {depends_on, status:in_progress} cannot bypass the serial-dep check.
	if req.DependsOn != nil {
		if err := h.replaceSubgoalDeps(r.Context(), workspaceID, parseUUID(current.GoalID), current.ID, *req.DependsOn); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if current.Status == subgoalStatusInProgress {
		okDeps, err := h.subgoalDepsSatisfied(r.Context(), current.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check dependencies")
			return
		}
		if !okDeps {
			writeError(w, http.StatusConflict, "serial dependencies are not satisfied")
			return
		}
	}
	if current.Title == "" || current.Purpose == "" || len(current.Title) > 200 || len(current.Purpose) > 4000 {
		writeError(w, http.StatusBadRequest, "title and purpose are required")
		return
	}
	artifactJSON, _ := json.Marshal(current.ArtifactRefs)
	activityJSON, _ := json.Marshal(current.ActivityDelta)
	updated, err := scanChannelGoalSubgoal(h.DB.QueryRow(r.Context(), `
		UPDATE channel_goal_subgoal
		SET title=$1, purpose=$2, completion_boundary=$3, brief=$4, current_conclusion=$5,
		    status=$6, responsible_type=$7, responsible_id=$8::uuid, waiting_on=$9::jsonb,
		    artifact_refs=$10::jsonb, activity_delta=$11::jsonb, source_message_id=$12::uuid,
		    updated_by_type='user', updated_by_id=$13, version=version+1, updated_at=now(),
		    resolved_at = CASE WHEN $6 IN ('resolved','cancelled') THEN COALESCE(resolved_at, now()) ELSE NULL END,
		    waiting_on_verified_at = CASE WHEN $9::jsonb = 'null'::jsonb THEN waiting_on_verified_at ELSE NULL END
		WHERE id=$14 AND version=$15
		RETURNING `+channelGoalSubgoalColumns,
		current.Title, current.Purpose, current.CompletionBoundary, current.Brief, current.CurrentConclusion,
		current.Status, current.ResponsibleType, current.ResponsibleID, waitingJSON,
		artifactJSON, activityJSON, nullableUUIDArg(sourceMessageID), parseUUID(userID), subgoalID, req.ExpectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "subgoal version is stale")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update subgoal")
		return
	}
	if req.Participants != nil {
		participants, ok := normalizeParticipants(*req.Participants, subgoalActor{Type: updated.ResponsibleType, ID: updated.ResponsibleID})
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid participants")
			return
		}
		if err := h.replaceSubgoalParticipants(r.Context(), workspaceID, updated.ID, participants); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update participants")
			return
		}
	}
	_ = h.hydrateSubgoalRelations(r.Context(), &updated)
	writeJSON(w, http.StatusOK, subgoalEnvelope{Subgoal: &updated})
}

func (h *Handler) ResolveChannelGoalSubgoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceIDStr := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	subgoalID, ok2 := parseUUIDOrBadRequest(w, chi.URLParam(r, "subgoalId"), "subgoal id")
	if !ok || !ok2 || !h.requireChannelWritable(w, r.Context(), workspaceIDStr, channelID) ||
		!h.requireChannelManager(w, r, workspaceIDStr, channelID, parseUUID(userID)) {
		return
	}
	var req resolveSubgoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version is required")
		return
	}
	conclusion := strings.TrimSpace(req.CurrentConclusion)
	if conclusion == "" || len(conclusion) > 16000 {
		writeError(w, http.StatusBadRequest, "current_conclusion is required")
		return
	}
	// Resolve updates only this subgoal row — never cascades to issues / Needs You.
	updated, err := scanChannelGoalSubgoal(h.DB.QueryRow(r.Context(), `
		UPDATE channel_goal_subgoal
		SET status='resolved', current_conclusion=$1, waiting_on='null'::jsonb,
		    updated_by_type='user', updated_by_id=$2, version=version+1,
		    updated_at=now(), resolved_at=now()
		WHERE id=$3 AND workspace_id=$4 AND channel_id=$5 AND version=$6
		  AND status NOT IN ('resolved','cancelled')
		RETURNING `+channelGoalSubgoalColumns,
		conclusion, parseUUID(userID), subgoalID, parseUUID(workspaceIDStr), channelID, req.ExpectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "subgoal version is stale or already closed")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve subgoal")
		return
	}
	_ = h.hydrateSubgoalRelations(r.Context(), &updated)
	writeJSON(w, http.StatusOK, subgoalEnvelope{Subgoal: &updated})
}

func (h *Handler) ClearChannelGoalSubgoalWaitingOn(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceIDStr := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	subgoalID, ok2 := parseUUIDOrBadRequest(w, chi.URLParam(r, "subgoalId"), "subgoal id")
	if !ok || !ok2 || !h.requireChannelWritable(w, r.Context(), workspaceIDStr, channelID) ||
		!h.requireChannelManager(w, r, workspaceIDStr, channelID, parseUUID(userID)) {
		return
	}
	var req clearWaitingOnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version and verification are required")
		return
	}
	current, err := scanChannelGoalSubgoal(h.DB.QueryRow(r.Context(), `
		SELECT `+channelGoalSubgoalColumns+`
		FROM channel_goal_subgoal
		WHERE id=$1 AND workspace_id=$2 AND channel_id=$3`, subgoalID, parseUUID(workspaceIDStr), channelID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "subgoal not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load subgoal")
		return
	}
	if current.Version != req.ExpectedVersion {
		writeError(w, http.StatusConflict, "subgoal version is stale")
		return
	}
	var wo subgoalWaitingOn
	if err := json.Unmarshal(current.WaitingOn, &wo); err != nil || strings.TrimSpace(wo.Kind) == "" {
		writeError(w, http.StatusConflict, "subgoal is not waiting")
		return
	}
	if !h.verifyWaitingOnCleared(r.Context(), wo, req) {
		writeError(w, http.StatusConflict, "waiting_on source verification failed; re-check source before clearing")
		return
	}
	updated, err := scanChannelGoalSubgoal(h.DB.QueryRow(r.Context(), `
		UPDATE channel_goal_subgoal
		SET waiting_on='null'::jsonb, waiting_on_verified_at=now(),
		    status=CASE WHEN status='waiting' THEN 'in_progress' ELSE status END,
		    updated_by_type='user', updated_by_id=$1, version=version+1, updated_at=now()
		WHERE id=$2 AND version=$3
		RETURNING `+channelGoalSubgoalColumns,
		parseUUID(userID), subgoalID, req.ExpectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "subgoal version is stale")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear waiting_on")
		return
	}
	_ = h.hydrateSubgoalRelations(r.Context(), &updated)
	writeJSON(w, http.StatusOK, subgoalEnvelope{Subgoal: &updated})
}

func (h *Handler) verifyWaitingOnCleared(ctx context.Context, wo subgoalWaitingOn, req clearWaitingOnRequest) bool {
	kind := strings.ToLower(strings.TrimSpace(req.Verification.Kind))
	if kind == "" {
		kind = strings.ToLower(strings.TrimSpace(wo.Kind))
	}
	if kind != strings.ToLower(strings.TrimSpace(wo.Kind)) {
		return false
	}
	target := strings.TrimSpace(req.Verification.TargetID)
	if target == "" {
		target = strings.TrimSpace(wo.TargetID)
	}
	if strings.TrimSpace(wo.TargetID) != "" && target != strings.TrimSpace(wo.TargetID) {
		return false
	}
	switch kind {
	case "issue":
		if target == "" {
			return false
		}
		var status string
		err := h.DB.QueryRow(ctx, `SELECT status FROM issue WHERE id=$1::uuid`, target).Scan(&status)
		if err != nil {
			return false
		}
		// Source verification: issue must be terminal, or caller attests after checking.
		return status == "done" || status == "cancelled" || req.Verification.IssueStatusOK
	case "member":
		return req.Verification.Acknowledged
	case "lock":
		return req.Verification.Released
	case "pr", "external":
		return req.Verification.ExternalOK || req.Verification.IssueStatusOK
	default:
		return false
	}
}

// channelSubgoalContextsForClaim returns bounded subgoal views for the claiming agent.
func (h *Handler) channelSubgoalContextsForClaim(ctx context.Context, workspaceID, channelID, agentID pgtype.UUID, goalID string) []protocol.ChannelSubgoalContext {
	if !agentID.Valid || strings.TrimSpace(goalID) == "" {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT `+channelGoalSubgoalColumns+`
		FROM channel_goal_subgoal sg
		WHERE sg.workspace_id = $1 AND sg.channel_id = $2 AND sg.goal_id = $3::uuid
		  AND sg.status IN ('captured','in_progress','waiting')
		  AND (
		    (sg.responsible_type = 'agent' AND sg.responsible_id = $4)
		    OR EXISTS (
		      SELECT 1 FROM channel_goal_subgoal_participant p
		      WHERE p.subgoal_id = sg.id AND p.participant_type = 'agent' AND p.participant_id = $4
		    )
		  )
		ORDER BY sg.updated_at DESC, sg.id
		LIMIT $5`, workspaceID, channelID, goalID, agentID, subgoalInjectMax)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]protocol.ChannelSubgoalContext, 0)
	for rows.Next() {
		sg, err := scanChannelGoalSubgoal(rows)
		if err != nil {
			continue
		}
		role := "participant"
		if sg.ResponsibleType == "agent" && sg.ResponsibleID == uuidToString(agentID) {
			role = "responsible"
		}
		delta := sg.ActivityDelta
		if len(delta) > subgoalActivityMax {
			delta = delta[len(delta)-subgoalActivityMax:]
		}
		item := protocol.ChannelSubgoalContext{
			ID:                 sg.ID,
			Title:              sg.Title,
			Purpose:            sg.Purpose,
			CompletionBoundary: sg.CompletionBoundary,
			Version:            sg.Version,
			Status:             sg.Status,
			OwnRole:            role,
			ActivityDelta:      delta,
			ArtifactRefs:       sg.ArtifactRefs,
		}
		var wo subgoalWaitingOn
		if json.Unmarshal(sg.WaitingOn, &wo) == nil && strings.TrimSpace(wo.Kind) != "" {
			item.WaitingOnKind = wo.Kind
			item.WaitingOnNote = wo.Note
		}
		out = append(out, item)
	}
	return out
}

// Agent-facing wrappers (manager agents).
func (h *Handler) ListAgentChannelGoalSubgoals(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, _, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	goal, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, subgoalListEnvelope{Subgoals: []ChannelGoalSubgoalResponse{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	items, err := h.listChannelGoalSubgoalsHydrated(r.Context(), goal.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list subgoals")
		return
	}
	writeJSON(w, http.StatusOK, subgoalListEnvelope{Subgoals: items})
}

func (h *Handler) CreateAgentChannelGoalSubgoal(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, agentID, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	if !h.agentGoalChannelWritable(r.Context(), workspaceID, channelID) || !h.agentIsChannelManager(r.Context(), workspaceID, channelID, agentID) {
		writeError(w, http.StatusForbidden, "manager role required")
		return
	}
	goal, err := h.currentChannelGoal(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel goal not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		return
	}
	var req createSubgoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sg, err := h.insertSubgoal(r.Context(), workspaceID, channelID, parseUUID(goal.ID), "agent", agentID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, subgoalEnvelope{Subgoal: &sg})
}
