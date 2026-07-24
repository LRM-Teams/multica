// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enChannels from "../../locales/en/channels.json";
import type { DMItem } from "@multica/core/dm";

const TEST_RESOURCES = { en: { channels: enChannels } };

const mockViewport = vi.hoisted(() => ({ isMobile: false }));
const mockQueryData = vi.hoisted(() => ({
  dms: [] as DMItem[],
  dmsPending: false,
  agents: [] as Array<Record<string, unknown>>,
  members: [] as Array<Record<string, unknown>>,
}));
const openDMMocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
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
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({
    openDM: openDMMocks.openDM,
    isPending: openDMMocks.isPending,
  }),
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
    useQuery: (opts: { kind?: string }) => {
      const isDms = opts?.kind === "dms";
      return {
        data: isDms
          ? mockQueryData.dms
          : opts?.kind === "agents"
            ? mockQueryData.agents
            : mockQueryData.members,
        isLoading: isDms ? mockQueryData.dmsPending : false,
        isPending: isDms ? mockQueryData.dmsPending : false,
      };
    },
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
      state.open ? <div data-testid="dm-picker-content">{children}</div> : null,
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
import { CONVERSATION_SIDEBAR_ROW_ACTIVE } from "./conversation-sidebar-styles";

function makeDm(overrides: Partial<DMItem> = {}): DMItem {
  return {
    id: "dm-1",
    source: "dm_channel",
    peer: { type: "user", id: "peer-1", name: "Pinned Person" },
    unread: 0,
    updated_at: "2026-07-03T00:00:00Z",
    ...overrides,
  };
}

function seedPickerPeers() {
  mockQueryData.agents = [
    {
      id: "agent-1",
      name: "helper",
      display_name: "Helpful Bot",
      archived_at: null,
    },
    {
      id: "agent-archived",
      name: "retired",
      display_name: "Retired Bot",
      archived_at: "2026-01-01T00:00:00Z",
    },
  ];
  mockQueryData.members = [
    {
      user_id: "user-1",
      name: "me",
      display_name: "Current User",
    },
    {
      user_id: "user-2",
      name: "alice",
      display_name: "Alice",
    },
  ];
}

function renderDmList(props: Partial<Parameters<typeof DmList>[0]> = {}) {
  return render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <DmList activeId={null} currentUserName="Test User" onSelect={vi.fn()} {...props} />
    </I18nProvider>,
  );
}

function openDesktopPicker() {
  fireEvent.click(screen.getByText("Start a chat"));
  expect(screen.getByText("New message")).toBeInTheDocument();
}

