// @vitest-environment jsdom
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ActorProfileTrigger } from "./actor-profile-popover";

// Mutable so a single file can drive both the desktop (HoverCard) and mobile
// (full-page navigation) branches.
const mockIsMobile = vi.hoisted(() => ({ current: false }));
const mockPush = vi.hoisted(() => vi.fn());

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mockIsMobile.current,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ isPending: true, isError: false, data: undefined }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    actorProfile: (memberType: string, memberId: string) =>
      `/acme/profile/${memberType}/${memberId}`,
  }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mockPush, back: vi.fn() }),
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: { profile_popover: { loading: string } }) => string) =>
      selector({ profile_popover: { loading: "Loading" } }),
  }),
}));

afterEach(() => {
  cleanup();
  mockIsMobile.current = false;
  mockPush.mockClear();
});

describe("ActorProfileTrigger — stretch-proof anchor (desktop)", () => {
  it("hugs children so message-grid stretch cannot inflate the hover anchor", () => {
    render(
      <ActorProfileTrigger memberType="agent" memberId="agent-1">
        <span data-testid="avatar">A</span>
      </ActorProfileTrigger>,
    );

    const trigger = screen.getByRole("button");
    expect(trigger.className).toContain("self-start");
    expect(trigger.className).toContain("shrink-0");
    expect(trigger.className).toContain("h-fit");
    expect(trigger.className).toContain("w-fit");
    expect(trigger).toHaveAttribute("data-testid", "actor-profile-trigger");
  });

  it("LRM-740: onClickCapture fires when the trigger is clicked (dock open path)", () => {
    const onClickCapture = vi.fn();
    render(
      <ActorProfileTrigger
        memberType="user"
        memberId="user-9"
        onClickCapture={onClickCapture}
      >
        <span>Bob</span>
      </ActorProfileTrigger>,
    );

    fireEvent.click(screen.getByTestId("actor-profile-trigger"));
    expect(onClickCapture).toHaveBeenCalledTimes(1);
  });
});

describe("ActorProfileTrigger — mobile navigates to the full-page profile (#586)", () => {
  it("navigates to the actor-generic profile route instead of opening a Drawer", () => {
    mockIsMobile.current = true;

    render(
      <ActorProfileTrigger memberType="agent" memberId="agent-1">
        <span data-testid="avatar">A</span>
      </ActorProfileTrigger>,
    );

    // No Base UI Drawer dialog — the mobile branch is a plain navigating trigger.
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button"));
    expect(mockPush).toHaveBeenCalledWith("/acme/profile/agent/agent-1");
  });

  it("routes users too and preserves onClickCapture (span trigger)", () => {
    mockIsMobile.current = true;
    const onClickCapture = vi.fn();

    render(
      <ActorProfileTrigger
        memberType="user"
        memberId="u1"
        triggerElement="span"
        onClickCapture={onClickCapture}
      >
        <span data-testid="mention">@Ada</span>
      </ActorProfileTrigger>,
    );

    fireEvent.click(screen.getByTestId("mention"));
    expect(onClickCapture).toHaveBeenCalled();
    expect(mockPush).toHaveBeenCalledWith("/acme/profile/user/u1");
  });
});
