/**
 * @vitest-environment happy-dom
 */
import { cloneElement, type ReactElement, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { NoteShareSummary } from "./note-share-summary";

vi.mock("@multica/ui/components/ui/hover-card", () => ({
  HoverCard: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  HoverCardTrigger: ({
    render,
    children,
    className,
  }: {
    render: ReactElement;
    children: ReactNode;
    className?: string;
  }) => cloneElement(render as ReactElement<{ className?: string; children?: ReactNode }>, { className }, children),
  HoverCardContent: ({ children }: { children: ReactNode }) => <div data-testid="share-hover-card">{children}</div>,
}));

describe("NoteShareSummary", () => {
  it("renders the share summary as a hover trigger, not a navigational link", () => {
    render(
      <NoteShareSummary
        shareNames={["Frank An (LRM-team)", "Ada Lovelace (LRM-team)"]}
        sharedToPrefix="Shared with"
        currentSharesLabel="Current shares"
        sharedEtcLabel="and others"
      />,
    );

    const trigger = screen.getByRole("button", { name: "Current shares" });
    expect(trigger).toHaveTextContent("Frank An (LRM-team)");
    expect(trigger.closest("a")).toBeNull();

    fireEvent.click(trigger);
    expect(screen.getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      "Frank An (LRM-team)",
      "Ada Lovelace (LRM-team)",
    ]);
    expect(screen.getByText("and others")).toBeInTheDocument();
  });
});
