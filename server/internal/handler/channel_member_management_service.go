package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Test-only observation point used to prove a member-management request has
// entered the channel FOR UPDATE attempt while another transaction owns it.
var testMemberManagementLockAttemptEntered int32

func noteMemberManagementLockAttempt() {
	atomic.AddInt32(&testMemberManagementLockAttemptEntered, 1)
}

type memberManagementHTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *memberManagementHTTPError) Error() string {
	return e.Message
}

func memberManagementError(status int, message string) error {
	return &memberManagementHTTPError{Status: status, Message: message}
}

func writeMemberManagementError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, errChannelSystemProtected) {
		writeSystemChannelProtected(w)
		return
	}
	var httpErr *memberManagementHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Code != "" {
			writeCodedError(w, httpErr.Status, httpErr.Code, httpErr.Message)
			return
		}
		writeError(w, httpErr.Status, httpErr.Message)
		return
	}
	slog.Error(fallback, "error", err)
	writeError(w, http.StatusInternalServerError, fallback)
}

type memberManagementActor struct {
	Kind        PrincipalKind
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (a memberManagementActor) provenance() channelMemberActor {
	if a.Kind == PrincipalKindAgent {
		return channelMemberAgentActor(a.ID)
	}
	return channelMemberUserActor(a.ID)
}

func (a memberManagementActor) activityType() string {
	if a.Kind == PrincipalKindAgent {
		return "agent"
	}
	return "member"
}

type lockedMemberManagementContext struct {
	Principal MemberManagementPrincipal
}

type memberManagementMutationResult struct {
	Mutated              bool
	SystemMessages       []ChannelMessageResponse
	OnboardingGeneration []pgtype.UUID
}

func (h *Handler) AddAgentChannelMember(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	actor, ok := memberManagementActorFromAgentPrincipal(w, r, p)
	if !ok {
		return
	}
	h.addChannelMemberAdapter(w, r, actor)
}

func (h *Handler) AddAgentChannelMembers(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	actor, ok := memberManagementActorFromAgentPrincipal(w, r, p)
	if !ok {
		return
	}
	h.addChannelMembersAdapter(w, r, actor)
}

func (h *Handler) RemoveAgentChannelMember(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	actor, ok := memberManagementActorFromAgentPrincipal(w, r, p)
	if !ok {
		return
	}
	h.removeChannelMemberAdapter(w, r, actor)
}

func memberManagementActorFromAgentPrincipal(
	w http.ResponseWriter,
	r *http.Request,
	p middleware.AgentPrincipal,
) (memberManagementActor, bool) {
	agentID, ok := p.AgentUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return memberManagementActor{}, false
	}
	workspaceID, ok := p.WorkspaceUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return memberManagementActor{}, false
	}
	if requestWorkspaceID := strings.TrimSpace(ctxWorkspaceID(r.Context())); requestWorkspaceID != "" &&
		requestWorkspaceID != p.WorkspaceID {
		writeError(w, http.StatusForbidden, "access denied")
		return memberManagementActor{}, false
	}
	return memberManagementActor{
		Kind:        PrincipalKindAgent,
		ID:          agentID,
		WorkspaceID: workspaceID,
	}, true
}

func humanMemberManagementActor(workspaceID, userID string) memberManagementActor {
	return memberManagementActor{
		Kind:        PrincipalKindUser,
		ID:          parseUUID(userID),
		WorkspaceID: parseUUID(workspaceID),
	}
}

func (h *Handler) addChannelMemberAdapter(
	w http.ResponseWriter,
	r *http.Request,
	actor memberManagementActor,
) {
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var request AddChannelMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target, err := parseMemberManagementTargetInput(request)
	if err != nil {
		writeMemberManagementError(w, err, "failed to add channel member")
		return
	}
	result, err := h.addChannelMembersService(r.Context(), actor, channelID, []MemberManagementTarget{target})
	if err != nil {
		writeMemberManagementError(w, err, "failed to add channel member")
		return
	}
	h.publishMemberManagementResult(r.Context(), actor, channelID, result)
	status := http.StatusOK
	if result.Mutated {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]string{"status": "ok"})
}

