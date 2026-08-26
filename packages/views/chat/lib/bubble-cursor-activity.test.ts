// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  activeBubbleStepSummary,
  bubbleToolSummary,
  classifyBubbleToolKind,
  deriveBubbleCursorPanels,
  extractTodoItems,
  friendlyBubbleToolLabel,
} from "./bubble-cursor-activity";
import type { ChatTimelineItem } from "@multica/core/chat";

describe("classifyBubbleToolKind", () => {
  it("classifies todo / task / plan tools", () => {
    expect(classifyBubbleToolKind("todo_write")).toBe("todo");
    expect(classifyBubbleToolKind("TodoWrite")).toBe("todo");
    expect(classifyBubbleToolKind("task")).toBe("task");
    expect(classifyBubbleToolKind("Task")).toBe("task");
    expect(classifyBubbleToolKind("create_plan")).toBe("plan");
    expect(classifyBubbleToolKind("read_file")).toBe("other");
  });
});

describe("extractTodoItems", () => {
  it("parses Cursor-style todos array", () => {
    expect(
      extractTodoItems({
        todos: [
          { id: "1", content: "Wire token bar", status: "completed" },
          { id: "2", content: "Add plan card", status: "in_progress" },
        ],
      }),
    ).toEqual([
      { id: "1", content: "Wire token bar", status: "completed" },
      { id: "2", content: "Add plan card", status: "in_progress" },
    ]);
  });
});

describe("deriveBubbleCursorPanels", () => {
  it("builds plan, todos, and subagent tree from tool_use rows", () => {
    const items: ChatTimelineItem[] = [
      {
        seq: 1,
        type: "tool_use",
        tool: "create_plan",
        input: { name: "Bubble UX", plan: "1. tokens\n2. tools\n3. cards" },
      },
      {
        seq: 2,
        type: "tool_use",
        tool: "todo_write",
        input: {
          todos: [
            { id: "a", content: "Token bar", status: "completed" },
            { id: "b", content: "Todo card", status: "pending" },
          ],
        },
      },
      {
        seq: 3,
        type: "tool_use",
        tool: "task",
        input: { description: "Explore daemon usage", prompt: "Find Usage mapping" },
      },
      {
        seq: 4,
        type: "tool_result",
        tool: "task",
        output: "done",
      },
    ];
    const panels = deriveBubbleCursorPanels(items);
    expect(panels.plan).toEqual({
      title: "Bubble UX",
      body: "1. tokens\n2. tools\n3. cards",
    });
    expect(panels.todos).toHaveLength(2);
    expect(panels.subagents).toHaveLength(1);
    expect(panels.subagents[0]?.status).toBe("done");
    expect(panels.subagents[0]?.title).toBe("Explore daemon usage");
  });
});

describe("friendlyBubbleToolLabel / activeBubbleStepSummary", () => {
  it("labels cursor file tools", () => {
    expect(friendlyBubbleToolLabel("read_file")).toBe("Read");
    expect(friendlyBubbleToolLabel("edit_file")).toBe("Edit");
  });

  it("summarizes the latest active step", () => {
    const items: ChatTimelineItem[] = [
      { seq: 1, type: "thinking", content: "..." },
      {
        seq: 2,
        type: "tool_use",
        tool: "read_file",
        input: { path: "/a/b/c/daemon.go" },
      },
    ];
    expect(activeBubbleStepSummary(items)).toBe("Read · .../c/daemon.go");
  });

  it("summarizes keepalive errors as connection lost", () => {
    expect(
      activeBubbleStepSummary([
        {
          seq: 1,
          type: "error",
          content: "Error: RetriableError: [internal] HTTP/2 keepalive ping timed out after 5000ms",
        },
      ]),
    ).toBe("Connection lost");
  });

  it("summarizes shell cmd / target_file aliases", () => {
    expect(
      bubbleToolSummary({
        seq: 1,
        type: "tool_use",
        tool: "shell",
        input: { cmd: "pnpm test" },
      }),
    ).toBe("pnpm test");
    expect(
      bubbleToolSummary({
        seq: 2,
        type: "tool_use",
        tool: "read_file",
        input: { target_file: "/x/y/z/note.ts" },
      }),
    ).toBe(".../z/note.ts");
  });
});
