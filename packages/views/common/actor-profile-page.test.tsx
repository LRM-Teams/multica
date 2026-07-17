// @vitest-environment jsdom
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActorProfilePage } from "./actor-profile-page";

const mockBack = vi.hoisted(() => vi.fn());

// PageHeader pulls in the sidebar context; stub it to a passthrough so the page
// test stays focused on the back button + content wiring.
vi.mock("../layout/page-header", () => ({
  PageHeader: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="page-header">{children}</div>
  ),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ back: mockBack, push: vi.fn() }),
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: { profile_popover: { back: string } }) => string) =>
      selector({ profile_popover: { back: "Back" } }),
  }),
}));

// The full-page host reuses the SAME peek content component. Stub it to echo the
// props so we can assert the route forwards memberType/memberId unchanged.
vi.mock("./actor-profile-popover", () => ({
  ActorProfileContent: ({
    memberType,
    memberId,
  }: {
    memberType: string;
    memberId: string;
  }) => (
    <div data-testid="actor-profile-content">
      {memberType}:{memberId}
    </div>
  ),
}));

describe("ActorProfilePage (#586 mobile full page)", () => {
  it("renders the shared profile content for the actor", () => {
    render(<ActorProfilePage memberType="agent" memberId="agent-1" />);

    expect(screen.getByTestId("actor-profile-content")).toHaveTextContent(
      "agent:agent-1",
    );
  });

  it("renders a Back button that calls navigation.back()", () => {
    render(<ActorProfilePage memberType="user" memberId="u1" />);

    const back = screen.getByRole("button", { name: /back/i });
    fireEvent.click(back);
    expect(mockBack).toHaveBeenCalledTimes(1);
  });
});
