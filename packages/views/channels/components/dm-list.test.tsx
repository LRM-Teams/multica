// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enChannels from "../../locales/en/channels.json";
import type { DMItem } from "@multica/core/dm";

const TEST_RESOURCES = { en: { channels: enChannels } };

const mockViewport = vi.hoisted(() => ({ isMobile: false }));
const mockQueryData = vi.hoisted(() => ({
  dms: [] as DMItem[],
  agents: [],
  members: [],
  squads: [],
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mockViewport.isMobile,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: { user: { id: string } }) => unknown) => {
      const state = { user: { id: "user-1" } };
      return selector ? selector(state) : state;
    },
    { getState: () => ({ user: { id: "user-1" } }) },
  ),
}));

vi.mock("@multica/core/dm", () => ({
  dmListOptions: () => ({ kind: "dms" as const }),
  useSetDMPinned: () => ({ mutate: vi.fn() }),
  useMarkDMUnread: () => ({ mutate: vi.fn() }),
  useCloseDM: () => ({ mutate: vi.fn() }),
  useMuteDM: () => ({ mutate: vi.fn() }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ kind: "agents" as const }),
  memberListOptions: () => ({ kind: "members" as const }),
  squadListOptions: () => ({ kind: "squads" as const }),
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: vi.fn(), isPending: false }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <div data-testid={`avatar-${actorId}`} />
  ),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQuery: (opts: { kind?: string }) => ({
      data:
        opts?.kind === "dms"
          ? mockQueryData.dms
          : opts?.kind === "agents"
            ? mockQueryData.agents
            : opts?.kind === "squads"
              ? mockQueryData.squads
              : mockQueryData.members,
      isLoading: false,
    }),
  };
});

vi.mock("@multica/ui/components/ui/popover", () => {
  const state = {
    open: false,
    onOpenChange: undefined as ((open: boolean) => void) | undefined,
  };
  return {
    Popover: ({
      children,
      open,
      onOpenChange,
    }: {
      children: ReactNode;
      open?: boolean;
      onOpenChange?: (open: boolean) => void;
    }) => {
      state.open = open ?? false;
      state.onOpenChange = onOpenChange;
      return (
        <div data-testid="dm-picker-popover" data-open={String(state.open)}>
          {children}
        </div>
      );
    },
    PopoverTrigger: ({ render, children }: { render?: ReactNode; children?: ReactNode }) => (
      <div role="presentation" onClick={() => state.onOpenChange?.(true)}>
        {render}
        {children}
      </div>
    ),
    PopoverContent: ({ children }: { children: ReactNode }) =>
      state.open ? <div>{children}</div> : null,
  };
});

vi.mock("@multica/ui/components/ui/drawer", () => ({
  Drawer: ({ children, open }: { children: ReactNode; open?: boolean }) =>
    open ? <div data-testid="dm-picker-drawer">{children}</div> : null,
  DrawerContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DrawerHeader: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DrawerTitle: ({ children }: { children: ReactNode }) => <h2>{children}</h2>,
}));

import { DmList } from "./dm-list";

function renderDmList() {
  return render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <DmList activeId={null} currentUserName="Test User" onSelect={vi.fn()} />
    </I18nProvider>,
  );
}

function makeDm(overrides: Partial<DMItem>): DMItem {
  return {
    id: "dm-1",
    source: "dm_channel",
    peer: { type: "agent", id: "agent-1", name: "Product Manager" },
    unread: 0,
    updated_at: "2026-07-02T00:00:00Z",
    ...overrides,
  };
}

describe("DmList new-DM picker", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.agents = [];
    mockQueryData.members = [];
    mockQueryData.squads = [];
  });

  it("opens the desktop picker from the empty-state CTA", () => {
    renderDmList();

    expect(screen.queryByText("New message")).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("Start a chat"));

    expect(screen.getByText("New message")).toBeInTheDocument();
  });

  it("opens the mobile drawer from the empty-state CTA", () => {
    mockViewport.isMobile = true;
    renderDmList();

    expect(screen.queryByTestId("dm-picker-drawer")).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("Start a chat"));

    expect(screen.getByTestId("dm-picker-drawer")).toBeInTheDocument();
    expect(screen.getByText("New message")).toBeInTheDocument();
  });

  it("hides legacy_session rows from the visible direct-message list", () => {
    mockQueryData.dms = [
      makeDm({
        id: "legacy-1",
        source: "legacy_session",
        peer: { type: "agent", id: "agent-legacy", name: "Old Chat Surface" },
      }),
      makeDm({
        id: "channel-1",
        source: "dm_channel",
        peer: { type: "agent", id: "agent-channel", name: "R2 DM Surface" },
      }),
    ];

    renderDmList();

    expect(screen.getByText("R2 DM Surface")).toBeInTheDocument();
    expect(screen.queryByText("Old Chat Surface")).not.toBeInTheDocument();
  });
});
