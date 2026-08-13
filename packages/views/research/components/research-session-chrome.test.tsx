import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ResearchSession } from "@multica/core/types";
import { ResearchSessionChrome } from "./research-session-chrome";

const mobileState = { isMobile: false };

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("./research-session-goal-card", () => ({
  ResearchSessionGoalCard: ({ goal }: { goal: string }) => (
    <div data-testid="research-session-goal-card">{goal}</div>
  ),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, vars?: Record<string, unknown>) => {
      const raw = fn({
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
        stage_short: {
          s1_plan: "S1",
          s2_sources: "S2",
          s3_validation: "S3",
          s4_delivery: "S4",
        },
        panel: {
          confirm_continue: "Confirm & continue",
          gate_approve: "Approve",
          gate_reject: "Reject",
          gate_reject_title: "Reject confirmation",
          gate_reject_hint: "Optional feedback goes back to the lead.",
          gate_reject_placeholder: "What should the fleet revise?",
          gate_reject_submit: "Send rejection",
          gate_reject_submitting: "Sending…",
          handoff_title: "Handoff delivery",
          view_delivery: "View delivery",
          handoff_project: "Create development project",
          handoff_channel: "Create development channel",
          handoff: "Handoff",
          session_tools: "Session tools",
          fleet: "Fleet",
          sources: "Sources",
          session_tools_hint: "On demand",
        },
        round: {
          subtitle: "Product rounds",
          budget_chip: "{{used}}/{{budget}}",
          budget_capped: "capped",
        },
        timeline: {
          label: "Research stages",
          done: "Done",
          current: "Current",
          upcoming: "Upcoming",
          done_feedback: "Stage completed",
        },
        create_params: {
          session_menu: "Create parameters",
          session_hint: "Read-only",
          depth_label: "Depth",
          language_label: "Language",
          weights_label: "Weights",
          chip_depth: "Depth · {{label}}",
          depth_tiers: {
            shallow: { label: "Shallow", hint: "2" },
            standard: { label: "Standard", hint: "5" },
            deep: { label: "Deep", hint: "10" },
          },
          language_options: { zh: "中文", en: "English" },
          weight_rows: {
            primary: { label: "Primary", hint: "Official" },
            secondary: { label: "Secondary", hint: "Reviews" },
            community: { label: "Community", hint: "Forums" },
          },
        },
      });
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
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
      return React.cloneElement(
        renderProp ?? <button type="button" />,
        {
          onClick: () => onOpenChange?.(!open),
        } as Record<string, unknown>,
        children,
      );
    },
    PopoverContent: ({
      children,
      ...rest
    }: {
      children?: React.ReactNode;
      "data-testid"?: string;
    }) => {
      const { open } = React.useContext(Ctx);
      return open ? (
        <div data-testid={rest["data-testid"] ?? "popover-content"}>{children}</div>
      ) : null;
    },
    PopoverHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    PopoverTitle: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    PopoverDescription: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  };
});

