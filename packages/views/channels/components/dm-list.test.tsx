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
const mockBubbleActivity = vi.hoisted(() => ({
  byAgent: new Map<string, { unreadCount: number; latestUpdatedAt: string | null }>(),
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

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getMemberHonor: () => undefined,
    getAgentFleetRank: () => undefined,
  }),
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

vi.mock("../../chat/lib/agent-bubble-unread", () => ({
  useAgentBubbleActivityByAgent: () => mockBubbleActivity.byAgent,
  // Keep LRM-762 gate in the mock so agent_pair rows never inherit bubble
  // unread/time from the projected peer agent.
  dmAgentBubbleActivity: (
    dm: { mode?: string; peer: { type: string; id: string } },
    byAgent: Map<string, { unreadCount: number; latestUpdatedAt: string | null }>,
  ) => {
    if (dm.mode === "agent_pair" || dm.peer.type !== "agent") return null;
    return byAgent.get(dm.peer.id) ?? null;
  },
}));

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../i18n/time", () => ({
  Time: ({ kind, value }: { kind: string; value: string }) => (
    <time data-kind={kind}>{value}</time>
  ),
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
import { resetSidebarSectionCollapsedMemoryForTests } from "../hooks/use-sidebar-section-collapsed";

beforeEach(() => {
  resetSidebarSectionCollapsedMemoryForTests();
  window.sessionStorage.clear();
  mockBubbleActivity.byAgent = new Map();
});

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
      <DmList
        activeId={null}
        currentUserName="Test User"
        onSelect={vi.fn()}
        dms={mockQueryData.dms}
        dmsPending={mockQueryData.dmsPending}
        {...props}
      />
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
    mockBubbleActivity.byAgent = new Map();
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

  it("marks a real unread with a bold name + numeric badge with the real count (LRM-767)", () => {
    mockQueryData.dms = [
      makeDm({
        unread: 7,
        real_unread: 7,
        peer: { type: "user", id: "peer-1", name: "Unread Person" },
      }),
    ];

    const { container } = renderDmList();

    // LRM-767 (Slack-aligned): active unread shows the real count in a neutral
    // pill — no brand/destructive accent (reserved for @-mentions).
    expect(container.querySelector("span.bg-primary")).toBeNull();
    const badge = container.querySelector("span.bg-muted");
    expect(badge).not.toBeNull();
    expect(badge).toHaveTextContent("7");
    // The name reads bold on unread.
    const name = container.querySelector("span.font-semibold");
    expect(name).not.toBeNull();
    expect(name).toHaveTextContent("Unread Person");
  });

  it("shows NO badge for a muted DM — the bold name is the only unread signal (LRM-767)", () => {
    mockQueryData.dms = [makeDm({ unread: 4, real_unread: 4, muted: true })];

    const { container } = renderDmList();

    // Muted plain-unread shows nothing on the right; the name still reads bold.
    expect(container).not.toHaveTextContent("4");
    expect(container.querySelector("span.bg-primary")).toBeNull();
    expect(container.querySelector("span.size-2.rounded-full")).toBeNull();
    expect(container.querySelector("span.font-semibold")).not.toBeNull();
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


describe("DmList bubble activity ordering", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.dmsPending = false;
    mockQueryData.agents = [];
    mockQueryData.members = [];
    mockBubbleActivity.byAgent = new Map();
  });

  it("keeps a recently replied agent row above older DM rows after unread is cleared", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-human",
        peer: { type: "user", id: "user-2", name: "Human Peer" },
        updated_at: "2026-07-30T10:00:00Z",
      }),
      makeDm({
        id: "dm-agent",
        peer: { type: "agent", id: "agent-1", name: "Agent Peer" },
        updated_at: "2026-07-29T10:00:00Z",
      }),
    ];
    mockBubbleActivity.byAgent = new Map([
      ["agent-1", { unreadCount: 0, latestUpdatedAt: "2026-07-30T11:00:00Z" }],
    ]);

    renderDmList();

    const agent = screen.getByText("Agent Peer");
    const human = screen.getByText("Human Peer");
    expect(agent.compareDocumentPosition(human) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByText("Bubble replied")).not.toBeInTheDocument();
  });

  it("still shows the bubble replied preview while the agent has unread bubble replies", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-agent",
        peer: { type: "agent", id: "agent-1", name: "Agent Peer" },
        last_message: {
          type: "agent",
          author_name: "Agent Peer",
          content: "channel last",
          created_at: "2026-07-29T10:00:00Z",
        },
      }),
    ];
    mockBubbleActivity.byAgent = new Map([
      ["agent-1", { unreadCount: 1, latestUpdatedAt: "2026-07-30T11:00:00Z" }],
    ]);

    renderDmList();

    expect(screen.getByText("Bubble replied")).toBeInTheDocument();
    // LRM-762/763: list time follows bubble session clock via <Time kind="list">,
    // not the hard-coded「刚刚」/just now string.
    expect(screen.queryByText("just now")).not.toBeInTheDocument();
    expect(screen.getByText("2026-07-30T11:00:00Z")).toBeInTheDocument();
  });

  it("does not paint bubble「刚刚」/unread onto supervised agent_pair rows (LRM-762)", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-pair",
        mode: "agent_pair",
        supervised: true,
        has_mention: true,
        peer: { type: "agent", id: "agent-1", name: "Front End" },
        participants: [
          { type: "agent", id: "agent-1", name: "Front End" },
          { type: "agent", id: "agent-2", name: "Beckham" },
        ],
        unread: 0,
        real_unread: 0,
        last_message: {
          type: "agent",
          author_name: "Beckham",
          content: "yesterday note",
          created_at: "2026-07-29T10:11:00Z",
        },
      }),
    ];
    mockBubbleActivity.byAgent = new Map([
      ["agent-1", { unreadCount: 3, latestUpdatedAt: "2026-07-30T12:00:00Z" }],
    ]);

    renderDmList();

    expect(screen.queryByText("Bubble replied")).not.toBeInTheDocument();
    expect(screen.queryByText("just now")).not.toBeInTheDocument();
    expect(screen.getByText(/yesterday note/)).toBeInTheDocument();
    expect(screen.getByText("2026-07-29T10:11:00Z")).toBeInTheDocument();
    expect(screen.queryByText("2026-07-30T12:00:00Z")).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/3 unread/i)).not.toBeInTheDocument();
  });
});

