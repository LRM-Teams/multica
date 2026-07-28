package handler

import "testing"

func TestDecideMemberManagementMatrix(t *testing.T) {
	user := func(workspaceRole WorkspaceRole, channelRole ChannelRole) MemberManagementPrincipal {
		return MemberManagementPrincipal{
			Kind:          PrincipalKindUser,
			WorkspaceRole: workspaceRole,
			ChannelRole:   channelRole,
		}
	}
	agent := func(workspaceRole WorkspaceRole, channelRole ChannelRole) MemberManagementPrincipal {
		return MemberManagementPrincipal{
			Kind:          PrincipalKindAgent,
			WorkspaceRole: workspaceRole,
			ChannelRole:   channelRole,
		}
	}
	ordinary := &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleMember}
	manager := &MemberManagementTarget{Kind: PrincipalKindAgent, Role: ChannelRoleManager}
	owner := &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleOwner}
	self := func(kind PrincipalKind, role ChannelRole) *MemberManagementTarget {
		return &MemberManagementTarget{Kind: kind, Role: role, IsSelf: true}
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
			name: "self remove is not leave", principal: user(WorkspaceRoleAdmin, ChannelRoleManager),
			action:  MemberManagementRemoveMember,
			target:  &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleMember, IsSelf: true},
			visible: true, writable: true, code: MemberManagementCodeForbidden,
		},
		{
			name: "manager cannot remove peer manager", principal: user(WorkspaceRoleMember, ChannelRoleManager),
			action: MemberManagementRemoveMember, target: manager, visible: true, writable: true,
			code: MemberManagementCodeTargetNotOrdinary,
		},
		{
			name: "workspace owner override cannot remove channel owner", principal: user(WorkspaceRoleOwner, ChannelRoleNone),
			action: MemberManagementRemoveMember, target: owner, visible: true, writable: true,
			code: MemberManagementCodeTargetNotOrdinary,
		},
		{
			name: "ordinary member may leave", principal: user(WorkspaceRoleMember, ChannelRoleMember),
			action: MemberManagementLeave, target: self(PrincipalKindUser, ChannelRoleMember),
			visible: true, writable: true, allowed: true,
		},
		{
			name: "agent manager may leave", principal: agent(WorkspaceRoleMember, ChannelRoleManager),
			action: MemberManagementLeave, target: self(PrincipalKindAgent, ChannelRoleManager),
			visible: true, writable: true, allowed: true,
		},
		{
			name: "nonmember admin cannot leave", principal: user(WorkspaceRoleAdmin, ChannelRoleNone),
			action: MemberManagementLeave, target: self(PrincipalKindUser, ChannelRoleNone),
			visible: true, writable: true,
			code: MemberManagementCodeForbidden,
		},
		{
			name: "sole human owner cannot leave", principal: user(WorkspaceRoleOwner, ChannelRoleOwner),
			action: MemberManagementLeave,
			target: &MemberManagementTarget{
				Kind: PrincipalKindUser, Role: ChannelRoleOwner, IsSelf: true,
				WouldBreakOwnerInvariant: true,
			},
			visible: true, writable: true, code: MemberManagementCodeOwnerInvariant,
		},
		{
			name: "human owner may leave when owner invariant survives", principal: user(WorkspaceRoleOwner, ChannelRoleOwner),
			action: MemberManagementLeave, target: self(PrincipalKindUser, ChannelRoleOwner),
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
			target:  &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleManager},
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
			target:  &MemberManagementTarget{Kind: PrincipalKindAgent, Role: ChannelRoleMember},
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
	validPrincipal := MemberManagementPrincipal{
		Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleMember, ChannelRole: ChannelRoleMember,
	}
	validTarget := &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleMember}
	tests := []struct {
		name string
		req  MemberManagementRequest
	}{
		{
			name: "unknown principal kind",
			req: MemberManagementRequest{
				Principal:       MemberManagementPrincipal{Kind: "system", WorkspaceRole: WorkspaceRoleMember, ChannelRole: ChannelRoleMember},
				Action:          MemberManagementAddMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "unknown workspace role",
			req: MemberManagementRequest{
				Principal:       MemberManagementPrincipal{Kind: PrincipalKindUser, WorkspaceRole: "manager", ChannelRole: ChannelRoleMember},
				Action:          MemberManagementAddMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "unknown channel role",
			req: MemberManagementRequest{
				Principal:       MemberManagementPrincipal{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleMember, ChannelRole: "admin"},
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
			name: "remove without target",
			req: MemberManagementRequest{
				Principal:       MemberManagementPrincipal{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone},
				Action:          MemberManagementRemoveMember,
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "remove target with unknown kind",
			req: MemberManagementRequest{
				Principal:       MemberManagementPrincipal{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone},
				Action:          MemberManagementRemoveMember,
				Target:          &MemberManagementTarget{Kind: "system", Role: ChannelRoleMember},
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
				Target:          &MemberManagementTarget{Kind: PrincipalKindAgent, Role: ChannelRoleMember, IsSelf: true},
				ChannelVisible:  true,
				ChannelWritable: true,
			},
		},
		{
			name: "leave self role must match principal",
			req: MemberManagementRequest{
				Principal:       validPrincipal,
				Action:          MemberManagementLeave,
				Target:          &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleManager, IsSelf: true},
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

func TestDecideMemberManagementArchivedChannelDeniesEveryActor(t *testing.T) {
	actors := []MemberManagementPrincipal{
		{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleMember, ChannelRole: ChannelRoleMember},
		{Kind: PrincipalKindAgent, WorkspaceRole: WorkspaceRoleMember, ChannelRole: ChannelRoleManager},
		{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleMember, ChannelRole: ChannelRoleOwner},
		{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone},
		{Kind: PrincipalKindAgent, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone},
		{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleOwner, ChannelRole: ChannelRoleNone},
	}
	for _, principal := range actors {
		for _, action := range []MemberManagementAction{
			MemberManagementAddMember,
			MemberManagementRemoveMember,
		} {
			got := DecideMemberManagement(MemberManagementRequest{
				Principal:       principal,
				Action:          action,
				Target:          &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleMember},
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
		{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleMember, ChannelRole: ChannelRoleManager},
		{Kind: PrincipalKindAgent, WorkspaceRole: WorkspaceRoleMember, ChannelRole: ChannelRoleManager},
		{Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone},
		{Kind: PrincipalKindAgent, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone},
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
					Target:          &MemberManagementTarget{Kind: targetKind, Role: targetRole},
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
			Principal: MemberManagementPrincipal{
				Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleMember, ChannelRole: role,
			},
			Action:         MemberManagementRemoveMember,
			Target:         &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleMember},
			ChannelVisible: true, ChannelWritable: true,
		})
		agent := DecideMemberManagement(MemberManagementRequest{
			Principal: MemberManagementPrincipal{
				Kind: PrincipalKindAgent, WorkspaceRole: WorkspaceRoleMember, ChannelRole: role,
			},
			Action:         MemberManagementRemoveMember,
			Target:         &MemberManagementTarget{Kind: PrincipalKindUser, Role: ChannelRoleMember},
			ChannelVisible: true, ChannelWritable: true,
		})
		if human != agent {
			t.Fatalf("channel role %q diverged by principal kind: human=%+v agent=%+v", role, human, agent)
		}
	}

	humanAdmin := DecideMemberManagement(MemberManagementRequest{
		Principal: MemberManagementPrincipal{
			Kind: PrincipalKindUser, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone,
		},
		Action:         MemberManagementRemoveMember,
		Target:         &MemberManagementTarget{Kind: PrincipalKindAgent, Role: ChannelRoleMember},
		ChannelVisible: true, ChannelWritable: true,
	})
	agentAdmin := DecideMemberManagement(MemberManagementRequest{
		Principal: MemberManagementPrincipal{
			Kind: PrincipalKindAgent, WorkspaceRole: WorkspaceRoleAdmin, ChannelRole: ChannelRoleNone,
		},
		Action:         MemberManagementRemoveMember,
		Target:         &MemberManagementTarget{Kind: PrincipalKindAgent, Role: ChannelRoleMember},
		ChannelVisible: true, ChannelWritable: true,
	})
	if humanAdmin != agentAdmin {
		t.Fatalf("workspace admins diverged by principal kind: human=%+v agent=%+v", humanAdmin, agentAdmin)
	}
}
