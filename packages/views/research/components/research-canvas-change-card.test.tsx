// @vitest-environment happy-dom
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  ResearchCanvasChangeCard,
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
            ops: {
              goal_modified: "Goal version impact",
              integration_formed: "Results merged",
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
});