describe("DmList sidebar contrast (LRM-354)", () => {
  beforeEach(() => {
    resetSidebarSectionCollapsedMemoryForTests();
    window.sessionStorage.clear();
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

    const { container, unmount } = renderDmList();
    fireEvent.click(screen.getByRole("button", { name: /Direct messages/i }));
    const badge = container.querySelector("span.bg-brand-solid.text-brand-solid-foreground");
    expect(badge).not.toBeNull();
    expect(badge).toHaveTextContent("5");
    unmount();
  });

  it("keeps DIRECT MESSAGES collapsed after remount (LRM-655)", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-1",
        peer: { type: "user", id: "peer-1", name: "Peer" },
      }),
    ];

    const first = renderDmList();
    fireEvent.click(screen.getByRole("button", { name: /Direct messages/i }));
    expect(
      first.container.querySelector('[aria-expanded="false"]'),
    ).not.toBeNull();
    // Row body gone while collapsed.
    expect(screen.queryByText("Peer")).toBeNull();
    first.unmount();

    // Simulate ChannelsPage remount on channel select — collapse must stick.
    const second = renderDmList();
    expect(
      second.container.querySelector('[aria-expanded="false"]'),
    ).not.toBeNull();
    expect(screen.queryByText("Peer")).toBeNull();
    second.unmount();
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

