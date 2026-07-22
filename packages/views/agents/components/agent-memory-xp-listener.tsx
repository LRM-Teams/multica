"use client";

import { useCallback, useEffect } from "react";
import { useWSEvent } from "@multica/core/realtime";
import type { AgentMemoryUpdatedPayload } from "@multica/core/types";
import { useAgentXpBurstStore } from "@multica/core/agents/stores";

declare global {
  interface Window {
    /** Dev-only: `__multicaSimulateAgentMemoryXp(agentId, fileKey?, delta?)` */
    __multicaSimulateAgentMemoryXp?: (
      agentId: string,
      fileKey?: string,
      delta?: number,
    ) => void;
  }
}

/**
 * Subscribes to `agent:memory_updated` and feeds the shared XP burst store.
 * Mount once under the workspace shell (DashboardLayout).
 */
export function AgentMemoryXpListener() {
  const ingest = useAgentXpBurstStore((s) => s.ingestMemoryUpdate);

  const handleMemoryUpdated = useCallback(
    (payload: unknown) => {
      const data = payload as AgentMemoryUpdatedPayload;
      if (!data?.agent_id || !data.file_key) return;
      ingest(data.agent_id, data.file_key, data.count ?? 1);
    },
    [ingest],
  );

  useWSEvent("agent.memory_updated", handleMemoryUpdated);

  useEffect(() => {
    if (process.env.NODE_ENV === "production") return;
    window.__multicaSimulateAgentMemoryXp = (agentId, fileKey = "memory", delta = 1) => {
      ingest(agentId, fileKey, delta);
    };
    return () => {
      delete window.__multicaSimulateAgentMemoryXp;
    };
  }, [ingest]);

  return null;
}