vi.mock("@multica/ui/components/ui/dropdown-menu", async () => {
  const React = await import("react");
  return {
    DropdownMenu: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuTrigger: ({
      render: renderProp,
      children,
    }: {
      render?: React.ReactElement;
      children?: React.ReactNode;
    }) => React.cloneElement(renderProp ?? <button type="button" />, {}, children),
    DropdownMenuContent: ({ children }: { children?: React.ReactNode }) => (
      <div data-testid="menu">{children}</div>
    ),
    DropdownMenuItem: ({
      children,
      onClick,
    }: {
      children?: React.ReactNode;
      onClick?: () => void;
    }) => (
      <button type="button" onClick={onClick}>
        {children}
      </button>
    ),
    DropdownMenuSeparator: () => <hr data-testid="menu-separator" />,
  };
});

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: () => null,
  SheetContent: () => null,
  SheetHeader: () => null,
  SheetTitle: () => null,
  SheetDescription: () => null,
}));

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
  beforeEach(() => {
    mobileState.isMobile = false;
  });

  it("renders single header: identity, status, stage timeline, actions, goal", () => {
    renderChrome(makeSession());
    expect(screen.getByTestId("research-session-identity")).toBeTruthy();
    expect(screen.getByTestId("research-session-actions")).toBeTruthy();
    expect(screen.getByTestId("research-stage-timeline")).toBeTruthy();
    expect(screen.getByText("知春路沿线房产市场深度调研")).toBeTruthy();
    expect(screen.getByTestId("research-session-status").textContent).toContain("Running");
    expect(screen.getByRole("button", { name: /S2 · Explore/ })).toBeTruthy();
    expect(screen.getByText("分析知春路沿线 3 公里二手房挂牌与成交")).toBeTruthy();
    const dot = document.querySelector(".bg-brand.animate-pulse");
    expect(dot).toBeTruthy();
  });

  it("running state shows no primary action; desktop keeps delivery outline", () => {
    renderChrome(makeSession({ status: "running" }));
    expect(screen.queryByText("Approve")).toBeNull();
    expect(screen.queryByText("Reject")).toBeNull();
    expect(screen.queryByText("Handoff delivery")).toBeNull();
    expect(screen.getByTestId("research-session-delivery")).toBeTruthy();
    expect(screen.getByText("View delivery")).toBeTruthy();
  });

  it("narrow folds delivery into tools so primary is not crowded", () => {
    mobileState.isMobile = true;
    const onOpenDelivery = vi.fn();
    renderChrome(makeSession({ status: "awaiting_user_confirm" }), {
      onOpenDelivery,
      onReject: () => {},
    });
    expect(screen.getByTestId("research-session-primary")).toBeTruthy();
    expect(screen.queryByTestId("research-session-delivery")).toBeNull();
    expect(screen.getByText("View delivery")).toBeTruthy();
    fireEvent.click(screen.getByText("View delivery"));
    expect(onOpenDelivery).toHaveBeenCalledTimes(1);
  });

  it("awaiting_user_confirm closes reject feedback only after commit succeeds (LRM-840)", async () => {
    const onConfirm = vi.fn();
    const onReject = vi.fn().mockResolvedValue(undefined);
    renderChrome(makeSession({ status: "awaiting_user_confirm" }), {
      onConfirm,
      onReject,
    });
    expect(screen.getByText("Awaiting confirm")).toBeTruthy();
    expect(screen.getByTestId("research-session-status").textContent).toContain(
      "Awaiting confirm",
    );
    expect(screen.queryByText("Handoff delivery")).toBeNull();
    fireEvent.click(screen.getByText("Approve"));
    expect(onConfirm).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId("research-session-gate-reject"));
    expect(screen.getByTestId("research-session-gate-reject-popover")).toBeTruthy();
    fireEvent.change(screen.getByTestId("research-session-gate-reject-reason"), {
      target: { value: "来源权重不够" },
    });
    fireEvent.click(screen.getByTestId("research-session-gate-reject-submit"));
    expect(onReject).toHaveBeenCalledWith("来源权重不够");
    await waitFor(() =>
      expect(
        screen.queryByTestId("research-session-gate-reject-popover"),
      ).toBeNull(),
    );
  });

  it("keeps reject feedback and focus while canonical rejection is pending", () => {
    let resolveReject: (() => void) | undefined;
    const onReject = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveReject = resolve;
        }),
    );
    const session = makeSession({ status: "awaiting_user_confirm" });
    const { rerender } = renderChrome(session, { onReject });

    fireEvent.click(screen.getByTestId("research-session-gate-reject"));
    const reason = screen.getByTestId("research-session-gate-reject-reason");
    fireEvent.change(reason, { target: { value: "保留这条反馈" } });
    const submit = screen.getByTestId("research-session-gate-reject-submit");
    submit.focus();
    fireEvent.click(submit);
    fireEvent.click(submit);
    expect(onReject).toHaveBeenCalledTimes(1);

    rerender(
      <ResearchSessionChrome
        session={session}
        canConfirm
        canHandoff
        createProject
        createChannel
        onCreateProjectChange={() => {}}
        onCreateChannelChange={() => {}}
        onConfirm={() => {}}
        onReject={onReject}
        onHandoff={() => {}}
        onOpenDelivery={() => {}}
        rejectPending
      />,
    );

    const pendingSubmit = screen.getByTestId(
      "research-session-gate-reject-submit",
    ) as HTMLButtonElement;
    expect(screen.getByTestId("research-session-gate-reject-popover")).toBeTruthy();
    expect(screen.getByTestId("research-session-gate-reject-reason")).toHaveValue(
      "保留这条反馈",
    );
    expect(pendingSubmit.disabled).toBe(false);
    expect(pendingSubmit).toHaveAttribute("aria-disabled", "true");
    expect(document.activeElement).toBe(pendingSubmit);
    fireEvent.click(pendingSubmit);
    expect(onReject).toHaveBeenCalledTimes(1);
    resolveReject?.();
  });

  it("keeps exact reject feedback available when the commit fails", async () => {
    const onReject = vi.fn().mockRejectedValue(new Error("commit failed"));
    renderChrome(makeSession({ status: "awaiting_user_confirm" }), { onReject });

    fireEvent.click(screen.getByTestId("research-session-gate-reject"));
    fireEvent.change(screen.getByTestId("research-session-gate-reject-reason"), {
      target: { value: "不要丢失这条反馈" },
    });
    fireEvent.click(screen.getByTestId("research-session-gate-reject-submit"));

    await waitFor(() => expect(onReject).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId("research-session-gate-reject-popover")).toBeTruthy();
    expect(screen.getByTestId("research-session-gate-reject-reason")).toHaveValue(
      "不要丢失这条反馈",
    );
  });

  it("LRM-1240: gateBusy keeps approve/reject focusable via aria-disabled (not native disabled)", () => {
    const onConfirm = vi.fn();
    const onReject = vi.fn();
    renderChrome(makeSession({ status: "awaiting_user_confirm" }), {
      onConfirm,
      onReject,
      confirmPending: true,
    });

    const approve = screen.getByTestId("research-session-primary");
    const reject = screen.getByTestId("research-session-gate-reject");

    expect(approve).toHaveProperty("disabled", false);
    expect(approve.getAttribute("aria-disabled")).toBe("true");
    expect(reject).toHaveProperty("disabled", false);
    expect(reject.getAttribute("aria-disabled")).toBe("true");

    fireEvent.click(approve);
    expect(onConfirm).not.toHaveBeenCalled();

    fireEvent.click(reject);
    expect(screen.queryByTestId("research-session-gate-reject-popover")).toBeNull();
    expect(onReject).not.toHaveBeenCalled();
  });

  it("completed shows handoff primary; checkboxes live inside the popover", () => {
    renderChrome(makeSession({ status: "completed" }));
    expect(screen.queryByText("Approve")).toBeNull();
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

  it("LRM-1265: handoffPending keeps trigger focusable via aria-disabled (not native disabled)", () => {
    const onHandoff = vi.fn();
    renderChrome(makeSession({ status: "completed" }), {
      onHandoff,
      handoffPending: true,
    });

    const trigger = screen.getByTestId("research-session-primary");
    expect(trigger).toHaveProperty("disabled", false);
    expect(trigger.getAttribute("aria-disabled")).toBe("true");
    expect(trigger.className).toMatch(/opacity-50/);
    expect(trigger.className).toMatch(/cursor-not-allowed/);

    fireEvent.click(trigger);
    expect(screen.queryByText("Create development project")).toBeNull();
    expect(onHandoff).not.toHaveBeenCalled();
  });

  it("LRM-1008: Goal Card shows session.goal (not selected node summary)", () => {
    const { container } = renderChrome(makeSession());
    expect(screen.getByTestId("research-session-goal-card").textContent).toContain(
      "分析知春路沿线 3 公里二手房挂牌与成交",
    );
    // Hairline + identity + toolbar.
    expect(container.querySelectorAll("header > div").length).toBe(3);
  });

  it("unknown status falls back to muted tone and raw status text", () => {
    renderChrome(makeSession({ status: "weird_state" }));
    expect(screen.getByText("weird_state")).toBeTruthy();
    expect(document.querySelector(".bg-muted-foreground")).toBeTruthy();
  });

  it("LRM-824: stage step becomes a button that anchors to the current stage", () => {
    const onSelectStage = vi.fn();
    renderChrome(makeSession({ current_stage: "s2_sources" }), { onSelectStage });
    const step = screen.getByRole("button", { name: /S2 · Explore — Current/ });
    fireEvent.click(step);
    expect(onSelectStage).toHaveBeenCalledWith("s2_sources");
  });

  it("LRM-824: stage step stays inert when no anchor handler is wired", () => {
    renderChrome(makeSession({ current_stage: "s2_sources" }));
    const step = screen.getByRole("button", { name: /S2 · Explore — Current/ });
    expect(step).toHaveAttribute("disabled");
  });
});
