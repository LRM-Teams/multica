import type { MemberRole } from "@multica/core/types";

export const editableWorkspaceRoles = ["admin", "member"] as const satisfies readonly MemberRole[];

export function workspaceMemberActions({
  canManage,
  isSelf,
  targetRole,
}: {
  canManage: boolean;
  isSelf: boolean;
  targetRole: MemberRole;
}): { canEditRole: boolean; canRemove: boolean } {
  const allowed = canManage && !isSelf && targetRole !== "owner";
  return { canEditRole: allowed, canRemove: allowed };
}
