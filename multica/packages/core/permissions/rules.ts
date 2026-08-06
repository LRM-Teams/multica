import type {
  Agent,
  Comment,
  Member,
  MemberRole,
  RuntimeDevice,
  Skill,
} from "../types";
import { ALLOW, deny, type Decision, type PermissionContext } from "./types";

/**
 * Pure permission rules — single source of truth that mirrors the Go backend
 * gates in `server/internal/handler/`. Hooks in `use-resource-permissions.ts`
 * are thin wrappers that pull `PermissionContext` from auth + member queries
 * and forward to these.
 *
 * Returning a `Decision` (not a boolean) lets every surface — disabled state,
 * tooltip, banner copy — read the same `reason` and stay consistent without
 * sprinkling copy through the view layer.
 */

const isAdminLike = (role: MemberRole | null) =>
  role === "owner" || role === "admin";

// ---- Agents ----------------------------------------------------------------

/**
 * Update / archive / restore agent fields. The backend gates archive and
 * restore identically to edit (`server/internal/handler/agent.go:519-535`),
 * so callers can use `canEditAgent` for all three.
 */
export function canEditAgent(agent: Agent, ctx: PermissionContext): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to edit this agent.");
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  if (agent.owner_id !== null && agent.owner_id === ctx.userId) return ALLOW;
  return deny(
    "not_resource_owner",
    "Only the agent owner and workspace admins can edit this agent.",
  );
}

/**
 * Read an agent's sensitive tabs — Activity, Reminders, Files, Usage.
 *
 * One rule for all of them, deliberately: three copies of the same rule is
 * three chances to miss one on the next change. Frank, 2026-07-30: "Activity
 * 还是要根据 workspace 的 role 来的，admin 可以看到，普通成员，只能看到自己
 * agent 的 activity" and then "其余几个 tab 同理，我只是说了 activity".
 *
 * Note there is deliberately no visibility term. Agent visibility was retired
 * (same day) and it never belonged here anyway: whether other people may read
 * an agent's activity is a property of the *viewer's* authority, not of how
 * discoverable the agent is.
 *
 * This gate decides **whether a tab is shown**, not whether data may be read.
 * The server re-checks admin-or-owner on every request, so getting this wrong
 * shows a tab that 403s — it cannot leak anything.
 */
export function canViewAgentSensitiveTabs(
  agent: Agent,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to view this agent's activity.");
  }
  if (agent.owner_id !== null && agent.owner_id === ctx.userId) return ALLOW;
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_resource_owner",
    "Only the agent owner and workspace admins can view this agent's activity.",
  );
}

/**
 * Assign an agent to an issue. Any workspace member may assign any agent.
 *
 * ⚠️ This is a widening, and it is not covered by Frank's 2026-07-30
 * statements — those retire *visibility*. Raised explicitly rather than let it
 * happen incidentally; see the note in the PR description.
 *
 * Why this and not admin-or-owner: retiring visibility collapses the two former
 * branches (workspace/channel → any member, private → owner+admin) into one,
 * and there is no "leave it as it was" option because the old split *was* the
 * visibility split. Of the two, "every member can see the agent but may not give
 * it work" is the stranger outcome. Choosing admin-or-owner would instead be a
 * *tightening* — members can currently assign workspace-visibility agents.
 *
 * Membership is still required: a non-member has no business assigning work.
 */
export function canAssignAgentToIssue(
  _agent: Agent,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to assign agents.");
  }
  if (ctx.role === null) {
    return deny("not_member", "Join this workspace to assign agents.");
  }
  return ALLOW;
}

// ---- Skills ----------------------------------------------------------------

export function canEditSkill(skill: Skill, ctx: PermissionContext): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to edit this skill.");
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  if (skill.created_by !== null && skill.created_by === ctx.userId) {
    return ALLOW;
  }
  return deny(
    "not_resource_owner",
    "Only the creator and workspace admins can edit this skill.",
  );
}

export function canDeleteSkill(skill: Skill, ctx: PermissionContext): Decision {
  return canEditSkill(skill, ctx);
}

// ---- Comments --------------------------------------------------------------

export function canEditComment(
  comment: Comment,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to edit comments.");
  }
  // Only member-authored comments can be edited; agent-authored comments are
  // immutable from any human's perspective.
  if (comment.author_type !== "member") {
    return deny(
      "not_resource_owner",
      "Agent-authored comments cannot be edited.",
    );
  }
  if (comment.author_id === ctx.userId) return ALLOW;
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_resource_owner",
    "Only the author and workspace admins can edit this comment.",
  );
}

export function canDeleteComment(
  comment: Comment,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to delete comments.");
  }
  if (comment.author_type === "member" && comment.author_id === ctx.userId) {
    return ALLOW;
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_resource_owner",
    "Only the author and workspace admins can delete this comment.",
  );
}

// ---- Runtimes --------------------------------------------------------------

export function canDeleteRuntime(
  runtime: RuntimeDevice,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to delete runtimes.");
  }
  if (runtime.owner_id !== null && runtime.owner_id === ctx.userId) {
    return ALLOW;
  }
  return deny(
    "not_resource_owner",
    "Only the runtime owner can delete this runtime.",
  );
}

// ---- Workspace -------------------------------------------------------------

export function canUpdateWorkspaceSettings(ctx: PermissionContext): Decision {
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_admin_role",
    "Only workspace owners and admins can update workspace settings.",
  );
}

export function canDeleteWorkspace(ctx: PermissionContext): Decision {
  if (ctx.role === "owner") return ALLOW;
  return deny(
    "not_owner_role",
    "Only the workspace owner can delete this workspace.",
  );
}

export function canManageMembers(ctx: PermissionContext): Decision {
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_admin_role",
    "Only workspace owners and admins can manage members.",
  );
}

/**
 * Change an agent's workspace role (`member` | `admin`). Frank 2026-08-01:
 * workspace owner AND admin. Backend historically owner-only
 * (`UpdateAgentWorkspaceRole`); FE gates on this rule so the control is
 * hidden for members — Vera expands the server gate to match.
 */
export function canChangeAgentWorkspaceRole(ctx: PermissionContext): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to change an agent role.");
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_admin_role",
    "Only workspace owners and admins can change an agent role.",
  );
}

/**
 * Encodes the role-change matrix from `workspace.go:458-530`:
 *   - admins cannot touch the owner role (neither demote owners nor promote)
 *   - the last owner cannot be demoted
 *   - non-managers cannot change roles at all
 *
 * `ownerCount` is the number of workspace members currently with role=owner.
 * Caller derives it locally from the cached member list.
 */
export function canChangeMemberRole(
  target: Pick<Member, "role">,
  ownerCount: number,
  ctx: PermissionContext,
): Decision {
  const manage = canManageMembers(ctx);
  if (!manage.allowed) return manage;

  if (target.role === "owner") {
    if (ctx.role !== "owner") {
      return deny(
        "not_owner_role",
        "Only the workspace owner can change another owner's role.",
      );
    }
    if (ownerCount <= 1) {
      return deny(
        "last_owner",
        "Promote another member to owner first — a workspace must keep at least one owner.",
      );
    }
  }
  return ALLOW;
}
