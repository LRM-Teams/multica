// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TaskMessagePayload } from "@multica/core/types/events";
import { appendTimelineItem, buildTimeline, coalesceTimelineItems, type TimelineItem } from "./build-timeline";

function message(seq: number, type: TaskMessagePayload["type"], content?: string): TaskMessagePayload {
  return {
    task_id: "task-1",
    issue_id: "issue-1",
    seq,
    type,
    content,
  };
}

describe("task transcript timeline", () => {
  it("merges adjacent text and thinking fragments split by streaming flushes", () => {
    const items = buildTimeline([
      message(2, "text", "world"),
      message(1, "text", "hello "),
      message(3, "thinking", "step "),
      message(4, "thinking", "one"),
    ]);

    expect(items).toEqual([
      expect.objectContaining({ seq: 1, type: "text", content: "hello world" }),
      expect.objectContaining({ seq: 3, type: "thinking", content: "step one" }),
    ]);
  });

  it("keeps an empty thinking phase wire out of the transcript", () => {
    const items = buildTimeline([
      message(1, "tool_use"),
      message(2, "thinking"),
      message(3, "text", "answer"),
    ]);

    expect(items).toEqual([
      expect.objectContaining({ seq: 1, type: "tool_use" }),
      expect.objectContaining({ seq: 3, type: "text", content: "answer" }),
    ]);
  });

  it("does not merge across tool or error boundaries", () => {
    const items = coalesceTimelineItems([
      { seq: 1, type: "text", content: "before" },
      { seq: 2, type: "tool_use", tool: "bash" },
      { seq: 3, type: "text", content: "after" },
      { seq: 4, type: "error", content: "failed" },
      { seq: 5, type: "text", content: "done" },
    ]);

    expect(items.map((item) => item.content ?? item.tool)).toEqual([
      "before",
      "bash",
      "after",
      "failed",
      "done",
    ]);
  });

  it("coalesces newly appended live text with the previous text item", () => {
    const existing: TimelineItem[] = [{ seq: 1, type: "text", content: "hello" }];
    const items = appendTimelineItem(existing, { seq: 2, type: "text", content: " world" });

    expect(items).toEqual([
      expect.objectContaining({ seq: 1, type: "text", content: "hello world" }),
    ]);
  });

  it("coalesces out-of-order raw text by sequence", () => {
    const existing: TimelineItem[] = [
      { seq: 1, type: "text", content: "A" },
      { seq: 3, type: "text", content: "C" },
    ];
    const items = appendTimelineItem(existing, { seq: 2, type: "text", content: "B" });

    expect(items).toEqual([
      expect.objectContaining({ seq: 1, type: "text", content: "ABC" }),
    ]);
  });

  it("redacts secrets after adjacent chunks are coalesced", () => {
    const items = buildTimeline([
      message(1, "text", "Authorization: Bearer abc123xyz."),
      message(2, "text", "def456"),
    ]);

    expect(items[0]?.content).toBe("Authorization: Bearer [REDACTED]");
    expect(items[0]?.content).not.toContain("abc123xyz");
    expect(items[0]?.content).not.toContain("def456");
  });

  it("keeps the latest created_at when coalescing streaming fragments", () => {
    const items = coalesceTimelineItems([
      { seq: 1, type: "text", content: "hello ", created_at: "2026-06-09T09:00:00.000Z" },
      { seq: 2, type: "text", content: "world", created_at: "2026-06-09T09:00:05.000Z" },
    ]);

    expect(items).toEqual([
      expect.objectContaining({
        seq: 1,
        type: "text",
        content: "hello world",
        created_at: "2026-06-09T09:00:05.000Z",
      }),
    ]);
  });

  it("falls back to the previous created_at when the merged fragment has none", () => {
    const items = coalesceTimelineItems([
      { seq: 1, type: "text", content: "hello ", created_at: "2026-06-09T09:00:00.000Z" },
      { seq: 2, type: "text", content: "world" },
    ]);

    expect(items[0]?.created_at).toBe("2026-06-09T09:00:00.000Z");
  });

  it("leaves transcript narrative projection to the UI helpers", () => {
    const items = buildTimeline([
      {
        task_id: "task-1",
        issue_id: "issue-1",
        seq: 1,
        type: "tool_use",
        tool: "exec_command",
        input: { cmd: "/bin/zsh -lc secret" },
      },
    ]);

    expect(items).toEqual([
      expect.objectContaining({
        seq: 1,
        type: "tool_use",
        tool: "exec_command",
        input: { cmd: "/bin/zsh -lc secret" },
      }),
    ]);
  });

  it("backfills empty tool_use.input from a later tool_result.input (LRM-689)", () => {
    const items = buildTimeline([
      {
        task_id: "task-1",
        issue_id: "issue-1",
        seq: 1,
        type: "tool_use",
        tool: "shell",
      },
      {
        task_id: "task-1",
        issue_id: "issue-1",
        seq: 2,
        type: "tool_result",
        tool: "shell",
        input: { command: "pwd" },
        output: "/tmp\n",
      },
    ]);

    expect(items[0]).toEqual(
      expect.objectContaining({
        seq: 1,
        type: "tool_use",
        tool: "shell",
        input: { command: "pwd" },
      }),
    );
    expect(items[1]).toEqual(
      expect.objectContaining({
        seq: 2,
        type: "tool_result",
        tool: "shell",
        input: { command: "pwd" },
        output: "/tmp\n",
      }),
    );
  });

  it("does not overwrite a non-empty tool_use.input during backfill", () => {
    const items = buildTimeline([
      {
        task_id: "task-1",
        issue_id: "issue-1",
        seq: 1,
        type: "tool_use",
        tool: "shell",
        input: { command: "echo started" },
      },
      {
        task_id: "task-1",
        issue_id: "issue-1",
        seq: 2,
        type: "tool_result",
        tool: "shell",
        input: { command: "echo completed" },
        output: "ok",
      },
    ]);

    expect(items[0]?.input).toEqual({ command: "echo started" });
  });
});
