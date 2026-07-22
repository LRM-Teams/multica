"use client";

import { useCallback, useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useWSEvent } from "@multica/core/realtime";
import { agentDetailKeys, memberProfileKeys } from "@multica/core/agents";
import { useAgentXpBurstStore } from "@multica/core/agents/stores";
import { useWorkspaceId } from "@multica/core/hooks";
import type { AgentMemoryUpdatedPayload } from "@multica/core/types";

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
 * Also refreshes GetAgent / member-profile caches so LRM-304 Memory growth
 * stays current on open cards (no toast / broadcast).
 * Mount once under the workspace shell (DashboardLayout).
 */
export function AgentMemoryXpListener() {
  const ingest = useAgentXpBurstStore((s) => s.ingestMemoryUpdate);
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  const handleMemoryUpdated = useCallback(
    (payload: unknown) => {
      const data = payload as AgentMemoryUpdatedPayload;
      if (!data?.agent_id || !data.file_key) return;
      ingest(data.agent_id, data.file_key, data.count ?? 1);
      if (wsId) {
        void qc.invalidateQueries({
          queryKey: agentDetailKeys.detail(wsId, data.agent_id),
        });
        void qc.invalidateQueries({
          queryKey: memberProfileKeys.detail(wsId, "agent", data.agent_id),
        });
      }
    },
    [ingest, qc, wsId],
  );

  useWSEvent("agent.memory_updated", handleMemoryUpdated);

  useEffect(() => {
    if (process.env.NODE_ENV === "production") return;
    window.__multicaSimulateAgentMemoryXp = (agentId, fileKey = "memory", delta = 1) => {
      ingest(agentId, fileKey, delta);
      if (wsId) {
        void qc.invalidateQueries({
          queryKey: agentDetailKeys.detail(wsId, agentId),
        });
        void qc.invalidateQueries({
          queryKey: memberProfileKeys.detail(wsId, "agent", agentId),
        });
      }
    };
    return () => {
      delete window.__multicaSimulateAgentMemoryXp;
    };
  }, [ingest, qc, wsId]);

  return null;
}
