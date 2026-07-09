import { render, screen, cleanup } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { MemberProfile, MemberProfileActivityItem } from "@multica/core/types";
import type { AgentLiveStatusView } from "../agents/resolve-agent-live-status";
import { ActorProfileContentLoaded } from "./actor-profile-popover";

// Live status is resolved by useAgentLiveStatus (snapshot + task-messages +
// presence). Stub the hook so layout tests stay free of QueryClient.
const mockLiveStatus = vi.hoisted(
  () => ({ current: null as AgentLiveStatusView | null }),
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

vi.mock("../agents/use-agent-live-status", () => ({
  useAgentLiveStatus: () => mockLiveStatus.current,
}));

// Namespace-aware i18n stub: only the channels profile_popover copy is needed
// for layout/description tests (live status labels come from the hook stub).
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
  return {
    useT: () => ({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      t: (selector: (r: any) => string) => selector(CHANNELS_RES),
    }),
  };
});

function makeActivity(index: number): MemberProfileActivityItem {
  return {
    id: `activity-${index}`,
    kind: "task",
    label: `Activity ${index}`,
    // Fixed UTC times so clock formatting is stable across locales.
    occurred_at: `2026-06-30T12:0${index}:0${index}Z`,
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
    status: "working",
    recent_activity: Array.from({ length: activityCount }, (_, index) =>
      makeActivity(index + 1),
    ),
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
});

describe("ActorProfileContentLoaded", () => {
  it("renders all five recent activity rows as time · dot · label", () => {
    render(<ActorProfileContentLoaded profile={makeProfile(5)} />);

    for (let index = 1; index <= 5; index += 1) {
      expect(screen.getByText(`Activity ${index}`)).toBeInTheDocument();
    }
    // Clock column uses HH:mm:ss — one entry per activity row.
    expect(document.querySelectorAll(".tabular-nums")).toHaveLength(5);
    // Relative "just now" is gone; no icon pills either.
    expect(screen.queryByText(/just now/i)).toBeNull();
  });

  it("name-row status shows Offline when the live hook reports Offline", () => {
    mockLiveStatus.current = live("Offline");

    render(<ActorProfileContentLoaded profile={makeProfile(0)} />);

    expect(screen.getByText("Offline")).toBeInTheDocument();
    expect(screen.queryByText(/Idle/)).toBeNull();
    expect(screen.queryByText(/Working/)).toBeNull();
  });

  it("name-row status shows detailed stage labels from the live hook", () => {
    mockLiveStatus.current = live("Running a command", {
      textClass: "text-brand",
      dotClass: "bg-brand",
    });

    render(<ActorProfileContentLoaded profile={makeProfile(0)} />);

    expect(screen.getByTestId("agent-live-status")).toHaveTextContent(
      "Running a command",
    );
  });

  it("places live status immediately after the display name (not far-right)", () => {
    mockLiveStatus.current = live("Thinking", {
      textClass: "text-brand",
      dotClass: "bg-brand",
    });

    render(<ActorProfileContentLoaded profile={makeProfile(0)} />);

    const name = screen.getByText("Aegis");
    const status = screen.getByTestId("agent-live-status");
    // Name and status share a flex row; the name must not flex-grow, or a
    // short name would shove status to the far edge of the card.
    expect(name.parentElement).toBe(status.parentElement);
    expect(name.className).not.toMatch(/\bflex-1\b/);
    expect(status).toHaveTextContent("Thinking");
  });

  it("omits the description section when the profile has no description", () => {
    const profile = makeProfile(0);
    profile.description = "";

    render(<ActorProfileContentLoaded profile={profile} />);

    expect(screen.queryByText("No description")).toBeNull();
    expect(screen.queryByText("Builds and reviews changes.")).toBeNull();
  });

  it("renders a real description when present", () => {
    render(<ActorProfileContentLoaded profile={makeProfile(0)} />);

    expect(screen.getByText("Builds and reviews changes.")).toBeInTheDocument();
  });
});
