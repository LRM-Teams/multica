package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func memberManagementTestUUID(seed byte) pgtype.UUID {
	var bytes [16]byte
	bytes[15] = seed
	return pgtype.UUID{Bytes: bytes, Valid: true}
}

func memberManagementTestPrincipal(
	kind PrincipalKind,
	workspaceRole WorkspaceRole,
	channelRole ChannelRole,
	idSeed byte,
) MemberManagementPrincipal {
	return MemberManagementPrincipal{
		Kind:          kind,
		ID:            memberManagementTestUUID(idSeed),
		WorkspaceID:   memberManagementTestUUID(100),
		WorkspaceRole: workspaceRole,
		ChannelRole:   channelRole,
	}
}

func TestDecideMemberManagementMatrix(t *testing.T) {
	user := func(workspaceRole WorkspaceRole, channelRole ChannelRole) MemberManagementPrincipal {
		return memberManagementTestPrincipal(PrincipalKindUser, workspaceRole, channelRole, 1)
	}
	agent := func(workspaceRole WorkspaceRole, channelRole ChannelRole) MemberManagementPrincipal {
		return memberManagementTestPrincipal(PrincipalKindAgent, workspaceRole, channelRole, 2)
	}
	ordinary := &MemberManagementTarget{Kind: PrincipalKindUser, ID: memberManagementTestUUID(3), Role: ChannelRoleMember}
	manager := &MemberManagementTarget{Kind: PrincipalKindAgent, ID: memberManagementTestUUID(4), Role: ChannelRoleManager}
	owner := &MemberManagementTarget{Kind: PrincipalKindUser, ID: memberManagementTestUUID(5), Role: ChannelRoleOwner}
	self := func(principal MemberManagementPrincipal) *MemberManagementTarget {
		return &MemberManagementTarget{Kind: principal.Kind, ID: principal.ID, Role: principal.ChannelRole}
	}

	tests := []struct {
		name      string
		principal MemberManagementPrincipal
		action    MemberManagementAction
		target    *MemberManagementTarget
		visible   bool
		writable  bool
		allowed   bool
		code      string
	}{
		{
			name: "invisible channel fails before actor authority", principal: user(WorkspaceRoleOwner, ChannelRoleNone),
			action: MemberManagementAddMember, visible: false, writable: true,
			code: MemberManagementCodeChannelNotVisible,
		},
		{
			name: "archived channel is not writable", principal: user(WorkspaceRoleOwner, ChannelRoleNone),
			action: MemberManagementAddMember, visible: true, writable: false,
			code: MemberManagementCodeChannelNotWritable,
		},
		{
			name: "ordinary human channel member may add", principal: user(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementAddMember, visible: true, writable: true, allowed: true,
		},
		{
			name: "ordinary agent channel member may add", principal: agent(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementAddMember, visible: true, writable: true, allowed: true,
		},
		{
			name: "ordinary workspace member outside channel may not add", principal: user(WorkspaceRoleMember, ChannelRoleNone),
			action: MemberManagementAddMember, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "human workspace admin outside channel may add", principal: user(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementAddMember, visible: true, writable: true, allowed: true,
		},
		{
			name: "agent workspace admin outside channel may add", principal: agent(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementAddMember, visible: true, writable: true, allowed: true,
		},
		{
			name: "channel manager removes ordinary human", principal: user(WorkspaceRoleMember, ChannelRoleManager),
			action: MemberManagementRemoveMember, target: ordinary, visible: true, writable: true, allowed: true,
		},
		{
			name: "agent manager removes ordinary human", principal: agent(WorkspaceRoleMember, ChannelRoleManager),
			action: MemberManagementRemoveMember, target: ordinary, visible: true, writable: true, allowed: true,
		},
		{
			name: "workspace admin outside channel removes ordinary", principal: agent(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementRemoveMember, target: ordinary, visible: true, writable: true, allowed: true,
		},
		{
			name: "human channel owner removes ordinary", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementRemoveMember, target: ordinary, visible: true, writable: true, allowed: true,
		},
		{
			name: "ordinary member may not remove other ordinary", principal: user(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementRemoveMember, target: ordinary, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "inviter may remove self-added agent without channel manage", principal: user(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementRemoveMember,
			target: &MemberManagementTarget{
				Kind:        PrincipalKindAgent,
				ID:          memberManagementTestUUID(8),
				Role:        ChannelRoleMember,
				AddedByKind: PrincipalKindUser,
				AddedByID:   memberManagementTestUUID(1),
			},
			visible: true, writable: true, allowed: true,
		},
		{
			name: "non-inviter ordinary may not remove agent", principal: user(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementRemoveMember,
			target: &MemberManagementTarget{
				Kind:        PrincipalKindAgent,
				ID:          memberManagementTestUUID(8),
				Role:        ChannelRoleMember,
				AddedByKind: PrincipalKindUser,
				AddedByID:   memberManagementTestUUID(9),
			},
			visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "inviter exception does not cover ordinary human targets", principal: user(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementRemoveMember,
			target: &MemberManagementTarget{
				Kind:        PrincipalKindUser,
				ID:          memberManagementTestUUID(3),
				Role:        ChannelRoleMember,
				AddedByKind: PrincipalKindUser,
				AddedByID:   memberManagementTestUUID(1),
			},
			visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "self remove is not leave", principal: user(WorkspaceRoleAdmin, ChannelRoleManager),
			action:  MemberManagementRemoveMember,
			target:  &MemberManagementTarget{Kind: PrincipalKindUser, ID: memberManagementTestUUID(1), Role: ChannelRoleMember},
			visible: true, writable: true, code: MemberManagementCodeForbidden,
		},
		{
			name: "manager cannot remove peer manager", principal: user(WorkspaceRoleMember, ChannelRoleManager),
			action: MemberManagementRemoveMember, target: manager, visible: true, writable: true,
			code: MemberManagementCodeTargetNotOrdinary,
		},
		{
			name: "human channel owner removes manager", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementRemoveMember, target: manager, visible: true, writable: true, allowed: true,
		},
		{
			name: "workspace owner override cannot remove channel owner", principal: user(WorkspaceRoleOwner, ChannelRoleNone),
			action: MemberManagementRemoveMember, target: owner, visible: true, writable: true,
			code: MemberManagementCodeTargetNotOrdinary,
		},
		{
			name: "ordinary member may leave", principal: user(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementLeave, target: self(user(WorkspaceRoleMember, ChannelRoleMember)),
			visible: true, writable: true, allowed: true,
		},
		{
			name: "agent manager may leave", principal: agent(WorkspaceRoleMember, ChannelRoleManager),
			action: MemberManagementLeave, target: self(agent(WorkspaceRoleMember, ChannelRoleManager)),
			visible: true, writable: true, allowed: true,
		},
		{
			name: "nonmember admin cannot leave", principal: user(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementLeave,
			target: &MemberManagementTarget{
				Kind: PrincipalKindUser, ID: memberManagementTestUUID(1), Role: ChannelRoleNone,
			},
			visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "sole human owner cannot leave", principal: user(WorkspaceRoleOwner, ChannelRoleOwner),
			action: MemberManagementLeave,
			target: &MemberManagementTarget{
				Kind: PrincipalKindUser, ID: memberManagementTestUUID(1), Role: ChannelRoleOwner,
				WouldBreakOwnerInvariant: true,
			},
			visible: true, writable: true, code: MemberManagementCodeOwnerInvariant,
		},
		{
			name: "human owner may leave when owner invariant survives", principal: user(WorkspaceRoleOwner, ChannelRoleOwner),
			action: MemberManagementLeave, target: self(user(WorkspaceRoleOwner, ChannelRoleOwner)),
			visible: true, writable: true, allowed: true,
		},
		{
			name: "human channel owner may promote ordinary", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementPromoteManager, target: ordinary, visible: true, writable: true, allowed: true,
		},
		{
			name: "workspace owner outside channel may not promote", principal: user(WorkspaceRoleOwner, ChannelRoleNone),
			action: MemberManagementPromoteManager, target: ordinary, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "agent workspace admin outside channel may not promote", principal: agent(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementPromoteManager, target: ordinary, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "human channel owner may demote manager", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementDemoteManager, target: manager, visible: true, writable: true, allowed: true,
		},
		{
			name: "human workspace admin outside channel may not demote", principal: user(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementDemoteManager, target: manager, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "human channel owner cannot promote existing manager", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementPromoteManager, target: manager, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "human channel owner cannot demote ordinary member", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementDemoteManager, target: ordinary, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "human channel owner may transfer to human ordinary", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementTransferOwnership, target: ordinary, visible: true, writable: true, allowed: true,
		},
		{
			name: "human channel owner may transfer to human manager", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action:  MemberManagementTransferOwnership,
			target:  &MemberManagementTarget{Kind: PrincipalKindUser, ID: memberManagementTestUUID(6), Role: ChannelRoleManager},
			visible: true, writable: true, allowed: true,
		},
		{
			name: "workspace owner outside channel may not transfer", principal: user(WorkspaceRoleOwner, ChannelRoleNone),
			action: MemberManagementTransferOwnership, target: ordinary, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "agent workspace admin outside channel may not transfer", principal: agent(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementTransferOwnership, target: ordinary, visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "human channel owner may not transfer to agent", principal: user(WorkspaceRoleMember, ChannelRoleOwner),
			action:  MemberManagementTransferOwnership,
			target:  &MemberManagementTarget{Kind: PrincipalKindAgent, ID: memberManagementTestUUID(7), Role: ChannelRoleMember},
			visible: true, writable: true, code: MemberManagementCodeAgentCannotBeWorkspaceOwner,
		},
		{
			name: "agent cannot hold workspace owner", principal: agent(WorkspaceRoleOwner, ChannelRoleNone),
			action: MemberManagementAddMember, visible: true, writable: true,
			code: MemberManagementCodeAgentCannotBeWorkspaceOwner,
		},
		{
			name: "agent cannot hold channel owner", principal: agent(WorkspaceRoleMember, ChannelRoleOwner),
			action: MemberManagementAddMember, visible: true, writable: true,
			code: MemberManagementCodeAgentCannotBeWorkspaceOwner,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideMemberManagement(MemberManagementRequest{
				Principal:       tt.principal,
				Action:          tt.action,
				Target:          tt.target,
				ChannelVisible:  tt.visible,
				ChannelWritable: tt.writable,
			})
			if got.Allowed != tt.allowed || got.Code != tt.code {
				t.Fatalf("decision = %+v, want allowed=%v code=%q", got, tt.allowed, tt.code)
			}
		})
	}
}

func TestDecideMemberManagementFailsClosedOnMalformedState(t *testing.T) {
	validPrincipal := memberManagementTestPrincipal(
		PrincipalKindUser, WorkspaceRoleMember, ChannelRoleMember, 1,
	)
	validTarget := &MemberManagementTarget{
		Kind: PrincipalKindUser, ID: memberManagementTestUUID(3), Role: ChannelRoleMember,
	}
	tests := []struct {
		name string
		req  MemberManagementRequest
	}{
		{
			name: "unknown principal kind",
			req: MemberManagementRequest{
				Principal:       memberManagementTestPrincipal("system", WorkspaceRoleMember, ChannelRoleMember, 1),
				Action:          MemberManagementAddMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "unknown workspace role",
			req: MemberManagementRequest{
				Principal:       memberManagementTestPrincipal(PrincipalKindUser, "manager", ChannelRoleMember, 1),
				Action:          MemberManagementAddMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "unknown channel role",
			req: MemberManagementRequest{
				Principal:       memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleMember, "admin", 1),
				Action:          MemberManagementAddMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "unknown action",
			req: MemberManagementRequest{
				Principal:       validPrincipal,
				Action:          "archive",
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "zero principal id",
			req: MemberManagementRequest{
				Principal: MemberManagementPrincipal{
					Kind:          PrincipalKindUser,
					ID:            pgtype.UUID{Valid: true},
					WorkspaceID:   memberManagementTestUUID(100),
					WorkspaceRole: WorkspaceRoleMember,
					ChannelRole:   ChannelRoleMember,
				},
				Action:          MemberManagementAddMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "invalid workspace id",
			req: MemberManagementRequest{
				Principal: MemberManagementPrincipal{
					Kind:          PrincipalKindUser,
					ID:            memberManagementTestUUID(1),
					WorkspaceRole: WorkspaceRoleMember,
					ChannelRole:   ChannelRoleMember,
				},
				Action:          MemberManagementAddMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "remove without target",
			req: MemberManagementRequest{
				Principal:       memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleAdmin, ChannelRoleNone, 1),
				Action:          MemberManagementRemoveMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "remove target with zero id",
			req: MemberManagementRequest{
				Principal:       memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleAdmin, ChannelRoleNone, 1),
				Action:          MemberManagementRemoveMember,
				Target:          &MemberManagementTarget{Kind: PrincipalKindUser, ID: pgtype.UUID{Valid: true}, Role: ChannelRoleMember},
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "remove target with unknown kind",
			req: MemberManagementRequest{
				Principal:       memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleAdmin, ChannelRoleNone, 1),
				Action:          MemberManagementRemoveMember,
				Target:          &MemberManagementTarget{Kind: "system", ID: memberManagementTestUUID(3), Role: ChannelRoleMember},
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "leave without actor row",
			req: MemberManagementRequest{
				Principal:       validPrincipal,
				Action:          MemberManagementLeave,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "leave cannot target another row",
			req: MemberManagementRequest{
				Principal:       validPrincipal,
				Action:          MemberManagementLeave,
				Target:          validTarget,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "leave self kind must match principal",
			req: MemberManagementRequest{
				Principal:       validPrincipal,
				Action:          MemberManagementLeave,
				Target:          &MemberManagementTarget{Kind: PrincipalKindAgent, ID: validPrincipal.ID, Role: ChannelRoleMember},
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "leave self role must match principal",
			req: MemberManagementRequest{
				Principal:       validPrincipal,
				Action:          MemberManagementLeave,
				Target:          &MemberManagementTarget{Kind: PrincipalKindUser, ID: validPrincipal.ID, Role: ChannelRoleManager},
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideMemberManagement(tt.req)
			if got.Allowed || got.Code != MemberManagementCodeForbidden {
				t.Fatalf("decision = %+v, want denied code=%q", got, MemberManagementCodeForbidden)
			}
		})
	}
}

func TestDecideMemberManagementDerivesSelfFromCanonicalIdentity(t *testing.T) {
	principal := memberManagementTestPrincipal(
		PrincipalKindAgent, WorkspaceRoleMember, ChannelRoleManager, 2,
	)

	t.Run("same identity cannot use remove member", func(t *testing.T) {
		got := DecideMemberManagement(MemberManagementRequest{
			Principal: principal,
			Action:    MemberManagementRemoveMember,
			Target: &MemberManagementTarget{
				Kind: principal.Kind, ID: principal.ID, Role: ChannelRoleMember,
			},
			ChannelVisible:  true,
			ChannelWritable: true,
		})
		if got.Allowed || got.Code != MemberManagementCodeForbidden {
			t.Fatalf("decision = %+v, want denied code=%q", got, MemberManagementCodeForbidden)
		}
	})

	t.Run("different identity cannot use leave", func(t *testing.T) {
		got := DecideMemberManagement(MemberManagementRequest{
			Principal: principal,
			Action:    MemberManagementLeave,
			Target: &MemberManagementTarget{
				Kind: principal.Kind, ID: memberManagementTestUUID(3), Role: principal.ChannelRole,
			},
			ChannelVisible:  true,
			ChannelWritable: true,
		})
		if got.Allowed || got.Code != MemberManagementCodeForbidden {
			t.Fatalf("decision = %+v, want denied code=%q", got, MemberManagementCodeForbidden)
		}
	})
}

func TestDecideMemberManagementArchivedChannelDeniesEveryActor(t *testing.T) {
	actors := []MemberManagementPrincipal{
		memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleMember, ChannelRoleMember, 1),
		memberManagementTestPrincipal(PrincipalKindAgent, WorkspaceRoleMember, ChannelRoleManager, 2),
		memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleMember, ChannelRoleOwner, 1),
		memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleAdmin, ChannelRoleNone, 1),
		memberManagementTestPrincipal(PrincipalKindAgent, WorkspaceRoleAdmin, ChannelRoleNone, 2),
		memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleOwner, ChannelRoleNone, 1),
	}
	for _, principal := range actors {
		for _, action := range []MemberManagementAction{
			MemberManagementAddMember,
			MemberManagementRemoveMember,
		} {
			got := DecideMemberManagement(MemberManagementRequest{
				Principal:       principal,
				Action:          action,
				Target:          &MemberManagementTarget{Kind: PrincipalKindUser, ID: memberManagementTestUUID(3), Role: ChannelRoleMember},
				ChannelVisible:  true,
				ChannelWritable: false,
			})
			if got.Allowed || got.Code != MemberManagementCodeChannelNotWritable {
				t.Fatalf("principal=%+v action=%q decision=%+v, want denied code=%q",
					principal, action, got, MemberManagementCodeChannelNotWritable)
			}
		}
	}
}

func TestDecideMemberManagementTargetKindParity(t *testing.T) {
	for _, principal := range []MemberManagementPrincipal{
		memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleMember, ChannelRoleManager, 1),
		memberManagementTestPrincipal(PrincipalKindAgent, WorkspaceRoleMember, ChannelRoleManager, 2),
		memberManagementTestPrincipal(PrincipalKindUser, WorkspaceRoleAdmin, ChannelRoleNone, 1),
		memberManagementTestPrincipal(PrincipalKindAgent, WorkspaceRoleAdmin, ChannelRoleNone, 2),
	} {
		for _, targetRole := range []ChannelRole{
			ChannelRoleMember,
			ChannelRoleManager,
			ChannelRoleOwner,
		} {
			decisions := make([]MemberManagementDecision, 0, 2)
			for _, targetKind := range []PrincipalKind{PrincipalKindUser, PrincipalKindAgent} {
				decisions = append(decisions, DecideMemberManagement(MemberManagementRequest{
					Principal:       principal,
					Action:          MemberManagementRemoveMember,
					Target:          &MemberManagementTarget{Kind: targetKind, ID: memberManagementTestUUID(3), Role: targetRole},
					ChannelVisible:  true,
					ChannelWritable: true,
				}))
			}
			if decisions[0] != decisions[1] {
				t.Fatalf("principal=%+v target role=%q diverged by target kind: user=%+v agent=%+v",
					principal, targetRole, decisions[0], decisions[1])
			}
		}
	}
}

func TestDecideMemberManagementHumanAgentParity(t *testing.T) {
	for _, role := range []ChannelRole{ChannelRoleManager, ChannelRoleMember} {
		human := DecideMemberManagement(MemberManagementRequest{
			Principal: memberManagementTestPrincipal(
				PrincipalKindUser, WorkspaceRoleMember, role, 1,
			),
			Action:         MemberManagementRemoveMember,
			Target:         &MemberManagementTarget{Kind: PrincipalKindUser, ID: memberManagementTestUUID(3), Role: ChannelRoleMember},
			ChannelVisible: true, ChannelWritable: true,
		})
		agent := DecideMemberManagement(MemberManagementRequest{
			Principal: memberManagementTestPrincipal(
				PrincipalKindAgent, WorkspaceRoleMember, role, 2,
			),
			Action:         MemberManagementRemoveMember,
			Target:         &MemberManagementTarget{Kind: PrincipalKindUser, ID: memberManagementTestUUID(3), Role: ChannelRoleMember},
			ChannelVisible: true, ChannelWritable: true,
		})
		if human != agent {
			t.Fatalf("channel role %q diverged by principal kind: human=%+v agent=%+v", role, human, agent)
		}
	}

	humanAdmin := DecideMemberManagement(MemberManagementRequest{
		Principal: memberManagementTestPrincipal(
			PrincipalKindUser, WorkspaceRoleAdmin, ChannelRoleNone, 1,
		),
		Action:         MemberManagementRemoveMember,
		Target:         &MemberManagementTarget{Kind: PrincipalKindAgent, ID: memberManagementTestUUID(3), Role: ChannelRoleMember},
		ChannelVisible: true, ChannelWritable: true,
	})
	agentAdmin := DecideMemberManagement(MemberManagementRequest{
		Principal: memberManagementTestPrincipal(
			PrincipalKindAgent, WorkspaceRoleAdmin, ChannelRoleNone, 2,
		),
		Action:         MemberManagementRemoveMember,
		Target:         &MemberManagementTarget{Kind: PrincipalKindAgent, ID: memberManagementTestUUID(3), Role: ChannelRoleMember},
		ChannelVisible: true, ChannelWritable: true,
	})
	if humanAdmin != agentAdmin {
		t.Fatalf("workspace admins diverged by principal kind: human=%+v agent=%+v", humanAdmin, agentAdmin)
	}
}