func (h *Handler) addChannelMembersAdapter(
	w http.ResponseWriter,
	r *http.Request,
	actor memberManagementActor,
) {
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var request AddChannelMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	targets := make([]MemberManagementTarget, 0, len(request.Members))
	for _, input := range request.Members {
		target, err := parseMemberManagementTargetInput(input)
		if err != nil {
			writeMemberManagementError(w, err, "failed to add channel members")
			return
		}
		targets = append(targets, target)
	}
	result, err := h.addChannelMembersService(r.Context(), actor, channelID, targets)
	if err != nil {
		writeMemberManagementError(w, err, "failed to add channel members")
		return
	}
	h.publishMemberManagementResult(r.Context(), actor, channelID, result)
	status := http.StatusOK
	if result.Mutated {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]string{"status": "ok"})
}

func parseMemberManagementTargetInput(input AddChannelMemberRequest) (MemberManagementTarget, error) {
	var kind PrincipalKind
	switch strings.TrimSpace(input.MemberType) {
	case string(PrincipalKindUser):
		kind = PrincipalKindUser
	case string(PrincipalKindAgent):
		kind = PrincipalKindAgent
	default:
		return MemberManagementTarget{}, memberManagementError(
			http.StatusBadRequest,
			"member_type must be user or agent",
		)
	}
	id, err := parseMemberManagementUUID(input.MemberID)
	if err != nil {
		return MemberManagementTarget{}, memberManagementError(http.StatusBadRequest, "invalid member_id")
	}
	return MemberManagementTarget{Kind: kind, ID: id, Role: ChannelRoleMember}, nil
}

func parseMemberManagementUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(strings.TrimSpace(value)); err != nil || !validMemberManagementUUID(id) {
		return pgtype.UUID{}, fmt.Errorf("invalid uuid")
	}
	return id, nil
}