describe("DmList new-DM picker", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.dmsPending = false;
    mockQueryData.agents = [];
    mockQueryData.members = [];
    openDMMocks.isPending = false;
    openDMMocks.openDM.mockReset();
    openDMMocks.openDM.mockResolvedValue(makeDm({ id: "dm-created" }));
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

  it("excludes pinned DMs from the list (they belong in the unified PINNED section)", () => {
    mockQueryData.dms = [
      makeDm({ id: "dm-pinned", pinned_at: "2026-07-03T00:00:00Z", peer: { type: "user", id: "p1", name: "Pinned Person" } }),
      makeDm({ id: "dm-free", peer: { type: "user", id: "p2", name: "Free Person" } }),
    ];

    renderDmList();

    expect(screen.queryByRole("button", { name: /Pinned Person/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Free Person/i })).toBeInTheDocument();
  });

  it("lists active agents and other members, excluding self and archived agents", () => {
    seedPickerPeers();
    renderDmList();
    openDesktopPicker();

    expect(screen.getByText("Helpful Bot")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.queryByText("Retired Bot")).not.toBeInTheDocument();
    expect(screen.queryByText("Current User")).not.toBeInTheDocument();
    // Type labels distinguish agent vs human peers.
    expect(screen.getByText("Agent")).toBeInTheDocument();
    expect(screen.getByText("Human")).toBeInTheDocument();
  });

  it("filters picker results by search query", () => {
    seedPickerPeers();
    renderDmList();
    openDesktopPicker();

    fireEvent.change(screen.getByPlaceholderText("Search people or agents…"), {
      target: { value: "alice" },
    });

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.queryByText("Helpful Bot")).not.toBeInTheDocument();
  });

  it("opens a DM with the selected peer and closes the picker on success", async () => {
    seedPickerPeers();
    renderDmList();
    openDesktopPicker();

    fireEvent.click(screen.getByText("Alice"));

    await waitFor(() => {
      expect(openDMMocks.openDM).toHaveBeenCalledWith({
        peer_type: "user",
        peer_id: "user-2",
      });
    });
    await waitFor(() => {
      expect(screen.queryByText("New message")).not.toBeInTheDocument();
    });
  });

  it("opens a DM with an agent peer", async () => {
    seedPickerPeers();
    renderDmList();
    openDesktopPicker();

    fireEvent.click(screen.getByText("Helpful Bot"));

    await waitFor(() => {
      expect(openDMMocks.openDM).toHaveBeenCalledWith({
        peer_type: "agent",
        peer_id: "agent-1",
      });
    });
  });

  it("keeps the picker open when openDM fails", async () => {
    openDMMocks.openDM.mockResolvedValueOnce(null);
    seedPickerPeers();
    renderDmList();
    openDesktopPicker();

    fireEvent.click(screen.getByText("Alice"));

    await waitFor(() => {
      expect(openDMMocks.openDM).toHaveBeenCalled();
    });
    expect(screen.getByText("New message")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
  });
});

// Anchor 7 (A4 / A6) — the row must surface the read-model `real_unread`-backed
// REAL count and present muted DMs silently, never a fabricated number.
describe("DmList unread affordance (read-model)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.agents = [];
    mockQueryData.members = [];
    openDMMocks.openDM.mockReset();
    openDMMocks.openDM.mockResolvedValue(makeDm({ id: "dm-created" }));
  });

  it("marks a real unread with a bold name + subtle dot, not a saturated count (#3 Slack-style)", () => {
    mockQueryData.dms = [
      makeDm({
        unread: 7,
        real_unread: 7,
        peer: { type: "user", id: "peer-1", name: "Unread Person" },
      }),
    ];

    const { container } = renderDmList();

    // No saturated count block — the unread signal is a subtle neutral dot plus
    // the bold channel name (the numeric block is reserved for @-mentions).
    expect(container.querySelector("span.bg-primary")).toBeNull();
    const dot = container.querySelector("span.size-2.rounded-full");
    expect(dot).not.toBeNull();
    expect(dot).toHaveClass("bg-muted-foreground");
    // The name reads bold on unread.
    const name = container.querySelector("span.font-semibold");
    expect(name).not.toBeNull();
    expect(name).toHaveTextContent("Unread Person");
  });

  it("shows a DIMMER dot for a muted DM — silent, no count, never louder than active (Parker)", () => {
    mockQueryData.dms = [makeDm({ unread: 4, real_unread: 4, muted: true })];

    const { container } = renderDmList();

    // Muted plain-unread is the quietest: a dimmer neutral dot, no numeric count.
    const dot = container.querySelector("span.size-2.rounded-full");
    expect(dot).not.toBeNull();
    expect(dot).toHaveClass("bg-muted-foreground/50");
    expect(container).not.toHaveTextContent("4");
    expect(container.querySelector("span.bg-primary")).toBeNull();
  });

  it("shows a manual-unread DOT, never the server's bumped '1' (Parker gate)", () => {
    // Manual unread bumps `unread` to 1 while `real_unread` stays 0 — the row
    // must render a marker dot, not a fake numeric badge.
    mockQueryData.dms = [
      makeDm({ unread: 1, real_unread: 0, manually_unread: true }),
    ];

    const { container } = renderDmList();

    expect(screen.queryByText("1")).not.toBeInTheDocument();
    const dot = container.querySelector("span.size-2.rounded-full");
    expect(dot).not.toBeNull();
  });
});

