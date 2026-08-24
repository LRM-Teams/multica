// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  groupComposerAgentActivityRows,
  selectComposerAgentActivityRows,
} from "./composer-agent-activity-rows";

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
      item("agent-run", "Command activity", "running"),
      item("agent-think", "Thinking...", "active"),
    ]);

    expect(rows.map((row) => row.agentId)).toEqual(["agent-think", "agent-run"]);
    expect(rows[0]).toMatchObject({
      name: "Thinker",
      label: "Thinking...",
      dotClass: "bg-brand",
    });
    expect(rows[1]).toMatchObject({
      name: "Runner",
      label: "Command activity",
      dotClass: "bg-running",
    });
    expect(rows.some((row) => /Online|Idle/.test(row.label))).toBe(false);
  });

  it("hides Online/Idle on 1:1 as well — presence belongs on the avatar", () => {
    const rows = selectComposerAgentActivityRows(
      [{ agentId: "agent-online", name: "OnlineBot" }],
      [item("agent-online", "Online", "success")],
    );
    expect(rows).toEqual([]);
  });

  it("keeps a 1:1 live verb so collecting is not mistaken for idle", () => {
    const rows = selectComposerAgentActivityRows(
      [{ agentId: "agent-think", name: "Collector" }],
      [item("agent-think", "Thinking...", "active")],
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]?.label).toBe("Thinking...");
  });

  it("drops names in a single-agent conversation — the peer is unambiguous", () => {
    const rows = selectComposerAgentActivityRows(
      [{ agentId: "agent-think", name: "Collector" }],
      [item("agent-think", "Thinking...", "active")],
    );

    expect(rows[0]?.name).toBe("");
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

  it("does not infer the running tone from display copy", () => {
    const rows = selectComposerAgentActivityRows(
      [{ agentId: "agent-run", name: "Runner" }],
      [item("agent-run", "Running command...", "warning")],
    );

    expect(rows[0]?.dotClass).toBe("bg-amber-500");
  });
});

describe("groupComposerAgentActivityRows", () => {
  function row(agentId: string, name: string, label: string, tone = "active") {
    const dotClass = tone === "active" ? "bg-brand" : "bg-running";
    return { agentId, name, label, dotClass, tone };
  }

  it("merges same-verb agents onto one line", () => {
    const { lines, hiddenAgentCount } = groupComposerAgentActivityRows([
      row("a", "里维", "Thinking..."),
      row("b", "leo", "Running command...", "info"),
      row("c", "owen", "Running command...", "info"),
    ]);

    expect(hiddenAgentCount).toBe(0);
    expect(lines).toEqual([
      {
        key: "Thinking",
        label: "Thinking...",
        dotClass: "bg-brand",
        names: ["里维"],
        hiddenNameCount: 0,
      },
      {
        key: "Running command",
        label: "Running command...",
        dotClass: "bg-running",
        names: ["leo", "owen"],
        hiddenNameCount: 0,
      },
    ]);
  });

  it("caps lines and reports how many agents the tail hides", () => {
    const { lines, hiddenAgentCount } = groupComposerAgentActivityRows([
      row("a", "leo", "Running command..."),
      row("b", "里维", "Thinking..."),
      row("c", "dante", "Reading history..."),
      row("d", "kevin", "Checking messages..."),
      row("e", "gpt", "Checking messages..."),
    ]);

    // Same tone everywhere, so the verb the most agents share leads.
    expect(lines.map((line) => line.label)).toEqual([
      "Checking messages...",
      "Reading history...",
    ]);
    expect(hiddenAgentCount).toBe(2);
  });

  it("puts the liveliest tone first regardless of line size", () => {
    const { lines } = groupComposerAgentActivityRows([
      row("a", "里维", "Thinking..."),
      row("b", "leo", "Running command...", "info"),
      row("c", "owen", "Running command...", "info"),
    ]);

    expect(lines.map((line) => line.label)).toEqual([
      "Thinking...",
      "Running command...",
    ]);
  });

  it("caps names inside one line", () => {
    const { lines } = groupComposerAgentActivityRows([
      row("a", "leo", "Thinking..."),
      row("b", "owen", "Thinking..."),
      row("c", "kevin", "Thinking..."),
      row("d", "dante", "Thinking..."),
    ]);

    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatchObject({
      names: ["leo", "owen"],
      hiddenNameCount: 2,
    });
  });

  it("treats trailing ellipsis variants as the same verb", () => {
    const { lines } = groupComposerAgentActivityRows([
      row("a", "leo", "Thinking…"),
      row("b", "owen", "Thinking..."),
    ]);

    expect(lines).toHaveLength(1);
    expect(lines[0]?.names).toEqual(["leo", "owen"]);
  });

  it("returns nothing for an empty row list", () => {
    expect(groupComposerAgentActivityRows([])).toEqual({
      lines: [],
      hiddenAgentCount: 0,
    });
  });
});
