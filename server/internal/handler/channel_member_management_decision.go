package handler

import "github.com/jackc/pgx/v5/pgtype"

type PrincipalKind string

const (
	PrincipalKindUser  PrincipalKind = "user"
	PrincipalKindAgent PrincipalKind = "agent"
)

type WorkspaceRole string

const (
	WorkspaceRoleOwner  WorkspaceRole = "owner"
	WorkspaceRoleAdmin  WorkspaceRole = "admin"
	WorkspaceRoleMember WorkspaceRole = "member"
)

type ChannelRole string

const (
	ChannelRoleOwner   ChannelRole = "owner"
	ChannelRoleManager ChannelRole = "manager"
	ChannelRoleMember  ChannelRole = "member"
	ChannelRoleNone    ChannelRole = "none"
)

type MemberManagementPrincipal struct {
	Kind          PrincipalKind
	ID            pgtype.UUID
	WorkspaceID   pgtype.UUID
	WorkspaceRole WorkspaceRole
	ChannelRole   ChannelRole
}

type MemberManagementAction string

const (
	MemberManagementAddMember         MemberManagementAction = "add_member"
	MemberManagementRemoveMember      MemberManagementAction = "remove_member"
	MemberManagementLeave             MemberManagementAction = "leave"
	MemberManagementPromoteManager    MemberManagementAction = "promote_manager"
	MemberManagementDemoteManager     MemberManagementAction = "demote_manager"
	MemberManagementTransferOwnership MemberManagementAction = "transfer_ownership"
)

const (
	MemberManagementCodeChannelNotVisible            = "channel_not_visible"
	MemberManagementCodeChannelNotWritable           = "channel_not_writable"
	MemberManagementCodeForbidden                    = "member_management_forbidden"
	MemberManagementCodeTargetNotOrdinary            = "target_not_ordinary"
	MemberManagementCodeOwnerInvariant               = "owner_invariant"
	MemberManagementCodeWorkspaceRoleChangeOwnerOnly = "workspace_role_change_owner_only"
	MemberManagementCodeAgentCannotBeWorkspaceOwner  = "agent_cannot_be_workspace_owner"
)

type MemberManagementTarget struct {
	Kind                     PrincipalKind
	ID                       pgtype.UUID
	Role                     ChannelRole
	WouldBreakOwnerInvariant bool
	// AddedBy* is optional provenance for inviter-scoped remove (LRM-869).
	// Empty/invalid means no inviter exception applies.
	AddedByKind PrincipalKind
	AddedByID   pgtype.UUID
}

type MemberManagementRequest struct {
	Principal       MemberManagementPrincipal
	Action          MemberManagementAction
	Target          *MemberManagementTarget
	ChannelVisible  bool
	ChannelWritable bool
}

type MemberManagementDecision struct {
	Allowed bool
	Code    string
}

func allowMemberManagement() MemberManagementDecision {
	return MemberManagementDecision{Allowed: true}
}

func denyMemberManagement(code string) MemberManagementDecision {
	return MemberManagementDecision{Code: code}
}

