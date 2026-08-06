"use client";

import type { ComponentProps, ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ChannelActiveTask, ChannelMemberBrief } from "@multica/core/types";
import {
  ChannelPresenceCluster,
} from "./channel-agents-live-cue";

const mobileState = { isMobile: false };
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../../agents/use-agent-live-status", () => ({
  useAgentActivityProjection: () => null,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) =>
      id === "a1" ? "Beckham" : id === "a2" ? "Wendy" : "Unknown Agent",
    getActorInitials: () => "A",
    getActorAvatarUrl: () => null,
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberProfileOptions: (_wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", "ws-1", "member-profiles", type, id],
    queryFn: async () => {
      if (id === "a-hidden") {
        return {
          member_type: "agent",
          member_id: "a-hidden",
          name: "hidden-slug",
          display_name: "隐藏群管",
          avatar_url: "/agent-avatars/hidden.png",
        };
      }
      if (id === "a-face") {
        return {
          member_type: "agent",
          member_id: "a-face",
          name: "face-slug",
          display_name: "有脸Agent",
          avatar_url: "/agent-avatars/face.png",
        };
      }
      throw new Error(`profile missing: ${type}/${id}`);
    },
    enabled: !!id,
  }),
}));

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>,
  );
}

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (picker: (ns: Record<string, unknown>) => unknown, vars?: Record<string, unknown>) => {
      const header = {
        presence_counts: `{{members}} · {{agents}}`,
        presence_working: `{{working}} working`,
        presence_attention: `Needs attention`,
        working_list_title: `Working · {{count}}`,
        working_verb_with_duration: `{{verb}} · {{duration}}`,
        working_failed: `Couldn't reply · try @ again`,
        working_no_reply: `No reply · try @ again`,
        view_members_aria: `View members`,
      };
      const agent_status = {
        running: "Thinking",
        queued: "Queued",
        stop: "Stop",
        stop_all: "Stop all",
        stop_aria: `Stop {{name}}'s current task`,
        stop_all_aria: `Stop all {{count}} current agent tasks`,
      };
      const ns = { header, agent_status };
      const template = picker(ns as never);
      if (typeof template !== "string") return String(template);
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(vars?.[key] ?? `{{${key}}}`),
      );
    },
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({
    actorId,
    showStatusDot,
    avatarUrlHint,
    name,
  }: {
    actorId: string;
    showStatusDot?: boolean;
    avatarUrlHint?: string | null;
    name?: string;
  }) => (
    <span
      data-testid={`face-${actorId}`}
      data-show-status-dot={showStatusDot ? "true" : "false"}
      data-avatar-hint={avatarUrlHint ?? ""}
      data-name={name ?? ""}
    >
      {actorId}
    </span>
  ),
}));

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: ({ name }: { name: string }) => <span data-testid="avatar">{name}</span>,
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

function task(over: Partial<ChannelActiveTask>): ChannelActiveTask {
  return {
    agent_id: "a1",
    agent_name: "Beckham",
    task_id: "t1",
    status: "running",
    kind: "reply",
    reason: "mention",
    inbox_event_id: "inbox-1",
    ...over,
  };
}

function members(
  ids: string[],
  extras?: Record<string, Partial<ChannelMemberBrief>>,
): ChannelMemberBrief[] {
  return ids.map((id) => ({
    member_type: id.startsWith("u") ? ("user" as const) : ("agent" as const),
    member_id: id,
    name: id,
    display_name: id,
    avatar_url: null,
    ...extras?.[id],
  }));
}