func (h *Handler) removeChannelMemberAdapter(
	w http.ResponseWriter,
	r *http.Request,
	actor memberManagementActor,
) {
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	target, err := parseMemberManagementTargetInput(AddChannelMemberRequest{
		MemberType: chi.URLParam(r, "memberType"),
		MemberID:   chi.URLParam(r, "memberId"),
	})
	if err != nil {
		writeMemberManagementError(w, err, "failed to remove channel member")
		return
	}
	result, err := h.removeChannelMemberService(
		r.Context(),
		actor,
		channelID,
		target,
	)
	if err != nil {
		writeMemberManagementError(w, err, "failed to remove channel member")
		return
	}
	h.publishMemberManagementResult(r.Context(), actor, channelID, result)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) addChannelMembersService(
	ctx context.Context,
	actor memberManagementActor,
	channelID pgtype.UUID,
	targets []MemberManagementTarget,
) (memberManagementMutationResult, error) {
	if h.TxStarter == nil {
		return memberManagementMutationResult{}, errors.New("transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return memberManagementMutationResult{}, err
	}
	defer tx.Rollback(ctx)

	locked, err := h.lockMemberManagementContext(ctx, tx, actor, channelID)
	if err != nil {
		return memberManagementMutationResult{}, err
	}
	if err := authorizeMemberManagement(MemberManagementRequest{
		Principal:       locked.Principal,
		Action:          MemberManagementAddMember,
		ChannelVisible:  true,
		ChannelWritable: true,
	}); err != nil {
		return memberManagementMutationResult{}, err
	}

	for i := range targets {
		targets[i].Role = ChannelRoleMember
		if err := h.validateMemberManagementTargetTx(ctx, tx, locked.Principal, channelID, targets[i]); err != nil {
			return memberManagementMutationResult{}, err
		}
	}

	actorProvenance := actor.provenance()
	if err := validateChannelMemberActorWithExec(
		ctx,
		tx,
		uuidToString(actor.WorkspaceID),
		actorProvenance,
	); err != nil {
		return memberManagementMutationResult{}, memberManagementError(
			http.StatusForbidden,
			"channel member actor is not available",
		)
	}

	result := memberManagementMutationResult{}
	for _, target := range targets {
		var generationID pgtype.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO channel_member (
				channel_id, workspace_id, member_type, member_id,
				added_by_type, added_by_id, join_source
			)
			VALUES ($1, $2, $3, $4, $5, $6, 'manual')
			ON CONFLICT DO NOTHING
			RETURNING generation_id`,
			channelID,
			actor.WorkspaceID,
			string(target.Kind),
			target.ID,
			actorProvenance.Type,
			actorProvenance.ID,
		).Scan(&generationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return memberManagementMutationResult{}, err
		}

		if err := insertMemberManagementActivityTx(
			ctx,
			tx,
			locked.Principal,
			target,
			channelID,
			"member_added",
			chimw.GetReqID(ctx),
		); err != nil {
			return memberManagementMutationResult{}, err
		}

		result.Mutated = true
		if target.Kind == PrincipalKindAgent {
			result.OnboardingGeneration = append(result.OnboardingGeneration, generationID)
			continue
		}
		systemMessage, err := h.insertChannelMemberSystemEventExec(
			ctx,
			tx,
			uuidToString(actor.WorkspaceID),
			channelID,
			channelMemberAddedEvent,
			actorProvenance.Type,
			actorProvenance.ID,
			string(target.Kind),
			target.ID,
		)
		if err != nil {
			return memberManagementMutationResult{}, err
		}
		result.SystemMessages = append(result.SystemMessages, systemMessage)
	}

	if err := tx.Commit(ctx); err != nil {
		return memberManagementMutationResult{}, err
	}
	return result, nil
}

func (h *Handler) removeChannelMemberService(
	ctx context.Context,
	actor memberManagementActor,
	channelID pgtype.UUID,
	target MemberManagementTarget,
) (memberManagementMutationResult, error) {
	if h.TxStarter == nil {
		return memberManagementMutationResult{}, errors.New("transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return memberManagementMutationResult{}, err
	}
	defer tx.Rollback(ctx)

	locked, err := h.lockMemberManagementContext(ctx, tx, actor, channelID)
	if err != nil {
		return memberManagementMutationResult{}, err
	}

	var addedByType pgtype.Text
	var addedByID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT role, added_by_type, added_by_id
		FROM channel_member
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = $3
		  AND member_id = $4
		FOR UPDATE`,
		channelID,
		actor.WorkspaceID,
		string(target.Kind),
		target.ID,
	).Scan((*string)(&target.Role), &addedByType, &addedByID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return memberManagementMutationResult{}, memberManagementError(http.StatusNotFound, "channel member not found")
		}
		return memberManagementMutationResult{}, err
	}
	if addedByType.Valid {
		target.AddedByKind = PrincipalKind(addedByType.String)
	}
	target.AddedByID = addedByID

	isSelf := locked.Principal.Kind == target.Kind &&
		locked.Principal.ID.Bytes == target.ID.Bytes
	action := MemberManagementRemoveMember
	auditAction := "member_removed"
	event := channelMemberRemovedEvent
	if isSelf {
		action = MemberManagementLeave
		auditAction = "member_left"
		event = channelMemberLeftEvent
		if target.Kind == PrincipalKindUser && target.Role == ChannelRoleOwner {
			var otherOwners int
			if err := tx.QueryRow(ctx, `
				SELECT count(*)::int
				FROM channel_member
				WHERE channel_id = $1
				  AND workspace_id = $2
				  AND member_type = 'user'
				  AND role = 'owner'
				  AND member_id <> $3`,
				channelID,
				actor.WorkspaceID,
				target.ID,
			).Scan(&otherOwners); err != nil {
				return memberManagementMutationResult{}, err
			}
			target.WouldBreakOwnerInvariant = otherOwners == 0
		}
	}

	if err := authorizeMemberManagement(MemberManagementRequest{
		Principal:       locked.Principal,
		Action:          action,
		Target:          &target,
		ChannelVisible:  true,
		ChannelWritable: true,
	}); err != nil {
		return memberManagementMutationResult{}, err
	}

	if target.Kind == PrincipalKindAgent {
		if _, err := tx.Exec(ctx, `
			SELECT pg_advisory_xact_lock(
				hashtext('agent_channel_membership_revoke'),
				hashtext($1 || ':' || $2)
			)`,
			uuidToString(channelID),
			uuidToString(target.ID),
		); err != nil {
			return memberManagementMutationResult{}, err
		}
	}

	tag, err := tx.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = $3
		  AND member_id = $4`,
		channelID,
		actor.WorkspaceID,
		string(target.Kind),
		target.ID,
	)
	if err != nil {
		return memberManagementMutationResult{}, err
	}
	if tag.RowsAffected() != 1 {
		return memberManagementMutationResult{}, memberManagementError(
			http.StatusConflict,
			"channel membership changed",
		)
	}

	if target.Kind == PrincipalKindAgent {
		if err := revokeAgentChannelAccessTx(
			ctx,
			tx,
			actor.WorkspaceID,
			channelID,
			target.ID,
		); err != nil {
			return memberManagementMutationResult{}, err
		}
	}

	if err := insertMemberManagementActivityTx(
		ctx,
		tx,
		locked.Principal,
		target,
		channelID,
		auditAction,
		chimw.GetReqID(ctx),
	); err != nil {
		return memberManagementMutationResult{}, err
	}

	actorProvenance := actor.provenance()
	systemMessage, err := h.insertChannelMemberSystemEventExec(
		ctx,
		tx,
		uuidToString(actor.WorkspaceID),
		channelID,
		event,
		actorProvenance.Type,
		actorProvenance.ID,
		string(target.Kind),
		target.ID,
	)
	if err != nil {
		return memberManagementMutationResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return memberManagementMutationResult{}, err
	}
	return memberManagementMutationResult{
		Mutated:        true,
		SystemMessages: []ChannelMessageResponse{systemMessage},
	}, nil
}

func (h *Handler) lockMemberManagementContext(
	ctx context.Context,
	tx pgx.Tx,
	actor memberManagementActor,
	channelID pgtype.UUID,
) (lockedMemberManagementContext, error) {
	var kind string
	var systemKey pgtype.Text
	var archivedAt pgtype.Timestamptz
	noteMemberManagementLockAttempt()
	err := tx.QueryRow(ctx, `
		SELECT kind, system_key, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`,
		channelID,
		actor.WorkspaceID,
	).Scan(&kind, &systemKey, &archivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lockedMemberManagementContext{}, memberManagementError(http.StatusNotFound, "channel not found")
		}
		return lockedMemberManagementContext{}, err
	}
	if kind != "group" {
		return lockedMemberManagementContext{}, memberManagementError(http.StatusNotFound, "channel not found")
	}
	if systemKey.Valid {
		return lockedMemberManagementContext{}, errChannelSystemProtected
	}
	if archivedAt.Valid {
		return lockedMemberManagementContext{}, memberManagementError(http.StatusConflict, "channel is archived")
	}

	var workspaceRole string
	switch actor.Kind {
	case PrincipalKindUser:
		err = tx.QueryRow(ctx, `
			SELECT role
			FROM member
			WHERE workspace_id = $1 AND user_id = $2
			FOR SHARE`,
			actor.WorkspaceID,
			actor.ID,
		).Scan(&workspaceRole)
	case PrincipalKindAgent:
		err = tx.QueryRow(ctx, `
			SELECT workspace_role
			FROM agent
			WHERE workspace_id = $1
			  AND id = $2
			  AND archived_at IS NULL
			FOR SHARE`,
			actor.WorkspaceID,
			actor.ID,
		).Scan(&workspaceRole)
	default:
		return lockedMemberManagementContext{}, memberManagementError(http.StatusForbidden, "access denied")
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lockedMemberManagementContext{}, memberManagementError(http.StatusForbidden, "access denied")
		}
		return lockedMemberManagementContext{}, err
	}

	channelRole := string(ChannelRoleNone)
	err = tx.QueryRow(ctx, `
		SELECT role
		FROM channel_member
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = $3
		  AND member_id = $4
		FOR SHARE`,
		channelID,
		actor.WorkspaceID,
		string(actor.Kind),
		actor.ID,
	).Scan(&channelRole)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return lockedMemberManagementContext{}, err
	}

	principal := MemberManagementPrincipal{
		Kind:          actor.Kind,
		ID:            actor.ID,
		WorkspaceID:   actor.WorkspaceID,
		WorkspaceRole: WorkspaceRole(workspaceRole),
		ChannelRole:   ChannelRole(channelRole),
	}
	if !validWorkspaceRole(principal.WorkspaceRole) || !validChannelRole(principal.ChannelRole) {
		return lockedMemberManagementContext{}, memberManagementError(http.StatusForbidden, "access denied")
	}
	return lockedMemberManagementContext{Principal: principal}, nil
}

func (h *Handler) validateMemberManagementTargetTx(
	ctx context.Context,
	tx pgx.Tx,
	principal MemberManagementPrincipal,
	channelID pgtype.UUID,
	target MemberManagementTarget,
) error {
	switch target.Kind {
	case PrincipalKindUser:
		var targetID pgtype.UUID
		err := tx.QueryRow(ctx, `
			SELECT user_id
			FROM member
			WHERE workspace_id = $1 AND user_id = $2
			FOR SHARE`,
			principal.WorkspaceID,
			target.ID,
		).Scan(&targetID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return memberManagementError(http.StatusNotFound, "workspace member not found")
			}
			return err
		}
		return nil
	case PrincipalKindAgent:
		var exists bool
		err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM agent
				WHERE workspace_id = $1
				  AND id = $2
				  AND archived_at IS NULL
				FOR SHARE
			)`,
			principal.WorkspaceID,
			target.ID,
		).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return memberManagementError(http.StatusNotFound, "agent not found")
		}
		return nil
	default:
		return memberManagementError(http.StatusBadRequest, "member_type must be user or agent")
	}
}

