/**
 * LRM-1366 harness stub — honors/roles come from the network-backed actor
 * directory; DM row names come from `/api/dm` itself, so the directory is not
 * part of the skeleton/empty-region paint path.
 */
export * from "../../../packages/core/workspace/hooks";

export function useActorName() {
  return {
    getActorName: (_type: string, id: string) => id,
    getActorInitials: (_type: string, id: string) => id.slice(0, 1),
    getActorAvatarUrl: () => null,
    getMemberName: (id: string) => id,
    getMemberHandle: () => null,
    getMemberRole: () => null,
    getMemberHonor: () => undefined,
    getAgentFleetRank: () => undefined,
  };
}