// DecideMemberManagement is the single role-based authorization core shared by
// human and agent adapters. It is intentionally pure: adapters are responsible
// for loading and locking the rows represented by the request before calling
// it, and for mapping the stable denial code to the transport-specific status.
func DecideMemberManagement(req MemberManagementRequest) MemberManagementDecision {
	if !req.ChannelVisible {
		return denyMemberManagement(MemberManagementCodeChannelNotVisible)
	}
	if !req.ChannelWritable {
		return denyMemberManagement(MemberManagementCodeChannelNotWritable)
	}
	if !validPrincipalKind(req.Principal.Kind) {
		return denyMemberManagement(MemberManagementCodeForbidden)
	}
	if !validMemberManagementUUID(req.Principal.ID) ||
		!validMemberManagementUUID(req.Principal.WorkspaceID) {
		return denyMemberManagement(MemberManagementCodeForbidden)
	}
	if !validWorkspaceRole(req.Principal.WorkspaceRole) || !validChannelRole(req.Principal.ChannelRole) {
		return denyMemberManagement(MemberManagementCodeForbidden)
	}
	if req.Principal.Kind == PrincipalKindAgent &&
		(req.Principal.WorkspaceRole == WorkspaceRoleOwner || req.Principal.ChannelRole == ChannelRoleOwner) {
		return denyMemberManagement(MemberManagementCodeAgentCannotBeWorkspaceOwner)
	}

	switch req.Action {
	case MemberManagementAddMember:
		if hasWorkspaceManagementAuthority(req.Principal.WorkspaceRole) ||
			req.Principal.ChannelRole != ChannelRoleNone {
			return allowMemberManagement()
		}
	case MemberManagementRemoveMember:
		if !validMemberManagementTarget(req.Target) || sameMemberManagementIdentity(req.Principal, *req.Target) {
			return denyMemberManagement(MemberManagementCodeForbidden)
		}
		if req.Target.Role == ChannelRoleManager && isHumanChannelOwner(req.Principal) {
			return allowMemberManagement()
		}
		if req.Target.Role != ChannelRoleMember {
			return denyMemberManagement(MemberManagementCodeTargetNotOrdinary)
		}
		if hasWorkspaceManagementAuthority(req.Principal.WorkspaceRole) ||
			req.Principal.ChannelRole == ChannelRoleOwner ||
			req.Principal.ChannelRole == ChannelRoleManager {
			return allowMemberManagement()
		}
		// LRM-869: inviter may remove an Agent they themselves added, even
		// without channel manager / workspace admin authority.
		if isInviterOfTargetAgent(req.Principal, *req.Target) {
			return allowMemberManagement()
		}
	case MemberManagementLeave:
		if !validMemberManagementTarget(req.Target) ||
			!sameMemberManagementIdentity(req.Principal, *req.Target) ||
			req.Target.Role != req.Principal.ChannelRole {
			return denyMemberManagement(MemberManagementCodeForbidden)
		}
		if req.Target.WouldBreakOwnerInvariant {
			return denyMemberManagement(MemberManagementCodeOwnerInvariant)
		}
		return allowMemberManagement()
	case MemberManagementPromoteManager:
		if !isHumanChannelOwner(req.Principal) {
			return denyMemberManagement(MemberManagementCodeForbidden)
		}
		if validMemberManagementTarget(req.Target) &&
			!sameMemberManagementIdentity(req.Principal, *req.Target) &&
			req.Target.Role == ChannelRoleMember {
			return allowMemberManagement()
		}
	case MemberManagementDemoteManager:
		if !isHumanChannelOwner(req.Principal) {
			return denyMemberManagement(MemberManagementCodeForbidden)
		}
		if validMemberManagementTarget(req.Target) &&
			!sameMemberManagementIdentity(req.Principal, *req.Target) &&
			req.Target.Role == ChannelRoleManager {
			return allowMemberManagement()
		}
	case MemberManagementTransferOwnership:
		if !isHumanChannelOwner(req.Principal) {
			return denyMemberManagement(MemberManagementCodeForbidden)
		}
		if !validMemberManagementTarget(req.Target) {
			return denyMemberManagement(MemberManagementCodeForbidden)
		}
		if req.Target.Kind == PrincipalKindAgent {
			return denyMemberManagement(MemberManagementCodeAgentCannotBeWorkspaceOwner)
		}
		if !sameMemberManagementIdentity(req.Principal, *req.Target) &&
			(req.Target.Role == ChannelRoleMember || req.Target.Role == ChannelRoleManager) {
			return allowMemberManagement()
		}
	}

	return denyMemberManagement(MemberManagementCodeForbidden)
}

func validPrincipalKind(kind PrincipalKind) bool {
	return kind == PrincipalKindUser || kind == PrincipalKindAgent
}

func validWorkspaceRole(role WorkspaceRole) bool {
	return role == WorkspaceRoleOwner || role == WorkspaceRoleAdmin || role == WorkspaceRoleMember
}

func validChannelRole(role ChannelRole) bool {
	return role == ChannelRoleOwner || role == ChannelRoleManager ||
		role == ChannelRoleMember || role == ChannelRoleNone
}

func hasWorkspaceManagementAuthority(role WorkspaceRole) bool {
	return role == WorkspaceRoleOwner || role == WorkspaceRoleAdmin
}

func validMemberManagementTarget(target *MemberManagementTarget) bool {
	return target != nil &&
		validPrincipalKind(target.Kind) &&
		validMemberManagementUUID(target.ID) &&
		validChannelRole(target.Role) &&
		target.Role != ChannelRoleNone
}

func validMemberManagementUUID(id pgtype.UUID) bool {
	return id.Valid && id.Bytes != [16]byte{}
}

func sameMemberManagementIdentity(principal MemberManagementPrincipal, target MemberManagementTarget) bool {
	return principal.Kind == target.Kind && principal.ID.Bytes == target.ID.Bytes
}

func isHumanChannelOwner(principal MemberManagementPrincipal) bool {
	return principal.Kind == PrincipalKindUser && principal.ChannelRole == ChannelRoleOwner
}

func isInviterOfTargetAgent(principal MemberManagementPrincipal, target MemberManagementTarget) bool {
	return target.Kind == PrincipalKindAgent &&
		validPrincipalKind(target.AddedByKind) &&
		validMemberManagementUUID(target.AddedByID) &&
		principal.Kind == target.AddedByKind &&
		principal.ID.Bytes == target.AddedByID.Bytes
}
