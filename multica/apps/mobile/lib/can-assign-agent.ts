/**
 * Mobile-owned mirror of the boolean shim
 * `packages/views/issues/components/pickers/assignee-picker.tsx:canAssignAgent`
 * — which in turn forwards to `packages/core/permissions/rules.ts:canAssignAgentToIssue`.
 *
 * We mirror (not import) per the apps/mobile/CLAUDE.md sharing rule: only
 * `import type` from @multica/core; logic is duplicated to keep mobile
 * independent. Any rule change must be applied here too.
 *
 * Rule (mirrors backend after agent.visibility retirement):
 *   - Any workspace member may assign any agent visible in the workspace list
 *
 * Used by the chat agent picker to filter "agents I can talk to" and by
 * NoAgentBanner to detect the all-zero state.
 */
import type { Agent } from "@multica/core/types";

type MemberRoleLike = "owner" | "admin" | "member" | null | undefined;

export function canAssignAgent(
  _agent: Agent,
  userId: string | undefined | null,
  memberRole: MemberRoleLike,
): boolean {
  if (!userId) return false;
  return memberRole === "owner" || memberRole === "admin" || memberRole === "member";
}
