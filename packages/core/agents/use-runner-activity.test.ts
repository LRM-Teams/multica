import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { RunnerActivityResponse, RunnerActivitySummariesResponse } from "../types";
import { runnerActivityKeys, runnerActivitySummaryKeys } from "./queries";
import { applyRunnerActivityRealtime } from "./runner-activity-updaters";

const current: RunnerActivityResponse = {
  summary: { label: "Running command...", activityKind: "working", detailKind: "running_command" },
  timeline: [{ id: "entry-1", occurred_at: "2026-08-06T00:00:00Z", title: "Running command", activity_kind: "working", detail_kind: "running_command", body_kind: "command", body: "git status" }],
};

describe("applyRunnerActivityRealtime", () => {
  it("replaces the canonical cache without invalidating it", () => {
    const client = new QueryClient();
    const key = runnerActivityKeys.all("workspace-1", "agent-1");
    client.setQueryData(key, { summary: null, timeline: [] });
    applyRunnerActivityRealtime(client, "workspace-1", { agent_id: "agent-1", activity: current });
    expect(client.getQueryData(key)).toEqual(current);
    expect(client.getQueryState(key)?.isInvalidated).toBe(false);
  });

  it("patches an existing shared summary and does not seed an incomplete cache", () => {
    const client = new QueryClient();
    const key = runnerActivitySummaryKeys.all("workspace-1");
    const summaries: RunnerActivitySummariesResponse = { items: [
      { agent_id: "agent-1", summary: { label: "Online", activityKind: "online", detailKind: "idle" } },
      { agent_id: "agent-2", summary: { label: "Thinking...", activityKind: "thinking", detailKind: "thinking_started" } },
    ] };
    client.setQueryData(key, summaries);
    applyRunnerActivityRealtime(client, "workspace-1", { agent_id: "agent-1", activity: current });
    applyRunnerActivityRealtime(client, "workspace-2", { agent_id: "agent-1", activity: current });
    expect(client.getQueryData<RunnerActivitySummariesResponse>(key)?.items[0]?.summary).toEqual(current.summary);
    expect(client.getQueryData(runnerActivitySummaryKeys.all("workspace-2"))).toBeUndefined();
  });

  it("ignores malformed projections", () => {
    const client = new QueryClient();
    const key = runnerActivityKeys.all("workspace-1", "agent-1");
    client.setQueryData(key, current);
    applyRunnerActivityRealtime(client, "workspace-1", { agent_id: "agent-1", activity: { timeline: [{ id: 123 }] } });
    expect(client.getQueryData(key)).toEqual(current);
  });
});