describe("DmList duplicate agent display names (LRM-749)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dms = [];
    mockQueryData.dmsPending = false;
    mockQueryData.members = [];
    mockQueryData.agents = [
      { id: "agent-b1", name: "beckham-lrm2", display_name: "贝克汉姆", archived_at: null },
      { id: "agent-b2", name: "beckham-ops", display_name: "贝克汉姆", archived_at: null },
      { id: "agent-w", name: "wendy", display_name: "Wendy", archived_at: null },
    ];
    mockQueryData.dms = [
      makeDm({ id: "dm-b1", peer: { type: "agent", id: "agent-b1", name: "贝克汉姆" } }),
      makeDm({ id: "dm-b2", peer: { type: "agent", id: "agent-b2", name: "贝克汉姆" } }),
      makeDm({ id: "dm-w", peer: { type: "agent", id: "agent-w", name: "Wendy" } }),
    ];
  });

  it("shows a weak gray @handle beside colliding agent rows only", () => {
    renderDmList();
    expect(screen.getByText("@beckham-lrm2")).toBeInTheDocument();
    expect(screen.getByText("@beckham-ops")).toBeInTheDocument();
    expect(screen.queryByText("@wendy")).not.toBeInTheDocument();
    const handle = screen.getByText("@beckham-lrm2");
    expect(handle.className).toContain("text-muted-foreground");
  });

  it("drops the handle once the other same-name agent is archived", () => {
    mockQueryData.agents = [
      { id: "agent-b1", name: "beckham-lrm2", display_name: "贝克汉姆", archived_at: null },
      {
        id: "agent-b2",
        name: "beckham-ops",
        display_name: "贝克汉姆",
        archived_at: "2026-07-01T00:00:00Z",
      },
    ];
    mockQueryData.dms = [
      makeDm({ id: "dm-b1", peer: { type: "agent", id: "agent-b1", name: "贝克汉姆" } }),
    ];
    renderDmList();
    expect(screen.queryByText("@beckham-lrm2")).not.toBeInTheDocument();
  });

  it("never adds a handle to human peer rows", () => {
    mockQueryData.dms = [
      makeDm({ id: "dm-human", peer: { type: "user", id: "user-9", name: "贝克汉姆" } }),
    ];
    renderDmList();
    expect(screen.queryByText("@beckham-lrm2")).not.toBeInTheDocument();
    expect(screen.queryByText("@beckham-ops")).not.toBeInTheDocument();
  });
});

describe("DmList agent_pair row menu (LRM-752)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dmsPending = false;
    mockQueryData.members = [];
    mockQueryData.agents = [
      { id: "agent-a", name: "alpha", display_name: "Alpha Bot", archived_at: null },
      { id: "agent-b", name: "beta", display_name: "Beta Bot", archived_at: null },
    ];
    mockQueryData.dms = [
      makeDm({
        id: "dm-pair",
        mode: "agent_pair",
        supervised: true,
        peer: { type: "agent", id: "agent-a", name: "Alpha Bot" },
        participants: [
          { type: "agent", id: "agent-a", name: "Alpha Bot" },
          { type: "agent", id: "agent-b", name: "Beta Bot" },
        ],
      }),
    ];
  });

  it("renders the ⋯ menu with all four list-preference actions on agent_pair rows", async () => {
    renderDmList();
    fireEvent.click(screen.getByRole("button", { name: /Agent collaboration/ }));
    expect(screen.getByText("Alpha Bot · Beta Bot")).toBeInTheDocument();

    const trigger = screen.getByRole("button", { name: "Direct message actions" });
    fireEvent.click(trigger);

    expect(await screen.findByText("Mark as unread")).toBeInTheDocument();
    expect(screen.getByText("Pin")).toBeInTheDocument();
    expect(screen.getByText("Mute notifications")).toBeInTheDocument();
    expect(screen.getByText("Close chat")).toBeInTheDocument();
  });
});