func memberManagementDecisionError(decision MemberManagementDecision) error {
	switch decision.Code {
	case MemberManagementCodeChannelNotVisible:
		return memberManagementError(http.StatusNotFound, decision.Code)
	case MemberManagementCodeChannelNotWritable, MemberManagementCodeOwnerInvariant:
		return memberManagementError(http.StatusConflict, decision.Code)
	default:
		return memberManagementError(http.StatusForbidden, decision.Code)
	}
}

func authorizeMemberManagement(req MemberManagementRequest) error {
	decision := DecideMemberManagement(req)
	if !decision.Allowed {
		return memberManagementDecisionError(decision)
	}
	return nil
}

func insertMemberManagementActivityTx(
	ctx context.Context,
	tx pgx.Tx,
	principal MemberManagementPrincipal,
	target MemberManagementTarget,
	channelID pgtype.UUID,
	action string,
	requestID string,
) error {
	authoritySource := "channel_membership"
	if principal.ChannelRole == ChannelRoleNone &&
		hasWorkspaceManagementAuthority(principal.WorkspaceRole) {
		authoritySource = "workspace_admin_override"
	}
	details := map[string]any{
		"channel_id":           uuidToString(channelID),
		"actor_workspace_role": string(principal.WorkspaceRole),
		"actor_channel_role":   string(principal.ChannelRole),
		"target_type":          string(target.Kind),
		"target_id":            uuidToString(target.ID),
		"target_role":          string(target.Role),
		"authority_source":     authoritySource,
		"request_id":           requestID,
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	actorType := "member"
	if principal.Kind == PrincipalKindAgent {
		actorType = "agent"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO activity_log (
			workspace_id, actor_type, actor_id, action, details
		)
		VALUES ($1, $2, $3, $4, $5)`,
		principal.WorkspaceID,
		actorType,
		principal.ID,
		action,
		encoded,
	)
	return err
}

func (h *Handler) publishMemberManagementResult(
	ctx context.Context,
	actor memberManagementActor,
	channelID pgtype.UUID,
	result memberManagementMutationResult,
) {
	if !result.Mutated {
		return
	}
	h.publish(
		protocol.EventChannelUpdated,
		uuidToString(actor.WorkspaceID),
		actor.activityType(),
		uuidToString(actor.ID),
		map[string]any{"id": uuidToString(channelID)},
	)
	for _, message := range result.SystemMessages {
		h.publishChannelToMembers(
			ctx,
			protocol.EventChannelMessage,
			uuidToString(actor.WorkspaceID),
			"system",
			"",
			channelID,
			message,
		)
	}
	for _, generationID := range result.OnboardingGeneration {
		if err := h.publishChannelOnboardingSystemMessageForGeneration(ctx, generationID); err != nil {
			slog.Warn(
				"channel member management: publish onboarding system message failed",
				"channel",
				uuidToString(channelID),
				"generation",
				uuidToString(generationID),
				"error",
				err,
			)
		}
	}
}
