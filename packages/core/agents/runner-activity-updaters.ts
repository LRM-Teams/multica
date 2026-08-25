import type { QueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { RunnerActivityResponseSchema } from "../api/schemas";
import { createLogger } from "../logger";
import type { RunnerActivitySummariesResponse } from "../types";
import { runnerActivityKeys, runnerActivitySummaryKeys } from "./queries";

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
  const receivedAt = Date.now();
  const activity = parsed.data.activity.timing
    ? {
        ...parsed.data.activity,
        timing: {
          ...parsed.data.activity.timing,
          frontend_received_at_ms: receivedAt,
          frontend_cached_at_ms: Date.now(),
        },
      }
    : parsed.data.activity;
  queryClient.setQueryData(
    runnerActivityKeys.all(wsId, parsed.data.agent_id),
    activity,
  );
  queryClient.setQueryData<RunnerActivitySummariesResponse | undefined>(
    runnerActivitySummaryKeys.all(wsId),
    (current) => {
      if (!current) return current;

      const nextSummary = activity.summary;
      const existingIndex = current.items.findIndex(
        (item) => item.agent_id === parsed.data.agent_id,
      );
      if (!nextSummary) {
        if (existingIndex < 0) return current;
        return {
          items: current.items.filter((item) => item.agent_id !== parsed.data.agent_id),
        };
      }

      const nextItem = { agent_id: parsed.data.agent_id, summary: nextSummary };
      if (existingIndex < 0) {
        return { items: [...current.items, nextItem] };
      }
      return {
        items: current.items.map((item, index) =>
          index === existingIndex ? nextItem : item,
        ),
      };
    },
  );
}
