import { describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { RunnerActivityResponse, RunnerActivitySummariesResponse } from "../types";
import { runnerActivityKeys, runnerActivitySummaryKeys } from "./queries";
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
  it("records WS receive and cache marks without changing provider timing", () => {
    vi.spyOn(Date, "now").mockReturnValueOnce(200).mockReturnValueOnce(203);
    const queryClient = new QueryClient();
    applyRunnerActivityRealtime(queryClient, "workspace-1", {
      agent_id: "agent-1",
      activity: {
        summary: null,
        timeline: [],
        timing: { accepted_at_ms: 100, first_acp_update_at_ms: 150, daemon_sent_at_ms: 180 },
      },
    });
    expect(queryClient.getQueryData<RunnerActivityResponse>(runnerActivityKeys.all("workspace-1", "agent-1"))?.timing).toEqual({
      accepted_at_ms: 100,
      first_acp_update_at_ms: 150,
      daemon_sent_at_ms: 180,
      frontend_received_at_ms: 200,
      frontend_cached_at_ms: 203,
    });
    vi.restoreAllMocks();
  });

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
    const summaryKey = runnerActivitySummaryKeys.all("workspace-1");
    const summaries: RunnerActivitySummariesResponse = {
      items: [{ agent_id: "agent-1", summary: current.summary! }],
    };
    queryClient.setQueryData(agentOneKey, current);
    queryClient.setQueryData(summaryKey, summaries);

    applyRunnerActivityRealtime(queryClient, "workspace-1", { agent_id: "agent-2", activity: current });
    const summariesBeforeMalformed = queryClient.getQueryData(summaryKey);
    applyRunnerActivityRealtime(queryClient, "workspace-1", {
      agent_id: "agent-1",
      activity: { summary: null, timeline: [{ id: 123 }] },
    });

    expect(queryClient.getQueryData(agentTwoKey)).toEqual(current);
    expect(queryClient.getQueryData(agentOneKey)).toEqual(current);
    expect(queryClient.getQueryData(summaryKey)).toEqual(summariesBeforeMalformed);
    expect(queryClient.getQueryState(agentOneKey)?.isInvalidated).toBe(false);
  });

  it("patches an existing Workspace summary projection without seeding an incomplete one", () => {
    const queryClient = new QueryClient();
    const summaryKey = runnerActivitySummaryKeys.all("workspace-1");
    const summaries: RunnerActivitySummariesResponse = {
      items: [
        { agent_id: "agent-1", summary: { label: "Online", tone: "success", visibility: "visible" } },
        { agent_id: "agent-2", summary: { label: "Thinking...", tone: "info", visibility: "visible" } },
      ],
    };
    queryClient.setQueryData(summaryKey, summaries);

    applyRunnerActivityRealtime(queryClient, "workspace-1", {
      agent_id: "agent-1",
      activity: current,
    });
    applyRunnerActivityRealtime(queryClient, "workspace-2", {
      agent_id: "agent-1",
      activity: current,
    });

    expect(queryClient.getQueryData<RunnerActivitySummariesResponse>(summaryKey)).toEqual({
      items: [
        { agent_id: "agent-1", summary: current.summary },
        { agent_id: "agent-2", summary: { label: "Thinking...", tone: "info", visibility: "visible" } },
      ],
    });
    expect(queryClient.getQueryData(runnerActivitySummaryKeys.all("workspace-2"))).toBeUndefined();
  });
});
