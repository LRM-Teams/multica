import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { RunnerActivityResponse } from "../types";
import { runnerActivityKeys } from "./queries";
import { applyRunnerActivityRealtime } from "./runner-activity-updaters";

const current: RunnerActivityResponse = {
  summary: { label: "Running command...", tone: "info", visibility: "visible" },
  timeline: [{
    id: "entry-1",
    occurred_at: "2026-08-06T00:00:00Z",
    title: "Running command...",
    tone: "info",
    body_kind: "text",
    body: "Safe narrative",
  }],
};

describe("applyRunnerActivityRealtime", () => {
  it("replaces the canonical server projection without scheduling REST reconciliation", () => {
    const queryClient = new QueryClient();
    const key = runnerActivityKeys.all("workspace-1", "agent-1");
    queryClient.setQueryData(key, { summary: null, timeline: [] });

    applyRunnerActivityRealtime(queryClient, "workspace-1", {
      agent_id: "agent-1",
      activity: current,
    });

    expect(queryClient.getQueryData(key)).toEqual(current);
    expect(queryClient.getQueryState(key)?.isInvalidated).toBe(false);
  });

  it("uses the payload agent key and ignores malformed payloads", () => {
    const queryClient = new QueryClient();
    const agentOneKey = runnerActivityKeys.all("workspace-1", "agent-1");
    const agentTwoKey = runnerActivityKeys.all("workspace-1", "agent-2");
    queryClient.setQueryData(agentOneKey, current);

    applyRunnerActivityRealtime(queryClient, "workspace-1", { agent_id: "agent-2", activity: current });
    applyRunnerActivityRealtime(queryClient, "workspace-1", {
      agent_id: "agent-1",
      activity: { summary: null, timeline: [{ id: 123 }] },
    });

    expect(queryClient.getQueryData(agentTwoKey)).toEqual(current);
    expect(queryClient.getQueryData(agentOneKey)).toEqual(current);
    expect(queryClient.getQueryState(agentOneKey)?.isInvalidated).toBe(false);
  });
});
