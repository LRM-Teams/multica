import { describe, expect, it } from "vitest";
import { groupComposerAgentActivityRows, selectComposerAgentActivityRows } from "./composer-agent-activity-rows";

describe("composer activity fact projection", () => {
  it("shows and sorts lifecycle facts without server visual fields", () => {
    const rows = selectComposerAgentActivityRows(
      [{ agentId: "a", name: "A" }, { agentId: "b", name: "B" }],
      [
        { agent_id: "a", summary: { label: "Running command...", activityKind: "working", detailKind: "running_command" } },
        { agent_id: "b", summary: { label: "Thinking...", activityKind: "thinking", detailKind: "thinking_started" } },
      ],
    );
    expect(rows.map((row) => row.agentId)).toEqual(["b", "a"]);
    expect(rows.map((row) => row.dotClass)).toEqual(["bg-blue-500", "bg-dot-working"]);
  });

  it("hides presence-only facts and groups matching verbs", () => {
    expect(selectComposerAgentActivityRows([{ agentId: "a", name: "A" }], [
      { agent_id: "a", summary: { label: "Online", activityKind: "online", detailKind: "idle" } },
    ])).toEqual([]);
    const grouped = groupComposerAgentActivityRows([
      { agentId: "a", name: "A", label: "Thinking...", dotClass: "bg-blue-500", rank: 0 },
      { agentId: "b", name: "B", label: "Thinking...", dotClass: "bg-blue-500", rank: 0 },
    ]);
    expect(grouped.lines[0]?.names).toEqual(["A", "B"]);
  });

  it("keeps roster members only and omits the unambiguous DM name", () => {
    const rows = selectComposerAgentActivityRows(
      [{ agentId: "a", name: "Collector" }],
      [
        { agent_id: "a", summary: { label: "Thinking...", activityKind: "thinking", detailKind: "thinking_started" } },
        { agent_id: "stranger", summary: { label: "Running command...", activityKind: "working", detailKind: "running_command" } },
      ],
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ agentId: "a", name: "", label: "Thinking..." });
  });

  it("returns empty for missing inputs and generic Working copy", () => {
    const agents = [{ agentId: "a", name: "A" }];
    expect(selectComposerAgentActivityRows([], [])).toEqual([]);
    expect(selectComposerAgentActivityRows(agents, undefined)).toEqual([]);
    expect(selectComposerAgentActivityRows(agents, [
      { agent_id: "a", summary: { label: "Working", activityKind: "working", detailKind: "generic" } },
    ])).toEqual([]);
  });

  it("caps grouped lines and counts hidden agents", () => {
    const grouped = groupComposerAgentActivityRows([
      { agentId: "a", name: "A", label: "Thinking...", dotClass: "bg-blue-500", rank: 0 },
      { agentId: "b", name: "B", label: "Running command...", dotClass: "bg-dot-working", rank: 1 },
      { agentId: "c", name: "C", label: "Reading history...", dotClass: "bg-dot-working", rank: 2 },
      { agentId: "d", name: "D", label: "Reading history...", dotClass: "bg-dot-working", rank: 2 },
    ]);
    expect(grouped.lines.map((line) => line.label)).toEqual(["Thinking...", "Running command..."]);
    expect(grouped.hiddenAgentCount).toBe(2);
  });

  it("caps names and normalizes ellipsis variants into one verb", () => {
    const grouped = groupComposerAgentActivityRows([
      { agentId: "a", name: "A", label: "Thinking…", dotClass: "bg-blue-500", rank: 0 },
      { agentId: "b", name: "B", label: "Thinking...", dotClass: "bg-blue-500", rank: 0 },
      { agentId: "c", name: "C", label: "Thinking...", dotClass: "bg-blue-500", rank: 0 },
    ]);
    expect(grouped.lines).toHaveLength(1);
    expect(grouped.lines[0]).toMatchObject({ names: ["A", "B"], hiddenNameCount: 1 });
  });
});
