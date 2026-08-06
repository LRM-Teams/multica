import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  AGENT_XP_BURST_MERGE_MS,
  useAgentXpBurstStore,
} from "./xp-burst-store";

describe("useAgentXpBurstStore", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useAgentXpBurstStore.setState({ bursts: {} });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("merges rapid updates within 800ms into one burst", () => {
    const ingest = useAgentXpBurstStore.getState().ingestMemoryUpdate;
    ingest("agent-1", "memory", 1);
    ingest("agent-1", "state", 1);
    expect(useAgentXpBurstStore.getState().bursts["agent-1"]).toBeUndefined();

    vi.advanceTimersByTime(AGENT_XP_BURST_MERGE_MS);
    const burst = useAgentXpBurstStore.getState().bursts["agent-1"];
    expect(burst?.delta).toBe(2);
    expect(burst?.fileKey).toBe("state");
    expect(burst?.burstKey).toBe(1);
  });

  it("starts a new burst key after a quiet window", () => {
    const ingest = useAgentXpBurstStore.getState().ingestMemoryUpdate;
    ingest("agent-1", "memory", 1);
    vi.advanceTimersByTime(AGENT_XP_BURST_MERGE_MS);
    ingest("agent-1", "memory", 1);
    vi.advanceTimersByTime(AGENT_XP_BURST_MERGE_MS);
    expect(useAgentXpBurstStore.getState().bursts["agent-1"]?.burstKey).toBe(2);
  });
});