describe("DmList supervised Agent DM folder (LRM-764)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dmsPending = false;
    mockQueryData.agents = [];
    mockQueryData.members = [];
    mockQueryData.dms = [
      makeDm({
        id: "dm-human",
        peer: { type: "user", id: "user-2", name: "Human Peer" },
      }),
      makeDm({
        id: "dm-agent-pair",
        mode: "agent_pair",
        supervised: true,
        peer: { type: "agent", id: "agent-a", name: "Alice · Bob" },
        participants: [
          { type: "agent", id: "agent-a", name: "Alice" },
          { type: "agent", id: "agent-b", name: "Bob" },
        ],
        unread: 3,
        real_unread: 3,
      }),
      makeDm({
        id: "dm-agent-pair-mention",
        mode: "agent_pair",
        supervised: true,
        has_mention: true,
        peer: { type: "agent", id: "agent-c", name: "Carol · Dave" },
        participants: [
          { type: "agent", id: "agent-c", name: "Carol" },
          { type: "agent", id: "agent-d", name: "Dave" },
        ],
        unread: 1,
        real_unread: 1,
      }),
    ];
  });

  it("keeps direct DMs visible and folds supervised Agent pairs by default", () => {
    renderDmList();
    expect(screen.getByText("Human Peer")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Agent collaboration/ })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.queryByText("Alice · Bob")).not.toBeInTheDocument();
  });

  it("keeps @-mention agent pairs flat (not folded) per LRM-764", () => {
    renderDmList();
    // Mentioned pair stays visible while the folder is collapsed.
    expect(screen.getByText("Carol · Dave")).toBeInTheDocument();
    expect(screen.queryByText("Alice · Bob")).not.toBeInTheDocument();
  });

  it("expands the folder to reveal the authorized Agent pair", () => {
    renderDmList();
    fireEvent.click(screen.getByRole("button", { name: /Agent collaboration/ }));
    expect(screen.getByText("Alice · Bob")).toBeInTheDocument();
  });
});

/**
 * LRM-1366 — the DIRECT MESSAGES region must never settle into "heading + `+`
 * and nothing else". Pinned DMs live in the unified PINNED section above, so
 * `dms.length > 0` with zero rows *in this list* is a reachable steady state
 * (all pinned, or a search that only matches pinned rows) and used to render an
 * empty fragment: a silent hole indistinguishable from a broken list.
 */
describe("DmList never renders a silent empty region (LRM-1366)", () => {
  beforeEach(() => {
    mockViewport.isMobile = false;
    mockQueryData.dmsPending = false;
    mockQueryData.agents = [];
    mockQueryData.members = [];
    mockQueryData.dms = [];
  });

  it("points at the PINNED section when every DM is pinned", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-pinned",
        pinned_at: "2026-08-04T00:00:00Z",
        peer: { type: "user", id: "p1", name: "Pinned Person" },
      }),
    ];

    renderDmList();

    expect(screen.getByTestId("dm-list-all-pinned")).toBeInTheDocument();
    // Not the "no DMs at all" state — the viewer does have conversations.
    expect(screen.queryByText("No direct messages yet.")).not.toBeInTheDocument();
  });

  it("shows the no-match hint when a search only leaves pinned DMs", () => {
    mockQueryData.dms = [
      makeDm({
        id: "dm-pinned",
        pinned_at: "2026-08-04T00:00:00Z",
        peer: { type: "user", id: "p1", name: "Pinned Person" },
      }),
    ];

    renderDmList({ searchQuery: "zzz" });

    expect(screen.getByText("No matching conversations")).toBeInTheDocument();
    expect(screen.queryByTestId("dm-list-all-pinned")).not.toBeInTheDocument();
  });

  it("still offers the empty CTA when the viewer has no DMs at all", () => {
    renderDmList();

    expect(screen.getByText("No direct messages yet.")).toBeInTheDocument();
    expect(screen.getByText("Start a chat")).toBeInTheDocument();
    expect(screen.queryByTestId("dm-list-all-pinned")).not.toBeInTheDocument();
  });

  it("keeps the skeleton — not a hint — while the DM query is pending", () => {
    mockQueryData.dmsPending = true;

    renderDmList();

    expect(screen.getByTestId("dm-list-skeleton")).toBeInTheDocument();
    expect(screen.queryByTestId("dm-list-all-pinned")).not.toBeInTheDocument();
    expect(screen.queryByText("No direct messages yet.")).not.toBeInTheDocument();
  });
});
