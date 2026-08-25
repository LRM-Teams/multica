import {
  render as renderWithTestingLibrary,
  screen,
  cleanup,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { MemberProfile, RunnerActivityResponse } from "@multica/core/types";
import type { AgentLiveStatusView } from "../agents/resolve-agent-live-status";
import enAgents from "../locales/en/agents.json";
import enSettings from "../locales/en/settings.json";
import { ActorProfileContentLoaded } from "./actor-profile-popover";

// Live status is resolved by useAgentLiveStatus (snapshot + task-messages +
// presence). Stub the hook so layout tests stay free of QueryClient.
const mockLiveStatus = vi.hoisted(
  () => ({ current: null as AgentLiveStatusView | null }),
);

const mockActivity = vi.hoisted(() => ({
  current: {
    data: null as RunnerActivityResponse | null | undefined,
    isLoading: false,
    isError: false,
  },
}));
const mockHonorApi = vi.hoisted(() => ({
  getAgentHonor: vi.fn(),
  getUserHonor: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: mockHonorApi,
}));

vi.mock("@multica/core/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/agents")>()),
  useRunnerActivity: () => mockActivity.current,
}));

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatarBase: () => <span data-testid="actor-avatar" />,
}));

vi.mock("./actor-avatar", () => ({
  AgentPresenceOverlay: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("./use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../agents/use-agent-live-status", () => ({
  useAgentLiveStatus: () => mockLiveStatus.current,
}));


// Namespace-aware i18n stub: only the channels profile_popover copy is needed
// for layout/description tests (live status labels come from the hook stub).
vi.mock("../i18n/use-t", () => {
  const CHANNELS_RES = {
    profile_popover: {
      unknown: "Unknown",
      loading: "Loading",
      no_description: "No description",
      description: "Description",
      recent_activity: "Recent activity",
      no_recent_activity: "No recent activity",
      honor: {
        title: "Developer honor",
        agent_title: "Agent honor",
        no_badge: "No badge equipped",
        keep_building: "Keep building",
        collection: "{{unlocked}} / {{total}} collected",
        agent_collection: "{{unlocked}} unlocked",
        level_value: "LV.{{level}}",
      },
      restricted: {
        runtime: "Runtime",
        usage: "Usage",
        activity: "Activity",
        channel_only: "Channel-only",
      },
      role: { agent: "Agent", owner: "Owner", admin: "Admin", member: "Member" },
    },
  };
  return {
    useT: (namespace?: string) => ({
      t: (
        selector: (r: any) => string,
        values?: Record<string, string | number>,
      ) => {
        let value = selector(
          namespace === "agents"
            ? enAgents
            : namespace === "settings"
              ? enSettings
              : CHANNELS_RES,
        );
        for (const [key, replacement] of Object.entries(values ?? {})) {
          value = value.replaceAll(`{{${key}}}`, String(replacement));
        }
        return value;
      },
    }),
  };
});

function render(ui: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return renderWithTestingLibrary(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>,
  );
}

function makeProfile(): MemberProfile {
  return {
    member_type: "agent",
    member_id: "agent-1",
    name: "agent_aegis",
    display_name: "Aegis",
    avatar_url: null,
    description: "Builds and reviews changes.",
    role: "agent",
    status: "working",
    // Kept only to satisfy the MemberProfile type; the popover consumes the
    // same Runner Activity timeline as the full Activity tab.
    recent_activity: [],
    profile_access: "full",
  };
}

function live(
  label: string,
  over: Partial<AgentLiveStatusView> = {},
): AgentLiveStatusView {
  return {
    label,
    textClass: "text-muted-foreground",
    dotClass: "bg-muted-foreground/40",
    ...over,
  };
}

beforeEach(() => {
  cleanup();
  mockLiveStatus.current = live("Idle");
  mockActivity.current = { data: null, isLoading: false, isError: false };
  mockHonorApi.getAgentHonor.mockReset();
  mockHonorApi.getUserHonor.mockReset();
  mockHonorApi.getAgentHonor.mockImplementation(
    () => new Promise(() => undefined),
  );
  mockHonorApi.getUserHonor.mockImplementation(
    () => new Promise(() => undefined),
  );
});

describe("ActorProfileContentLoaded", () => {
  it("reuses Activity timeline chrome for the latest five headings without details", () => {
    mockActivity.current = {
      data: {
        summary: { label: "Running command...", activityKind: "working", detailKind: "running_command" },
        timeline: [
          {
            id: "newest-command",
            occurred_at: "2026-08-14T06:00:00Z",
            title: "Running command",
            subtext: "pnpm test -- --secret",
            activity_kind: "working",
            detail_kind: "running_command",
            body_kind: "command",
            body: "pnpm test -- --secret",
          },
          {
            id: "sending",
            occurred_at: "2026-08-14T05:59:00Z",
            title: "Sending message",
            subtext: "#private-target",
            activity_kind: "working",
            detail_kind: "sending_message",
            body_kind: "none",
          },
          {
            id: "working",
            occurred_at: "2026-08-14T05:58:00Z",
            title: "Working",
            subtext: "Message received",
            activity_kind: "working",
            detail_kind: "message_received",
            body_kind: "none",
          },
          {
            id: "idle",
            occurred_at: "2026-08-14T05:57:00Z",
            title: "Idle",
            subtext: "Idle",
            activity_kind: "online",
            detail_kind: "idle",
            body_kind: "none",
          },
          {
            id: "starting",
            occurred_at: "2026-08-14T05:56:00Z",
            title: "Starting",
            activity_kind: "working",
            detail_kind: "starting",
            body_kind: "none",
          },
          {
            id: "older",
            occurred_at: "2026-08-14T05:55:00Z",
            title: "Older omitted activity",
            activity_kind: "offline",
            detail_kind: "stopped",
            body_kind: "none",
          },
        ],
      },
      isLoading: false,
      isError: false,
    };

    render(<ActorProfileContentLoaded profile={makeProfile()} />);

    const timeline = screen.getByTestId("profile-activity-timeline");
    expect(timeline).toHaveAttribute("data-activity-details", "hidden");
    expect(screen.getByTestId("profile-activity-timeline-spine")).toBeInTheDocument();
    const rows = screen.getAllByTestId("profile-activity-row");
    expect(rows).toHaveLength(5);
    ["Starting", "Idle", "Working", "Sending message", "Running command"].forEach(
      (title, index) => expect(rows[index]).toHaveTextContent(title),
    );
    expect(rows[4]?.querySelector(".bg-dot-working")).not.toBeNull();
    expect(screen.queryByText("Older omitted activity")).toBeNull();
    expect(screen.queryByText("pnpm test -- --secret")).toBeNull();
    expect(screen.queryByText("#private-target")).toBeNull();
    expect(screen.queryByText("Message received")).toBeNull();
    expect(screen.queryByTestId("activity-command-block")).toBeNull();
    expect(screen.queryByTestId("runner-activity-subtext")).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy" })).toBeNull();
  });

  it("shows compact timeline skeletons before the first Activity paint", () => {
    mockActivity.current = { data: undefined, isLoading: true, isError: false };

    render(<ActorProfileContentLoaded profile={makeProfile()} />);

    expect(screen.getByTestId("profile-activity-loading")).toHaveAttribute("aria-busy", "true");
    expect(screen.queryByText("No recent activity")).toBeNull();
  });

  it("does not render a name-row Online/Offline label (avatar badge only, LRM-248)", () => {
    mockLiveStatus.current = live("Offline");

    render(<ActorProfileContentLoaded profile={makeProfile()} />);

    expect(screen.queryByTestId("agent-live-status")).toBeNull();
    expect(screen.queryByText("Offline")).toBeNull();
    expect(screen.getByText("Aegis")).toBeInTheDocument();
  });

  it("omits the description section when the profile has no description", () => {
    const profile = makeProfile();
    profile.description = "";

    render(<ActorProfileContentLoaded profile={profile} />);

    expect(screen.queryByText("No description")).toBeNull();
    expect(screen.queryByText("Builds and reviews changes.")).toBeNull();
  });

  it("renders a real description when present", () => {
    render(<ActorProfileContentLoaded profile={makeProfile()} />);

    expect(screen.getByText("Builds and reviews changes.")).toBeInTheDocument();
  });

  it("identity_only: keeps name + description, greys sensitive blocks (LRM-288)", () => {
    // #2: a private/removed agent surfaced via a readable message returns only
    // basic identity (profile_access=identity_only). Show the identity card —
    // never a blank "Agent unavailable" — and grey sensitive panels with an
    // explicit "Channel-only" label (LRM-238: no silent omission).
    const profile = makeProfile();
    profile.profile_access = "identity_only";
    mockActivity.current = {
      data: {
        summary: { label: "Thinking...", activityKind: "thinking", detailKind: "thinking_started" },
        timeline: [{
          id: "thinking",
          occurred_at: "2026-08-14T06:00:00Z",
          title: "Thinking...",
          activity_kind: "thinking",
          detail_kind: "thinking_started",
          body_kind: "none",
        }],
      },
      isLoading: false,
      isError: false,
    };

    render(<ActorProfileContentLoaded profile={profile} />);

    // Identity stays visible.
    expect(screen.getByText("Aegis")).toBeInTheDocument();
    expect(screen.getByText("Builds and reviews changes.")).toBeInTheDocument();
    // Protected live panels are not live-rendered — no live mark, no timeline —
    // even though the activity stub has events.
    expect(screen.queryByTestId("agent-live-status")).toBeNull();
    expect(screen.queryByText("Thinking...")).toBeNull();
    expect(screen.queryByText("Recent activity")).toBeNull();
    // Sensitive blocks are explicit (greyed + channel-only), not silent.
    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.getByText("Usage")).toBeInTheDocument();
    expect(screen.getAllByText("Channel-only")).toHaveLength(3);
  });

  it("keeps a human member's level and equipped badge on the identity card", async () => {
    mockHonorApi.getUserHonor.mockResolvedValue({
      level: 42,
      name_style: "animated_prismatic",
      equipped_badge: {
        id: "prism_core",
        title: "Prism Core",
        description: "Late-game badge",
        svg_key: "prism_core",
      },
      showcase_badges: [
        { id: "earth", title: "Earth", description: "", svg_key: "earth" },
        { id: "mars", title: "Mars", description: "", svg_key: "mars" },
        { id: "saturn", title: "Saturn", description: "", svg_key: "saturn" },
      ],
      badges_unlocked: 28,
      badges_total: 51,
      unlocked_badges: [],
    });
    const profile: MemberProfile = {
      member_type: "user",
      member_id: "user-1",
      name: "caosz2",
      display_name: "caosz2",
      avatar_url: null,
      description: "Ships collaboration software.",
      role: "admin",
      status: "",
      recent_activity: [],
      profile_access: "full",
    };

    render(<ActorProfileContentLoaded profile={profile} />);

    const summary = await screen.findByTestId("member-honor-showcase");
    expect(summary).toHaveTextContent("LV.42");
    expect(summary).toHaveTextContent("Prism Core");
    expect(summary.querySelector('[data-user-honor-level="42"]')).not.toBeNull();
    expect(summary.closest("section")).toBeNull();
    expect(summary).not.toHaveClass("honor-dark-surface");
    expect(screen.queryByText("Developer honor")).toBeNull();
    expect(screen.queryByText("28 / 51 collected")).toBeNull();
    expect(screen.getAllByText("caosz2")).toHaveLength(1);
  });

  it("keeps an agent's level and equipped achievement on the identity card", async () => {
    mockHonorApi.getAgentHonor.mockResolvedValue({
      agent_id: "agent-1",
      level: 12,
      total_xp: 3_400,
      xp_to_next_level: 200,
      equipped_achievement_id: "streak_5",
      showcase_achievement_ids: ["streak_5"],
      metrics: {
        completed_count: 32,
        failed_count: 2,
        success_streak: 8,
        memory_writes: 14,
        evolution_promotions: 1,
        distinct_projects: 3,
        recovery_count: 2,
      },
      fleet: {
        agent_id: "agent-1",
        fleet_score: 73,
        class_id: "battleship",
        class_label: "Battleship",
        fleet_rank: 2,
        fleet_size: 18,
        sample_tasks: 34,
        min_sample_tasks: 5,
        sample_sufficient: true,
        frozen: false,
        pillars: {
          delivery: 0.82,
          evolution: 0.65,
          growth: 0.58,
          efficiency: 0.74,
        },
      },
      achievements: [
        {
          id: "streak_5",
          title: "Clean Burn",
          description: "Five accepted tasks in a row.",
          svg_key: "venus",
          category: "reliability",
          xp_reward: 75,
          rarity: 30,
          secret: false,
          unlocked: true,
        },
      ],
      recent_events: [],
      fleet_history: [],
      rules_version: "test",
    });

    render(<ActorProfileContentLoaded profile={makeProfile()} />);

    const summary = await screen.findByTestId("agent-honor-showcase");
    expect(summary).toHaveTextContent("LV.12");
    expect(summary).toHaveTextContent("Clean Burn");
    expect(summary.querySelector("[data-agent-honor-level='12']")).toHaveClass("size-10");
    expect(summary).toHaveClass("rounded-lg", "border");
    expect(summary.closest("section")).toBeNull();
    expect(summary).not.toHaveClass("honor-dark-surface");
    expect(screen.queryByText("Agent honor")).toBeNull();
    expect(screen.queryByText("3400 XP")).toBeNull();
    expect(screen.queryByText("Battleship")).toBeNull();
  });
});
