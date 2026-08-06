package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type memberManagementCapabilitiesProjection struct {
	ChannelID        string                                       `json:"channel_id"`
	Name             string                                       `json:"name"`
	Kind             string                                       `json:"kind"`
	Archived         bool                                         `json:"archived"`
	CanAddMembers    bool                                         `json:"can_add_members"`
	CanRemoveMembers bool                                         `json:"can_remove_members"`
	CanLeave         bool                                         `json:"can_leave"`
	Targets          []memberManagementCapabilityTargetProjection `json:"targets"`
}

type memberManagementCapabilityTargetProjection struct {
	MemberType           string  `json:"member_type"`
	MemberID             string  `json:"member_id"`
	DisplayName          string  `json:"display_name"`
	AvatarURL            *string `json:"avatar_url"`
	Role                 string  `json:"role"`
	CanRemove            bool    `json:"can_remove"`
	CanPromoteToManager  bool    `json:"can_promote_to_manager"`
	CanDemoteToMember    bool    `json:"can_demote_to_member"`
	CanTransferOwnership bool    `json:"can_transfer_ownership"`
}

type memberManagementCapabilityRosterTarget struct {
	Target      MemberManagementTarget
	DisplayName string
	AvatarURL   *string
}

func (h *Handler) GetChannelMemberManagementCapabilities(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "GetChannelMemberManagementCapabilities") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	h.writeMemberManagementCapabilities(
		w,
		r,
		humanMemberManagementActor(workspaceID, userID),
	)
}

func (h *Handler) GetAgentChannelMemberManagementCapabilities(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	actor, ok := memberManagementActorFromAgentPrincipal(w, r, principal)
	if !ok {
		return
	}
	h.writeMemberManagementCapabilities(w, r, actor)
}

func (h *Handler) writeMemberManagementCapabilities(
	w http.ResponseWriter,
	r *http.Request,
	actor memberManagementActor,
) {
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	projection, err := h.getMemberManagementCapabilities(r.Context(), actor, channelID)
	if err != nil {
		writeMemberManagementError(w, err, "failed to load member management capabilities")
		return
	}
	writeJSON(w, http.StatusOK, projection)
}

