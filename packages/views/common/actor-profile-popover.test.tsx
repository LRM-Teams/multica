import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { MemberProfile, MemberProfileActivityItem } from "@multica/core/types";
import { ActorProfileContentLoaded } from "./actor-profile-popover";

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        profile_popover: {
          unknown: string;
          no_description: string;
          description: string;
          recent_activity: string;
          no_recent_activity: string;
          role: { agent: string; owner: string; admin: string; member: string };
          activity: {
            queued: string;
            failed: string;
            cancelled: string;
            task: string;
            working: string;
          };
        };
      }) => string,
    ) =>
      selector({
        profile_popover: {
          unknown: "Unknown",
          no_description: "No description",
          description: "Description",
          recent_activity: "Recent activity",
          no_recent_activity: "No recent activity",
          role: {
            agent: "Agent",
            owner: "Owner",
            admin: "Admin",
            member: "Member",
          },
          activity: {
            queued: "Queued",
            failed: "Failed",
            cancelled: "Cancelled",
            task: "Task",
            working: "Working",
          },
        },
      }),
  }),
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
    status: "online",
    recent_activity: Array.from({ length: activityCount }, (_, index) =>
      makeActivity(index + 1),
    ),
  };
}

describe("ActorProfileContentLoaded", () => {
  it("renders all five recent activity rows returned by the profile API", () => {
    render(<ActorProfileContentLoaded profile={makeProfile(5)} />);

    for (let index = 1; index <= 5; index += 1) {
      expect(screen.getByText(`Activity ${index}`)).toBeInTheDocument();
    }
    expect(screen.getAllByText("just now")).toHaveLength(5);
  });
});
