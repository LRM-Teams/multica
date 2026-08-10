import type { QueryClient } from "@tanstack/react-query";
import { z } from "zod";
import type { AgentPresence } from "../types";
import { createLogger } from "../logger";
import { agentPresenceKeys } from "./agent-presence";

const logger = createLogger("agent-presence.ws");

const AgentPresenceRealtimePayloadSchema = z.object({
  agent_id: z.string().min(1),
  presence: z.enum(["online", "offline"]),
});

export function applyAgentPresenceRealtime(
  queryClient: QueryClient,
  wsId: string | undefined,
  payload: unknown,
): void {
  if (!wsId) return;
  const parsed = AgentPresenceRealtimePayloadSchema.safeParse(payload);
  if (!parsed.success) {
    logger.warn("ignored malformed agent:presence payload", parsed.error.issues);
    return;
  }
  queryClient.setQueryData<ReadonlyMap<string, AgentPresence>>(
    agentPresenceKeys.workspace(wsId),
    (current) => {
      if (!current) return current;
      // The REST snapshot is the roster authority. A stale WS client from a
      // previous Workspace must not insert an unknown Agent into this cache.
      // Roster lifecycle events reconcile the snapshot separately.
      if (!current.has(parsed.data.agent_id)) return current;
      if (current.get(parsed.data.agent_id) === parsed.data.presence) return current;
      const next = new Map(current);
      next.set(parsed.data.agent_id, parsed.data.presence);
      return next;
    },
  );
}
