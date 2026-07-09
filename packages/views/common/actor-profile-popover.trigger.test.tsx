// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActorProfileTrigger } from "./actor-profile-popover";

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ isPending: true, isError: false, data: undefined }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: { profile_popover: { loading: string } }) => string) =>
      selector({ profile_popover: { loading: "Loading" } }),
  }),
}));

describe("ActorProfileTrigger — stretch-proof anchor", () => {
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
  });
});