func (h *Handler) getMemberManagementCapabilities(
	ctx context.Context,
	actor memberManagementActor,
	channelID pgtype.UUID,
) (memberManagementCapabilitiesProjection, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return memberManagementCapabilitiesProjection{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return memberManagementCapabilitiesProjection{}, err
	}

	var (
		channelName string
		channelKind string
		systemKey   pgtype.Text
		archivedAt  pgtype.Timestamptz
	)
	err = tx.QueryRow(ctx, `
		SELECT name, kind, system_key, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2`,
		channelID,
		actor.WorkspaceID,
	).Scan(&channelName, &channelKind, &systemKey, &archivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return memberManagementCapabilitiesProjection{}, memberManagementError(http.StatusNotFound, "channel not found")
		}
		return memberManagementCapabilitiesProjection{}, err
	}
	if channelKind != "group" || systemKey.Valid {
		return memberManagementCapabilitiesProjection{}, memberManagementError(http.StatusNotFound, "channel not found")
	}

	principal, err := loadMemberManagementCapabilityPrincipal(ctx, tx, actor, channelID)
	if err != nil {
		return memberManagementCapabilitiesProjection{}, err
	}
	if principal.ChannelRole == ChannelRoleNone &&
		!hasWorkspaceManagementAuthority(principal.WorkspaceRole) {
		return memberManagementCapabilitiesProjection{}, memberManagementError(http.StatusNotFound, "channel not found")
	}

	roster, err := loadMemberManagementCapabilityRoster(
		ctx,
		tx,
		actor.WorkspaceID,
		channelID,
	)
	if err != nil {
		return memberManagementCapabilitiesProjection{}, err
	}

	channelWritable := !archivedAt.Valid
	projection := memberManagementCapabilitiesProjection{
		ChannelID: uuidToString(channelID),
		Name:      channelName,
		Kind:      channelKind,
		Archived:  archivedAt.Valid,
		Targets:   make([]memberManagementCapabilityTargetProjection, 0, len(roster)),
	}
	projection.CanAddMembers = DecideMemberManagement(MemberManagementRequest{
		Principal:       principal,
		Action:          MemberManagementAddMember,
		ChannelVisible:  true,
		ChannelWritable: channelWritable,
	}).Allowed
	projection.CanRemoveMembers = DecideMemberManagement(MemberManagementRequest{
		Principal:       principal,
		Action:          MemberManagementRemoveMember,
		Target:          ordinaryMemberManagementCapabilityProbe(principal),
		ChannelVisible:  true,
		ChannelWritable: channelWritable,
	}).Allowed

	if principal.ChannelRole != ChannelRoleNone {
		self := MemberManagementTarget{
			Kind:                     principal.Kind,
			ID:                       principal.ID,
			Role:                     principal.ChannelRole,
			WouldBreakOwnerInvariant: principal.ChannelRole == ChannelRoleOwner,
		}
		projection.CanLeave = DecideMemberManagement(MemberManagementRequest{
			Principal:       principal,
			Action:          MemberManagementLeave,
			Target:          &self,
			ChannelVisible:  true,
			ChannelWritable: channelWritable,
		}).Allowed
	}

	for _, member := range roster {
		target := member.Target
		projection.Targets = append(
			projection.Targets,
			memberManagementCapabilityTargetProjection{
				MemberType:           string(target.Kind),
				MemberID:             uuidToString(target.ID),
				DisplayName:          member.DisplayName,
				AvatarURL:            member.AvatarURL,
				Role:                 string(target.Role),
				CanRemove:            decideProjectedMemberManagementAction(principal, MemberManagementRemoveMember, target, channelWritable),
				CanPromoteToManager:  decideProjectedMemberManagementAction(principal, MemberManagementPromoteManager, target, channelWritable),
				CanDemoteToMember:    decideProjectedMemberManagementAction(principal, MemberManagementDemoteManager, target, channelWritable),
				CanTransferOwnership: decideProjectedMemberManagementAction(principal, MemberManagementTransferOwnership, target, channelWritable),
			},
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return memberManagementCapabilitiesProjection{}, err
	}
	return projection, nil
}

func loadMemberManagementCapabilityPrincipal(
	ctx context.Context,
	tx pgx.Tx,
	actor memberManagementActor,
	channelID pgtype.UUID,
) (MemberManagementPrincipal, error) {
	var workspaceRole string
	var err error
	switch actor.Kind {
	case PrincipalKindUser:
		err = tx.QueryRow(ctx, `
			SELECT role
			FROM member
			WHERE workspace_id = $1 AND user_id = $2`,
			actor.WorkspaceID,
			actor.ID,
		).Scan(&workspaceRole)
	case PrincipalKindAgent:
		err = tx.QueryRow(ctx, `
			SELECT workspace_role
			FROM agent
			WHERE workspace_id = $1
			  AND id = $2
			  AND archived_at IS NULL`,
			actor.WorkspaceID,
			actor.ID,
		).Scan(&workspaceRole)
	default:
		return MemberManagementPrincipal{}, memberManagementError(http.StatusForbidden, "access denied")
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemberManagementPrincipal{}, memberManagementError(http.StatusForbidden, "access denied")
		}
		return MemberManagementPrincipal{}, err
	}

	channelRole := string(ChannelRoleNone)
	err = tx.QueryRow(ctx, `
		SELECT role
		FROM channel_member
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = $3
		  AND member_id = $4`,
		channelID,
		actor.WorkspaceID,
		string(actor.Kind),
		actor.ID,
	).Scan(&channelRole)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return MemberManagementPrincipal{}, err
	}

	principal := MemberManagementPrincipal{
		Kind:          actor.Kind,
		ID:            actor.ID,
		WorkspaceID:   actor.WorkspaceID,
		WorkspaceRole: WorkspaceRole(workspaceRole),
		ChannelRole:   ChannelRole(channelRole),
	}
	if !validWorkspaceRole(principal.WorkspaceRole) || !validChannelRole(principal.ChannelRole) {
		return MemberManagementPrincipal{}, memberManagementError(http.StatusForbidden, "access denied")
	}
	if principal.Kind == PrincipalKindAgent &&
		(principal.WorkspaceRole == WorkspaceRoleOwner || principal.ChannelRole == ChannelRoleOwner) {
		return MemberManagementPrincipal{}, memberManagementError(http.StatusForbidden, "access denied")
	}
	return principal, nil
}

