// @vitest-environment happy-dom
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  ResearchCanvasChangeCard,
  canvasChangeTargetNodeIds,
  isCanvasChangeProcessMessage,
} from "./research-canvas-change-card";
import type { ResearchMessage } from "@multica/core/types";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, vars?: Record<string, unknown>) => {
      const dict = {
        d5: {
          change_receipt: {
            title: "Canvas update · {{label}}",
            input_count: "Merged {{count}} input nodes",
            conclusion_count: "{{count}} conclusions",
            graph_version: "Graph v{{version}}",
            show_on_canvas: "Show on constellation",
            ops: {
              goal_modified: "Goal version impact",
              integration_formed: "Results merged",
              task_restarted: "Task restarted",
            },
          },
        },
      };
      const raw = fn(dict);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
  }),
}));

function processMessage(op: string): ResearchMessage {
  return {
    id: "m1",
    session_id: "s1",
    sender_type: "system",
    sender_id: null,
    target_agent_id: null,
    card_kind: "process",
    body: "detail body",
    meta: { op, title: "Merged conclusion" },
    created_at: "",
  };
}

describe("ResearchCanvasChangeCard (Slice F)", () => {
  it("recognizes goal_modified process receipts", () => {
    const message = processMessage("goal_modified");
    expect(isCanvasChangeProcessMessage(message)).toBe(true);
    render(<ResearchCanvasChangeCard message={message} />);
    expect(screen.getByTestId("research-canvas-change-card")).toHaveAttribute(
      "data-canvas-change-op",
      "goal_modified",
    );
    expect(screen.getByText("Canvas update · Goal version impact")).toBeTruthy();
  });

  it("maps the production graph_merge operation and renders committed facts", () => {
    const message = processMessage("graph_merge");
    message.body = "Merged two independently verified results";
    message.meta = {
      op: "graph_merge",
      title: "Combined finding",
      input_node_ids: ["n1", "n2"],
      conclusion_count: 4,
      graph_version: 9,
    };

    expect(isCanvasChangeProcessMessage(message)).toBe(true);
    render(<ResearchCanvasChangeCard message={message} />);

    expect(screen.getByTestId("research-canvas-change-card")).toHaveAttribute(
      "data-canvas-change-kind",
      "integration_formed",
    );
    expect(screen.getByText("Canvas update · Results merged")).toBeTruthy();
    expect(screen.getByText("Merged 2 input nodes")).toBeTruthy();
    expect(screen.getByText("4 conclusions")).toBeTruthy();
    expect(screen.getByText("Graph v9")).toBeTruthy();
  });

  it("maps the production retry command without treating unrelated process cards as changes", () => {
    expect(isCanvasChangeProcessMessage(processMessage("node_command_retry"))).toBe(true);
    expect(isCanvasChangeProcessMessage(processMessage("source_upsert"))).toBe(false);
  });

  it("focuses the server-declared merge result before its input nodes", async () => {
    const user = userEvent.setup();
    const onFocusNode = vi.fn();
    const message = processMessage("graph_merge");
    message.meta = {
      op: "graph_merge",
      node_id: "result-node",
      input_node_ids: ["input-a", "input-b", "input-a"],
    };

    expect(canvasChangeTargetNodeIds(message)).toEqual([
      "result-node",
      "input-a",
      "input-b",
    ]);
    render(
      <ResearchCanvasChangeCard message={message} onFocusNode={onFocusNode} />,
    );

    await user.click(screen.getByRole("button", { name: "Show on constellation" }));
    expect(onFocusNode).toHaveBeenCalledWith("result-node");
  });

  it("does not invent a canvas target when the process metadata has no node id", () => {
    const onFocusNode = vi.fn();
    const message = processMessage("goal_modified");

    expect(canvasChangeTargetNodeIds(message)).toEqual([]);
    render(
      <ResearchCanvasChangeCard message={message} onFocusNode={onFocusNode} />,
    );
    expect(screen.queryByRole("button", { name: "Show on constellation" })).toBeNull();
  });
});
