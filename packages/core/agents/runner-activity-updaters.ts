import type { QueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { RunnerActivityResponseSchema } from "../api/schemas";
import { createLogger } from "../logger";
import { runnerActivityKeys } from "./queries";

const logger = createLogger("runner-activity.ws");

const RunnerActivityRealtimePayloadSchema = z.object({
  agent_id: z.string().min(1),
  activity: RunnerActivityResponseSchema,
});

/**
 * Applies the complete server-projected Runner Activity payload to the Query
 * cache. Normal realtime delivery is authoritative and must not trigger a
 * redundant REST reconciliation; reconnect recovery owns that fallback.
 */
export function applyRunnerActivityRealtime(
  queryClient: QueryClient,
  wsId: string | undefined,
  payload: unknown,
): void {
  if (!wsId) return;
  const parsed = RunnerActivityRealtimePayloadSchema.safeParse(payload);
  if (!parsed.success) {
    logger.warn("ignored malformed agent:activity payload", parsed.error.issues);
    return;
  }
  queryClient.setQueryData(
    runnerActivityKeys.all(wsId, parsed.data.agent_id),
    parsed.data.activity,
  );
}
