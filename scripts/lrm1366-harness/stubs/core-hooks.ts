/**
 * LRM-1366 harness stub — the real hook derives the workspace from the route
 * slug + workspace list query, neither of which exists outside a workspace
 * route. Nothing in the DM sidebar paint path depends on the real id.
 */
export * from "../../../packages/core/hooks";

export function useWorkspaceId(): string {
  return "ws-harness";
}