describe("ChannelPresenceCluster (LRM-581 A v3)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mobileState.isMobile = false;
  });

  it("idle K≥2 shows faces only — no outer N · M counts or Stop", () => {
    const onOpen = vi.fn();
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[]}
        onOpenMembers={onOpen}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "false");
    expect(screen.getByTestId("channel-presence-faces")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-presence-counts")).toBeNull();
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(chip).not.toHaveTextContent("4");
    expect(chip).not.toHaveTextContent("8");
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();
    fireEvent.click(chip);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("K=1 never shows Working chrome even with running tasks", () => {
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1"])}
        memberCount={2}
        agentCount={1}
        tasks={[task({ status: "running" })]}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "false");
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
  });

  it("K≥2 working: faces + ring only (no outer count/working text); Stop all in card", () => {
    const onStopAll = vi.fn();
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({ task_id: "t1", inbox_event_id: "i1", status: "running" }),
          task({
            agent_id: "a2",
            agent_name: "Wendy",
            task_id: "t2",
            inbox_event_id: "i2",
            status: "queued",
          }),
        ]}
        onStopTask={vi.fn()}
        onStopAll={onStopAll}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    expect(screen.getByTestId("channel-presence-faces")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-presence-counts")).toBeNull();
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(chip).not.toHaveTextContent("working");
    expect(chip).not.toHaveTextContent("4");
    expect(chip).not.toHaveTextContent("8");
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();

    fireEvent.click(chip);
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    // LRM-1288: projection mock is null — running/queued show Waiting, never Thinking.
    expect(screen.queryByText(/Thinking/)).toBeNull();
    expect(screen.getAllByText(/Waiting/).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByTestId("channel-agents-working-stop-all"));
    expect(onStopAll).toHaveBeenCalledTimes(1);
  });

  it("terminal no_reply/failed alone do not open Working chrome (Activity SoT)", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({
            agent_id: "a2",
            agent_name: "Wendy",
            task_id: "t2",
            inbox_event_id: "inbox-2",
            status: "failed",
            outcome: "no_reply",
          }),
        ]}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "false");
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    fireEvent.click(chip);
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
  });

  it("facepile stacks with z-index and no status-dot punch-outs", () => {
    const { container } = renderWithQuery(
      <ChannelPresenceCluster
        members={members(["a1", "a2", "u1"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({ task_id: "t1", inbox_event_id: "i1", status: "running" }),
          task({
            agent_id: "a2",
            agent_name: "Wendy",
            task_id: "t2",
            inbox_event_id: "i2",
            status: "running",
          }),
        ]}
      />,
    );
    const faces = screen.getByTestId("channel-presence-faces");
    expect(faces.className).toContain("isolate");
    const stacked = faces.querySelectorAll(":scope > span");
    expect(stacked.length).toBeGreaterThanOrEqual(2);
    expect((stacked[0] as HTMLElement).style.zIndex).toBe("1");
    expect((stacked[1] as HTMLElement).style.zIndex).toBe("2");
    // Facepile must not enable status-dot punch-outs (collide with neighbor rings).
    expect(
      container.querySelectorAll('[data-show-status-dot="true"]').length,
    ).toBe(0);
    expect(
      container.querySelectorAll('[data-show-status-dot="false"]').length,
    ).toBeGreaterThanOrEqual(2);
  });

  it("LRM-391: directory-miss Unknown Agent rows stay out of Working list", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({ task_id: "t1", inbox_event_id: "i1", status: "running" }),
          task({
            agent_id: "a-gone",
            agent_name: "Unknown Agent",
            task_id: "t-gone",
            inbox_event_id: "i-gone",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    fireEvent.click(chip);
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    expect(screen.getAllByText("Beckham").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Unknown Agent")).toBeNull();
    expect(screen.getByText("Working · 1")).toBeInTheDocument();
    expect(screen.getAllByTestId("channel-agents-working-row")).toHaveLength(1);
  });


  it("LRM-391: emit-time agent_name wins over directory Unknown Agent sentinel", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1"])}
        memberCount={2}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-emit",
            agent_name: "前端工程师",
            task_id: "t-e",
            inbox_event_id: "i-e",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-header-members-chip"));
    expect(screen.getAllByText("前端工程师").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Unknown Agent")).toBeNull();
  });

  it("LRM-1350: Stop passes Working-list resolved name, not Unknown Agent sentinel", () => {
    mobileState.isMobile = true;
    const onStopTask = vi.fn();
    const stopped = task({
      agent_id: "a-roster",
      agent_name: "Unknown Agent",
      task_id: "t-stop",
      inbox_event_id: "i-stop",
      status: "running",
    });
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a-roster"], {
          "a-roster": {
            display_name: "群内Agent",
            avatar_url: "/agent-avatars/roster.png",
          },
        })}
        memberCount={2}
        agentCount={2}
        tasks={[stopped]}
        canStop
        onStopTask={onStopTask}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-header-members-chip"));
    expect(screen.getAllByText("群内Agent").length).toBeGreaterThanOrEqual(1);
    fireEvent.click(screen.getByTestId("channel-agents-working-stop"));
    expect(onStopTask).toHaveBeenCalledTimes(1);
    expect(onStopTask).toHaveBeenCalledWith(stopped, "群内Agent");
  });

  it("LRM-391 AC#5: channel roster name+avatar keeps Working face (no over-omit)", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a-roster"], {
          "a-roster": {
            display_name: "群内Agent",
            avatar_url: "/agent-avatars/roster.png",
          },
        })}
        memberCount={2}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-roster",
            agent_name: "Unknown Agent",
            task_id: "t-r",
            inbox_event_id: "i-r",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    expect(screen.getByTestId("face-a-roster")).toHaveAttribute(
      "data-avatar-hint",
      "/agent-avatars/roster.png",
    );
    fireEvent.click(chip);
    expect(screen.getAllByText("群内Agent").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Unknown Agent")).toBeNull();
    // Working row Avatar also gets the roster face hint.
    const workingFace = screen.getAllByTestId("face-a-roster");
    expect(
      workingFace.some(
        (el) => el.getAttribute("data-avatar-hint") === "/agent-avatars/roster.png",
      ),
    ).toBe(true);
  });


  it("LRM-391 AC#5: emit-time task.avatar_url seeds facepile without roster/profile", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1"])}
        memberCount={1}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-snap",
            agent_name: "快照Agent",
            avatar_url: "/agent-avatars/snap.png",
            task_id: "t-s",
            inbox_event_id: "i-s",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    expect(screen.getByTestId("face-a-snap")).toHaveAttribute(
      "data-avatar-hint",
      "/agent-avatars/snap.png",
    );
    fireEvent.click(chip);
    expect(screen.getAllByText("快照Agent").length).toBeGreaterThanOrEqual(1);
    expect(
      screen
        .getAllByTestId("face-a-snap")
        .some(
          (el) =>
            el.getAttribute("data-avatar-hint") === "/agent-avatars/snap.png",
        ),
    ).toBe(true);
  });
});

