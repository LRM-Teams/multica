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
          fleet_empty_title: "No fleet",
          fleet_empty_body: "Empty body",
          fleet_loading_body: "Loading fleet",
          fleet_count: "{{count}} members",
          fleet_done_hint: "Settled",
          fleet_mode: {
            empty: "Idle",
            loading: "Assembling",
            running: "Running",
            done: "Done",
          },
          sources_empty: "No sources",
          sources_hint: "Sorted",
          sources_view_all: "View all · {{count}}",
          sources_collapse: "Collapse",
          fleet_badge: { lead: "Lead", pending: "Pending", done: "Settled" },
          weight: "Weight",
        },
        create_params: {
          session_menu: "Create parameters",
          session_hint: "Read-only create params",
          depth_label: "Depth",
          language_label: "Language",
          weights_label: "Weights",
          depth_tiers: {
            shallow: { label: "Shallow", hint: "2 rounds" },
            standard: { label: "Standard", hint: "5 rounds" },
            deep: { label: "Deep", hint: "10 rounds" },
          },
          language_options: { zh: "中文", en: "English" },
          weight_rows: {
            primary: { label: "Primary", hint: "Official" },
            secondary: { label: "Secondary", hint: "Reviews" },
            community: { label: "Community", hint: "Forums" },
          },
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
    DropdownMenuSeparator: () => <hr data-testid="menu-separator" />,
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

  it("renders leading secondary actions before fleet/sources (LRM-995)", () => {
    const onSelect = vi.fn();
    render(
      <ResearchSessionMetaMenu
        members={[member]}
        sources={[source]}
        leadingActions={[{ id: "delivery", label: "View delivery", onSelect }]}
      />,
    );
    expect(screen.getByTestId("menu-separator")).toBeInTheDocument();
    fireEvent.click(screen.getByText("View delivery"));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it("opens create params summary when session is provided (LRM-838)", () => {
    render(
      <ResearchSessionMetaMenu
        members={[member]}
        sources={[source]}
        session={{
          goal: "调研向量库\n\n【调研参数 depth=deep lang=zh primary=0.90 secondary=0.50 community=0.20】",
          depth_tier: "deep",
        }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Create parameters" }));
    expect(screen.getByTestId("research-session-params-summary")).toBeInTheDocument();
    expect(screen.getByText("Deep")).toBeInTheDocument();
    expect(screen.getByText("0.90")).toBeInTheDocument();
  });
});
