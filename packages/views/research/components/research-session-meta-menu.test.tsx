import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchFleetMember, ResearchSource } from "@multica/core/types";
import type { ReactNode } from "react";
import { ResearchSessionMetaMenu } from "./research-session-meta-menu";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        panel: {
          fleet: "Fleet",
          sources: "Sources",
          session_tools: "Session tools",
          session_tools_hint: "On demand",
          fleet_empty: "No fleet",
          sources_empty: "No sources",
          sources_hint: "Sorted",
          sources_view_all: "View all · {{count}}",
          sources_collapse: "Collapse",
          fleet_badge: { lead: "Lead", pending: "Pending" },
          weight: "Weight",
        },
      }),
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/ui/components/ui/dropdown-menu", async () => {
  const React = await import("react");
  return {
    DropdownMenu: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuTrigger: ({
      render: renderProp,
      children,
    }: {
      render?: React.ReactElement;
      children?: React.ReactNode;
    }) => React.cloneElement(renderProp ?? <button type="button" />, {}, children),
    DropdownMenuContent: ({ children }: { children?: React.ReactNode }) => (
      <div data-testid="menu">{children}</div>
    ),
    DropdownMenuItem: ({
      children,
      onClick,
    }: {
      children?: React.ReactNode;
      onClick?: () => void;
    }) => (
      <button type="button" onClick={onClick}>
        {children}
      </button>
    ),
  };
});

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({
    open,
    children,
  }: {
    open?: boolean;
    children?: ReactNode;
  }) => (open ? <div data-testid="sheet">{children}</div> : null),
  SheetContent: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  SheetHeader: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children?: ReactNode }) => <h2>{children}</h2>,
  SheetDescription: ({ children }: { children?: ReactNode }) => <p>{children}</p>,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="avatar" />,
}));
vi.mock("../../channels/components/agent-compact-activity", () => ({
  AgentCompactActivity: () => null,
}));

const member: ResearchFleetMember = {
  id: "m1",
  agent_id: "a1",
  name: "Ronaldo",
  display_name: "罗纳尔多",
  role: "lead",
  is_lead: true,
  status: "active",
} as ResearchFleetMember;

const source: ResearchSource = {
  id: "s1",
  url: "https://example.com/doc",
  title: "Example Doc",
  credibility_weight: 0.9,
} as ResearchSource;

describe("ResearchSessionMetaMenu (LRM-919)", () => {
  it("opens fleet panel from session tools menu (not a canvas float)", () => {
    render(<ResearchSessionMetaMenu members={[member]} sources={[source]} />);
    fireEvent.click(screen.getByText("Fleet"));
    expect(screen.getByTestId("sheet")).toBeInTheDocument();
    expect(screen.getByTestId("research-session-meta-panel")).toBeInTheDocument();
    expect(screen.getByText("罗纳尔多")).toBeInTheDocument();
    expect(document.querySelector(".absolute.left-4.top-4")).toBeNull();
  });

  it("opens sources panel from session tools menu", () => {
    render(<ResearchSessionMetaMenu members={[member]} sources={[source]} />);
    fireEvent.click(screen.getByText("Sources"));
    expect(screen.getByText("Example Doc")).toBeInTheDocument();
    expect(document.querySelector(".absolute.right-3")).toBeNull();
  });
});