/**
 * LRM-1348 (parent design gate LRM-1347) — Working list Stop / Stop all must
 * express pending with `aria-disabled` + a guarded handler, never native
 * `disabled`. These buttons live inside a Portal overlay (desktop Base UI
 * PreviewCard / narrow Popover): dropping focus to <body> lets the overlay
 * dismiss its whole subtree, so Stop all and every other row's Stop leave the
 * DOM mid-interaction (LRM-1347 case B, measured in real Chromium).
 */
describe("ChannelPresenceCluster Working Stop pending (LRM-1348)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mobileState.isMobile = true;
  });

  type ClusterProps = ComponentProps<typeof ChannelPresenceCluster>;

  function renderCluster(props: ClusterProps) {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const utils = render(<ChannelPresenceCluster {...props} />, { wrapper });
    return {
      ...utils,
      rerenderCluster: (next: ClusterProps) =>
        utils.rerender(<ChannelPresenceCluster {...next} />),
    };
  }

  const twoRunning = [
    task({ task_id: "t1", inbox_event_id: "i1", status: "running" }),
    task({
      agent_id: "a2",
      agent_name: "Wendy",
      task_id: "t2",
      inbox_event_id: "i2",
      status: "running",
    }),
  ];

  function baseProps(over?: Partial<ClusterProps>): ClusterProps {
    return {
      members: members(["u1", "a1", "a2"]),
      memberCount: 4,
      agentCount: 8,
      tasks: twoRunning,
      onStopTask: vi.fn(),
      onStopAll: vi.fn(),
      ...over,
    };
  }

  function openList() {
    fireEvent.click(screen.getByTestId("channel-header-members-chip"));
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
  }

  /** Indexed access under noUncheckedIndexedAccess — fail loudly, never `!`. */
  function stopAt(index: number): HTMLButtonElement {
    const buttons = screen.getAllByTestId("channel-agents-working-stop");
    const btn = buttons[index];
    if (!btn) {
      throw new Error(
        `expected a Working Stop button at index ${index}, got ${buttons.length}`,
      );
    }
    return btn as HTMLButtonElement;
  }

  function stopAllButton(): HTMLButtonElement {
    return screen.getByTestId(
      "channel-agents-working-stop-all",
    ) as HTMLButtonElement;
  }

  it("idle: Stop and Stop all carry no native disabled and no aria-disabled", () => {
    renderCluster(baseProps({ stoppingTaskId: null }));
    openList();
    const stops = screen.getAllByTestId(
      "channel-agents-working-stop",
    ) as HTMLButtonElement[];
    for (const btn of [...stops, stopAllButton()]) {
      expect(btn.disabled).toBe(false);
      expect(btn).not.toHaveAttribute("aria-disabled");
    }
  });

  it("pending row: aria-disabled=true with zero native disabled", () => {
    renderCluster(baseProps({ stoppingTaskId: "t1" }));
    openList();
    const stop = stopAt(0);
    expect(stop.disabled).toBe(false);
    expect(stop).toHaveAttribute("aria-disabled", "true");
  });

  it("pending Stop click does not call onStopTask; idle click calls once with the same task", () => {
    const onStopTask = vi.fn();
    const { rerenderCluster } = renderCluster(
      baseProps({ stoppingTaskId: "t1", onStopTask }),
    );
    openList();
    fireEvent.click(stopAt(0));
    expect(onStopTask).not.toHaveBeenCalled();

    rerenderCluster(baseProps({ stoppingTaskId: null, onStopTask }));
    fireEvent.click(stopAt(0));
    expect(onStopTask).toHaveBeenCalledTimes(1);
    // AC: same object reference, not a structural clone.
    expect(onStopTask.mock.calls[0]?.[0]).toBe(twoRunning[0]);
  });

  it("pending Stop all click does not call onStopAll; idle click calls once", () => {
    const onStopAll = vi.fn();
    const { rerenderCluster } = renderCluster(
      baseProps({ stoppingTaskId: "__all__", onStopAll }),
    );
    openList();
    fireEvent.click(screen.getByTestId("channel-agents-working-stop-all"));
    expect(onStopAll).not.toHaveBeenCalled();

    rerenderCluster(baseProps({ stoppingTaskId: null, onStopAll }));
    fireEvent.click(screen.getByTestId("channel-agents-working-stop-all"));
    expect(onStopAll).toHaveBeenCalledTimes(1);
  });

  it("focus stays on the very same Stop node when the row enters pending", () => {
    // jsdom does not emulate Chromium's "blur on becoming disabled", so the
    // BEFORE red here is the native `disabled` assertion; the activeElement
    // identity check guards against a future remount/key change, and the real
    // focus-drop is proven by the Chromium probe attached to LRM-1348.
    const { rerenderCluster } = renderCluster(baseProps({ stoppingTaskId: null }));
    openList();
    const stop = stopAt(0);
    stop.focus();
    expect(document.activeElement).toBe(stop);

    rerenderCluster(baseProps({ stoppingTaskId: "t1" }));
    expect(document.activeElement).toBe(stop);
    expect(stop.disabled).toBe(false);
  });

  it("focus stays on Stop all when Stop all enters pending", () => {
    const { rerenderCluster } = renderCluster(baseProps({ stoppingTaskId: null }));
    openList();
    const stopAll = stopAllButton();
    stopAll.focus();
    expect(document.activeElement).toBe(stopAll);

    rerenderCluster(baseProps({ stoppingTaskId: "__all__" }));
    expect(document.activeElement).toBe(stopAll);
    expect(stopAll.disabled).toBe(false);
  });

  it("stopping one row leaves the other row's Stop fully interactive (phase semantics unchanged)", () => {
    // The frozen constraint is "pending semantics verbatim": only `__all__`
    // pends every row. A single in-flight task_id must not spread pending to
    // its siblings, otherwise this slice would silently change the stop phase
    // machine instead of only fixing focus.
    const onStopTask = vi.fn();
    renderCluster(baseProps({ stoppingTaskId: "t1", onStopTask }));
    openList();
    const second = stopAt(1);
    expect(second.disabled).toBe(false);
    expect(second).not.toHaveAttribute("aria-disabled");
    second.focus();
    expect(document.activeElement).toBe(second);
    fireEvent.click(second);
    expect(onStopTask).toHaveBeenCalledTimes(1);
    // AC: same object reference, not a structural clone.
    expect(onStopTask.mock.calls[0]?.[0]).toBe(twoRunning[1]);
  });

  it("__all__ marks every Stop and Stop all aria-disabled while all stay focusable", () => {
    renderCluster(baseProps({ stoppingTaskId: "__all__" }));
    openList();
    const all: HTMLButtonElement[] = [
      ...(screen.getAllByTestId(
        "channel-agents-working-stop",
      ) as HTMLButtonElement[]),
      stopAllButton(),
    ];
    for (const btn of all) {
      expect(btn.disabled).toBe(false);
      expect(btn).toHaveAttribute("aria-disabled", "true");
      btn.focus();
      expect(document.activeElement).toBe(btn);
    }
  });

  it("pending buttons keep a single dim level (LRM-1170)", () => {
    renderCluster(baseProps({ stoppingTaskId: "__all__" }));
    openList();
    const all: HTMLButtonElement[] = [
      ...(screen.getAllByTestId(
        "channel-agents-working-stop",
      ) as HTMLButtonElement[]),
      stopAllButton(),
    ];
    for (const btn of all) {
      // Only unprefixed utilities can actually apply. The Button base keeps an
      // inert `disabled:opacity-50` variant, which never matches because these
      // buttons are never natively disabled.
      const applied = btn.className
        .split(/\s+/)
        .filter((cls) => /^opacity-/.test(cls));
      expect(applied).toEqual(["opacity-50"]);
      expect(btn.className).toContain("cursor-not-allowed");
      // The `ghost` variant's own `hover:*` tokens stay — identical to the
      // already-merged LRM-1213 pattern (research-session-interrupt-banner).
      // Neutralising them would mean touching packages/ui, which this slice
      // must not do; raised on LRM-1348 for the design owner to decide.
    }
  });
});
