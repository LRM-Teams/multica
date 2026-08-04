/**
 * LRM-1348 harness stub — real module minus the slug/workspace-list derivation,
 * which throws outside a workspace route and is irrelevant to focus behaviour.
 */
export * from "../../../packages/core/hooks";

export function useWorkspaceId(): string {
  return "ws-harness";
}
