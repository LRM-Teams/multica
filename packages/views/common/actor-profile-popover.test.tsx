import { render, screen, cleanup } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { MemberProfile, MemberProfileActivityItem } from "@multica/core/types";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { ActorProfileContentLoaded } from "./actor-profile-popover";

// The status pill now reads live presence (same source as the dot), so the
// component pulls in useWorkspaceId + useAgentPresenceDetail — stub both.
const mockPresence = vi.hoisted(
  () => ({ current: "loading" as AgentPresenceDetail | "loading" }),
);

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/agents", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/agents")>(
      "@multica/core/agents",
    );
  return { ...actual, useAgentPresenceDetail: () => mockPresence.current };
});

// Namespace-aware i18n stub: `channels` gets the profile_popover copy, `agents`
// gets the workload/availability labels the pill now renders from.
vi.mock("../i18n/use-t", () => {
  const CHANNELS_RES = {
    profile_popover: {
      unknown: "Unknown",
      no_description: "No description",
      description: "Description",
      recent_activity: "Recent activity",
      no_recent_activity: "No recent activity",
      role: { agent: "Agent", owner: "Owner", admin: "Admin", member: "Member" },
      activity: {
        queued: "Queued",
        failed: "Failed",
        cancelled: "Cancelled",
        task: "Task",
        working: "Working",
      },
    },
  };
  const AGENTS_RES = {
    workload: { working: "Working", queued: "Queued", idle: "Idle" },
    availability: {
      online: "Online",
      unstable: "Unstable",
      offline: "Offline",
      archived: "Archived",
    },
  };
  return {
    useT: (ns: string) => ({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      t: (selector: (r: any) => string) =>
        selector(ns === "agents" ? AGENTS_RES : CHANNELS_RES),
    }),
  };
});

vi.mock("../i18n/use-time-ago", () => ({
  useTimeAgo: () => () => "just now",
}));

function makeActivity(index: number): MemberProfileActivityItem {
  return {
    id: `activity-${index}`,
    kind: "task",
    label: `Activity ${index}`,
    occurred_at: `2026-06-30T00:0${index}:00Z`,
    status: "completed",
  };
}

function makeProfile(activityCount: number): MemberProfile {
  return {
    member_type: "agent",
    member_id: "agent-1",
    name: "agent_aegis",
    display_name: "Aegis",
    avatar_url: null,
    description: "Builds and reviews changes.",
    role: "agent",
    // Raw status is intentionally a lie here — the pill must ignore it and use
    // live presence instead.
    status: "working",
    recent_activity: Array.from({ length: activityCount }, (_, index) =>
      makeActivity(index + 1),
    ),
  };
}

function presence(over: Partial<AgentPresenceDetail>): AgentPresenceDetail {
  return {
    availability: "online",
    workload: "idle",
    runningCount: 0,
    queuedCount: 0,
    capacity: 1,
    ...over,
  };
}

beforeEach(() => {
  cleanup();
  mockPresence.current = presence({ availability: "online", workload: "idle" });
});

describe("ActorProfileContentLoaded", () => {
  it("renders all five recent activity rows returned by the profile API", () => {
    render(<ActorProfileContentLoaded profile={makeProfile(5)} />);

    for (let index = 1; index <= 5; index += 1) {
      expect(screen.getByText(`Activity ${index}`)).toBeInTheDocument();
    }
    expect(screen.getAllByText("just now")).toHaveLength(5);
  });

  it("status pill shows the availability word (Offline), not a workload word, when offline", () => {
    // Dot is gray (offline); the pill must agree — never "Idle"/"Working"/the
    // raw `status: 'working'`. This is Frank's #288 card contradiction.
    mockPresence.current = presence({ availability: "offline", workload: "idle" });

    render(<ActorProfileContentLoaded profile={makeProfile(0)} />);

    expect(screen.getByText("Agent · Offline")).toBeInTheDocument();
    expect(screen.queryByText(/Idle/)).toBeNull();
    expect(screen.queryByText(/Working/)).toBeNull();
  });

  it("status pill shows the workload word while online", () => {
    mockPresence.current = presence({ availability: "online", workload: "working" });

    render(<ActorProfileContentLoaded profile={makeProfile(0)} />);

    expect(screen.getByText("Agent · Working")).toBeInTheDocument();
  });
});
