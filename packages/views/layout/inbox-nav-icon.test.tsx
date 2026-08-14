import { render, screen } from "@testing-library/react";
import { Activity } from "lucide-react";
import { describe, expect, it, vi } from "vitest";
import { InboxNavIcon } from "./inbox-nav-icon";

vi.mock("@multica/core/inbox", () => ({
  useMentionPopupStore: (selector: (state: { setIconRect: () => void; bounceSignal: number }) => unknown) =>
    selector({ setIconRect: vi.fn(), bounceSignal: 0 }),
}));

describe("InboxNavIcon", () => {
  it("hides the unread dot until the rail is collapsed", () => {
    render(<InboxNavIcon icon={Activity} unread />);
    const dot = screen.getByTestId("inbox-unread-dot");
    expect(dot.className).toContain("hidden");
    expect(dot.className).toContain("group-data-[collapsible=icon]:block");
  });

  it("does not render an unread dot when the inbox is clear", () => {
    render(<InboxNavIcon icon={Activity} />);
    expect(screen.queryByTestId("inbox-unread-dot")).toBeNull();
  });
});
