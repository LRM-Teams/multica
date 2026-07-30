import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchSession } from "@multica/core/types";
import { ResearchSessionChrome } from "./research-session-chrome";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        status: {
          running: "Running",
          awaiting_user_confirm: "Awaiting confirm",
          completed: "Completed",
          paused: "Paused",
        },
        stage: {
          s1_plan: "S1 · Plan",
          s2_sources: "S2 · Explore",
          s3_validation: "S3 · Validate",
          s4_delivery: "S4 · Deliver",
        },
        panel: {
          confirm_continue: "Confirm & continue",
          handoff_title: "Handoff delivery",
          view_delivery: "View delivery",
          handoff_project: "Create development project",
          handoff_channel: "Create development channel",
          handoff: "Handoff",
        },
      }),
  }),
}));

// Real base-ui Popover does not render in jsdom; mirror its open/trigger
// contract so the chrome behavior is what gets tested.
vi.mock("@multica/ui/components/ui/popover", async () => {
  const React = await import("react");
  const Ctx = React.createContext<{
    open: boolean;
    onOpenChange?: (open: boolean) => void;
  }>({ open: false });
  return {
    Popover: ({
      open,
      onOpenChange,
      children,
    }: {
      open?: boolean;
      onOpenChange?: (open: boolean) => void;
      children?: React.ReactNode;
    }) => (
      <Ctx.Provider value={{ open: open ?? false, onOpenChange }}>{children}</Ctx.Provider>
    ),
    PopoverTrigger: ({
      render: renderProp,
      children,
    }: {
      render?: React.ReactElement;
      children?: React.ReactNode;
    }) => {
      const { open, onOpenChange } = React.useContext(Ctx);
      return React.cloneElement(renderProp ?? <button type="button" />, {
        onClick: () => onOpenChange?.(!open),
      } as Record<string, unknown>, children);
    },
    PopoverContent: ({ children }: { children?: React.ReactNode }) => {
      const { open } = React.useContext(Ctx);
      return open ? <div data-testid="popover-content">{children}</div> : null;
    },
    PopoverHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    PopoverTitle: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    PopoverDescription: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  };
});

function makeSession(overrides: Partial<ResearchSession> = {}): ResearchSession {
  return {
    id: "s1",
    workspace_id: "w1",
    fleet_id: "f1",
    created_by: "u1",
    title: "知春路沿线房产市场深度调研",
    goal: "分析知春路沿线 3 公里二手房挂牌与成交",
    status: "running",
    current_stage: "s2_sources",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T00:00:00Z",
    ...overrides,
  };
}

function renderChrome(session: ResearchSession, overrides: Record<string, unknown> = {}) {
  return render(
    <ResearchSessionChrome
      session={session}
      canConfirm={true}
      canHandoff={true}
      createProject={true}
      createChannel={true}
      onCreateProjectChange={() => {}}
      onCreateChannelChange={() => {}}
      onConfirm={() => {}}
      onHandoff={() => {}}
      onOpenDelivery={() => {}}
      {...overrides}
    />,
  );
}

describe("ResearchSessionChrome", () => {
  it("renders two rows: title + status dot + stage chip, goal line below", () => {
    renderChrome(makeSession());
    expect(screen.getByText("知春路沿线房产市场深度调研")).toBeTruthy();
    expect(screen.getByText("Running")).toBeTruthy();
    expect(screen.getAllByText("S2 · Explore").length).toBeGreaterThan(0);
    expect(screen.getByText("分析知春路沿线 3 公里二手房挂牌与成交")).toBeTruthy();
    const dot = document.querySelector(".bg-brand.animate-pulse");
    expect(dot).toBeTruthy();
  });

  it("running state shows no primary action, only the delivery outline", () => {
    renderChrome(makeSession({ status: "running" }));
    expect(screen.queryByText("Confirm & continue")).toBeNull();
    expect(screen.queryByText("Handoff delivery")).toBeNull();
    expect(screen.getByText("View delivery")).toBeTruthy();
  });

  it("awaiting_user_confirm shows exactly the confirm primary", () => {
    const onConfirm = vi.fn();
    renderChrome(makeSession({ status: "awaiting_user_confirm" }), { onConfirm });
    expect(screen.getByText("Awaiting confirm")).toBeTruthy();
    const confirm = screen.getByText("Confirm & continue");
    expect(screen.queryByText("Handoff delivery")).toBeNull();
    fireEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("completed shows handoff primary; checkboxes live inside the popover", () => {
    renderChrome(makeSession({ status: "completed" }));
    expect(screen.queryByText("Confirm & continue")).toBeNull();
    expect(screen.queryByText("Create development project")).toBeNull();

    fireEvent.click(screen.getByText("Handoff delivery"));
    expect(screen.getByText("Create development project")).toBeTruthy();
    expect(screen.getByText("Create development channel")).toBeTruthy();
  });

  it("handoff confirm fires onHandoff and closes the popover", () => {
    const onHandoff = vi.fn();
    renderChrome(makeSession({ status: "completed" }), { onHandoff });
    fireEvent.click(screen.getByText("Handoff delivery"));
    fireEvent.click(screen.getByText("Handoff", { selector: "button" }));
    expect(onHandoff).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Create development project")).toBeNull();
  });

  it("handoff confirm is disabled when both targets are unchecked", () => {
    renderChrome(makeSession({ status: "completed" }), {
      createProject: false,
      createChannel: false,
    });
    fireEvent.click(screen.getByText("Handoff delivery"));
    const confirm = screen.getByText("Handoff", { selector: "button" });
    expect(confirm).toHaveProperty("disabled", true);
  });

  it("selected node summary replaces the goal line without adding a third row", () => {
    const { container } = renderChrome(makeSession(), {
      selectedSummary: "偏离度 8.2% — 高置信",
    });
    expect(screen.getByText("偏离度 8.2% — 高置信")).toBeTruthy();
    expect(screen.queryByText("分析知春路沿线 3 公里二手房挂牌与成交")).toBeNull();
    expect(container.querySelectorAll("header > div")).toHaveLength(2);
  });

  it("unknown status falls back to muted tone and raw status text", () => {
    renderChrome(makeSession({ status: "weird_state" }));
    expect(screen.getByText("weird_state")).toBeTruthy();
    expect(document.querySelector(".bg-muted-foreground")).toBeTruthy();
  });
});
