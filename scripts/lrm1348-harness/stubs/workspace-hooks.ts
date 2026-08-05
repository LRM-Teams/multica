/**
 * LRM-1348 harness stub — real module minus the network-backed actor directory.
 * Names come from the roster the harness passes, so rows resolve without a
 * workspace member/agent list query.
 */
export * from "../../../packages/core/workspace/hooks";

const NAMES: Record<string, string> = {
  "agent-beckham": "贝克汉姆",
  "agent-wendy": "Wendy",
  "agent-nash": "Nash",
};

export function useActorName() {
  return {
    getActorName: (_type: string, id: string) => NAMES[id] ?? "Unknown Agent",
    getActorInitials: (_type: string, id: string) => (NAMES[id] ?? "?").slice(0, 1),
    getActorAvatarUrl: () => null,
    getMemberName: (id: string) => NAMES[id] ?? "Unknown",
    getMemberHandle: () => null,
    getMemberRole: () => null,
  };
}