func loadMemberManagementCapabilityRoster(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, channelID pgtype.UUID,
) ([]memberManagementCapabilityRosterTarget, error) {
	rows, err := tx.Query(ctx, `
		SELECT cm.member_type,
		       cm.member_id,
		       COALESCE(
		         NULLIF(u.display_name, ''),
		         u.name,
		         u.email,
		         NULLIF(a.display_name, ''),
		         a.name,
		         ''
		       ) AS display_name,
		       CASE WHEN cm.member_type = 'user' THEN u.avatar_url ELSE a.avatar_url END,
		       cm.role,
		       cm.added_by_type,
		       cm.added_by_id
		FROM channel_member cm
		LEFT JOIN "user" u
		  ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a
		  ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		ORDER BY
		  CASE cm.role
		    WHEN 'owner' THEN 0
		    WHEN 'manager' THEN 1
		    ELSE 2
		  END,
		  cm.created_at ASC,
		  cm.member_type ASC,
		  cm.member_id ASC`,
		channelID,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roster := make([]memberManagementCapabilityRosterTarget, 0)
	for rows.Next() {
		var (
			memberType   string
			memberID     pgtype.UUID
			displayName  string
			avatarURL    pgtype.Text
			role         string
			addedByType  pgtype.Text
			addedByID    pgtype.UUID
		)
		if err := rows.Scan(
			&memberType,
			&memberID,
			&displayName,
			&avatarURL,
			&role,
			&addedByType,
			&addedByID,
		); err != nil {
			return nil, err
		}
		target := MemberManagementTarget{
			Kind:                     PrincipalKind(memberType),
			ID:                       memberID,
			Role:                     ChannelRole(role),
			WouldBreakOwnerInvariant: role == string(ChannelRoleOwner),
			AddedByID:                addedByID,
		}
		if addedByType.Valid {
			target.AddedByKind = PrincipalKind(addedByType.String)
		}
		if !validMemberManagementTarget(&target) || displayName == "" {
			return nil, memberManagementError(http.StatusInternalServerError, "invalid channel member")
		}
		roster = append(roster, memberManagementCapabilityRosterTarget{
			Target:      target,
			DisplayName: displayName,
			AvatarURL:   textToPtr(avatarURL),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roster, nil
}

func ordinaryMemberManagementCapabilityProbe(
	principal MemberManagementPrincipal,
) *MemberManagementTarget {
	id := principal.ID
	id.Bytes[15] ^= 0xff
	if id.Bytes == [16]byte{} {
		id.Bytes[15] = 1
	}
	return &MemberManagementTarget{
		Kind: PrincipalKindUser,
		ID:   id,
		Role: ChannelRoleMember,
	}
}

func decideProjectedMemberManagementAction(
	principal MemberManagementPrincipal,
	action MemberManagementAction,
	target MemberManagementTarget,
	channelWritable bool,
) bool {
	return DecideMemberManagement(MemberManagementRequest{
		Principal:       principal,
		Action:          action,
		Target:          &target,
		ChannelVisible:  true,
		ChannelWritable: channelWritable,
	}).Allowed
}
