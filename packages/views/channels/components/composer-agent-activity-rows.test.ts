// @vitest-environment node
import { describe, expect, it } from "vitest";
import { selectComposerAgentActivityRows } from "./composer-agent-activity-rows";

function item(
  agentId: string,
  label: string,
  tone: string,
  visibility = "visible",
) {
  return { agent_id: agentId, summary: { label, tone, visibility } };
}

const agents = [
  { agentId: "agent-idle", name: "IdleBot" },
  { agentId: "agent-think", name: "Thinker" },
  { agentId: "agent-run", name: "Runner" },
  { agentId: "agent-online", name: "OnlineBot" },
];

describe("selectComposerAgentActivityRows", () => {
  it("keeps live verbs, sorts them first, and hides presence on a group roster", () => {
    const rows = selectComposerAgentActivityRows(agents, [
      item("agent-online", "Online", "success"),
      item("agent-idle", "Idle", "neutral"),
      item("agent-run", "Running command...", "info"),
      item("agent-think", "Thinking...", "active"),
    ]);

    expect(rows.map((row) => row.agentId)).toEqual(["agent-think", "agent-run"]);
    expect(rows[0]).toMatchObject({
      name: "Thinker",
      label: "Thinking...",
      dotClass: "bg-brand",
    });
    expect(rows.some((row) => /Online|Idle/.test(row.label))).toBe(false);
  });

  it("still allows a 1:1 Online cue so DM matches the existing strip", () => {
    const rows = selectComposerAgentActivityRows(
      [{ agentId: "agent-online", name: "OnlineBot" }],
      [item("agent-online", "Online", "success")],
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]?.label).toBe("Online");
  });

  it("drops Working, hidden, and agents with no observation", () => {
    const rows = selectComposerAgentActivityRows(agents, [
      item("agent-think", "Working", "active"),
      item("agent-run", "Running command...", "info", "hidden"),
      item("stranger", "Thinking...", "active"),
    ]);

    expect(rows).toEqual([]);
  });

  it("returns empty when the roster or summaries are empty", () => {
    expect(selectComposerAgentActivityRows([], [item("agent-think", "Thinking...", "active")])).toEqual([]);
    expect(selectComposerAgentActivityRows(agents, [])).toEqual([]);
    expect(selectComposerAgentActivityRows(agents, undefined)).toEqual([]);
  });
});
