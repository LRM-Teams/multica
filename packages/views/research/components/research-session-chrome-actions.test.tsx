import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ResearchSession } from "@multica/core/types";
import { ResearchSessionChromeActions } from "./research-session-chrome-actions";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) => {
      return fn({
        status: {
          running: "Running",
          completed: "Completed",
        },
        panel: {
          view_delivery: "View delivery",
          handoff_title: "Handoff delivery",
          handoff_project: "Create development project",
          handoff_channel: "Create development channel",
          handoff: "Handoff",
          handoff_submitting: "Handing off…",
        },
      });
    },
  }),
}));

function makeSession(overrides: Partial<ResearchSession> = {}): ResearchSession {
  return {
    id: "s1",
    workspace_id: "w1",
    fleet_id: "f1",
    created_by: "u1",
    title: "知春路沿线房产市场深度调研",
    goal: "分析知春路沿线 3 公里二手房挂牌与成交",
    status: "completed",
    current_stage: "s4_delivery",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T00:00:00Z",
    ...overrides,
  };
}

describe("ResearchSessionChromeActions", () => {
  it("omits the unused tools menu and pins status after delivery", () => {
    render(
      <ResearchSessionChromeActions
        session={makeSession()}
        canConfirm={false}
        canHandoff
        createProject
        createChannel
        onCreateProjectChange={() => {}}
        onCreateChannelChange={() => {}}
        onConfirm={() => {}}
        onHandoff={() => {}}
        onOpenDelivery={() => {}}
        showStatus
      />,
    );

    expect(screen.queryByTestId("research-session-tools")).toBeNull();
    const delivery = screen.getByTestId("research-session-delivery");
    const status = screen.getByTestId("research-session-status");
    expect(status).toHaveTextContent("Completed");
    expect(
      delivery.compareDocumentPosition(status) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});
