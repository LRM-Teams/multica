"use client";

import { create } from "zustand";

/** Merge rapid memory writes before showing one burst (design Phase①). */
export const AGENT_XP_BURST_MERGE_MS = 800;
/** Ring + float label duration. */
export const AGENT_XP_BURST_DURATION_MS = 1200;

export interface AgentXpBurstSnapshot {
  burstKey: number;
  delta: number;
  fileKey: string;
}

interface PendingMerge {
  delta: number;
  fileKey: string;
}

interface AgentXpBurstState {
  bursts: Readonly<Record<string, AgentXpBurstSnapshot>>;
  ingestMemoryUpdate: (agentId: string, fileKey: string, delta: number) => void;
  /** Test-only: flush pending merge timers immediately. */
  __flushPendingForTests?: () => void;
}

const pendingByAgent = new Map<string, PendingMerge>();
const mergeTimers = new Map<string, ReturnType<typeof setTimeout>>();

function clearMergeTimer(agentId: string) {
  const timer = mergeTimers.get(agentId);
  if (timer) {
    clearTimeout(timer);
    mergeTimers.delete(agentId);
  }
}

function commitBurst(
  set: (fn: (state: AgentXpBurstState) => Partial<AgentXpBurstState>) => void,
  agentId: string,
  fileKey: string,
  delta: number,
) {
  if (delta <= 0) return;
  set((state) => {
    const prev = state.bursts[agentId];
    const burstKey = (prev?.burstKey ?? 0) + 1;
    return {
      bursts: {
        ...state.bursts,
        [agentId]: { burstKey, delta, fileKey },
      },
    };
  });
}

function scheduleMerge(
  set: (fn: (state: AgentXpBurstState) => Partial<AgentXpBurstState>) => void,
  agentId: string,
) {
  clearMergeTimer(agentId);
  mergeTimers.set(
    agentId,
    setTimeout(() => {
      mergeTimers.delete(agentId);
      const pending = pendingByAgent.get(agentId);
      pendingByAgent.delete(agentId);
      if (!pending) return;
      commitBurst(set, agentId, pending.fileKey, pending.delta);
    }, AGENT_XP_BURST_MERGE_MS),
  );
}

function flushAllPending(
  set: (fn: (state: AgentXpBurstState) => Partial<AgentXpBurstState>) => void,
) {
  for (const agentId of [...mergeTimers.keys()]) {
    clearMergeTimer(agentId);
    const pending = pendingByAgent.get(agentId);
    pendingByAgent.delete(agentId);
    if (pending) {
      commitBurst(set, agentId, pending.fileKey, pending.delta);
    }
  }
}

export const useAgentXpBurstStore = create<AgentXpBurstState>((set) => ({
  bursts: {},
  ingestMemoryUpdate: (agentId, fileKey, delta) => {
    const safeDelta = Number.isFinite(delta) && delta > 0 ? delta : 1;
    const key = fileKey.trim() || "memory";
    const pending = pendingByAgent.get(agentId);
    if (pending) {
      pending.delta += safeDelta;
      pending.fileKey = key;
    } else {
      pendingByAgent.set(agentId, { delta: safeDelta, fileKey: key });
    }
    scheduleMerge(set, agentId);
  },
  __flushPendingForTests: () => flushAllPending(set),
}));

/** Human-readable label for the floating burst chip. */
export function formatMemoryFileKeyLabel(fileKey: string): string {
  switch (fileKey.toLowerCase()) {
    case "memory":
      return "记忆";
    case "user":
      return "用户";
    case "state":
      return "状态";
    case "channel_context":
      return "频道";
    case "decisions":
      return "决策";
    case "project_memory":
      return "项目";
    default:
      return fileKey.replace(/_/g, " ").toUpperCase();
  }
}