describe("DmList no Ask Wendy promo card (LRM-294)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.agents = [];
    mockQueryData.members = [];
  });

  it("does not render the promo card in the empty DM list", () => {
    mockQueryData.agents = [
      {
        id: "wendy-1",
        name: "wendy",
        display_name: "Wendy",
        archived_at: null,
        runtime_id: "rt-1",
      },
    ];

    renderDmList();

    expect(screen.queryByText("Ask Wendy")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Wendy can help you turn real work into useful agents."),
    ).not.toBeInTheDocument();
  });

  it("keeps Wendy as a normal DM row when a conversation exists", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-wendy",
        peer: { type: "agent", id: "wendy-1", name: "Wendy" },
      }),
    ];

    renderDmList();

    expect(screen.getByRole("button", { name: /Wendy/i })).toBeInTheDocument();
    expect(screen.queryByText("Ask Wendy")).not.toBeInTheDocument();
  });

  it("lists Wendy in the new-DM picker", () => {
    mockQueryData.agents = [
      {
        id: "wendy-1",
        name: "wendy",
        display_name: "Wendy",
        archived_at: null,
      },
    ];

    renderDmList();
    openDesktopPicker();

    expect(screen.getByText("Wendy")).toBeInTheDocument();
    expect(screen.getByText("Agent")).toBeInTheDocument();
  });
});

describe("DmList sidebar contrast (LRM-354)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.dmsPending = false;
    mockQueryData.agents = [];
    mockQueryData.members = [];
  });

  it("marks the active DM row with sidebar-accent (not primary wash)", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-active",
        peer: { type: "user", id: "peer-1", name: "Active Peer" },
      }),
    ];

    const { container } = renderDmList({ activeId: "dm-active" });
    const activeRow = container.querySelector(`.${CONVERSATION_SIDEBAR_ROW_ACTIVE}`);
    expect(activeRow).not.toBeNull();
    expect(container.querySelector(".bg-primary\\/\\[0\\.08\\]")).toBeNull();
  });

  it("shows collapsed section unread as a brand pill", () => {
    mockQueryData.dms = [
      makeDm({
        unread: 5,
        real_unread: 5,
        peer: { type: "user", id: "peer-1", name: "Unread Peer" },
      }),
    ];

    const { container } = renderDmList();
    fireEvent.click(screen.getByRole("button", { name: /Direct messages/i }));
    const badge = container.querySelector("span.bg-brand-solid.text-brand-solid-foreground");
    expect(badge).not.toBeNull();
    expect(badge).toHaveTextContent("5");
  });
});

describe("DmList loading skeleton (LRM-459)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.dmsPending = false;
    mockQueryData.agents = [];
    mockQueryData.members = [];
  });

  it("shows row skeleton while DM list is pending (not empty CTA)", () => {
    mockQueryData.dmsPending = true;
    renderDmList();

    expect(screen.getByTestId("dm-list-skeleton")).toBeInTheDocument();
    expect(screen.queryByText("Start a chat")).not.toBeInTheDocument();
  });

  it("replaces skeleton with rows once loaded", () => {
    mockQueryData.dmsPending = false;
    mockQueryData.dms = [
      makeDm({
        id: "dm-1",
        peer: { type: "user", id: "peer-1", name: "Loaded Peer" },
      }),
    ];
    renderDmList();

    expect(screen.queryByTestId("dm-list-skeleton")).not.toBeInTheDocument();
    expect(screen.getByText("Loaded Peer")).toBeInTheDocument();
  });

  it("does not flash empty CTA when pending with empty default data", () => {
    // Regression: isLoading=false while isPending=true used to paint empty state.
    mockQueryData.dmsPending = true;
    mockQueryData.dms = [];
    renderDmList();

    expect(screen.getByTestId("dm-list-skeleton")).toBeInTheDocument();
    expect(screen.queryByText("No direct messages yet.")).not.toBeInTheDocument();
  });
});
