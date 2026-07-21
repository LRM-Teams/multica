import { act, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// #568 — confirmed live bug: at a genuine 768px viewport (tablet), the
// two-pane desktop layout's detail header doesn't switch to any condensed
// pattern (MOBILE_BREAKPOINT is 768, and `useIsMobile` uses a strict `<`, so
// 768 itself stays on the desktop icon-row branch), but the detail pane at
// that width doesn't have room for the full action-icon row (at the time:
// member cluster + invite + search/share/stats/files/settings — all
// `shrink-0`, so they never yield space to the truncating title). The row
// overflowed past the viewport with 群设置 (Group Settings) physically
// unreachable — no scroll, no affordance to get to it. #831 (LRM-210) later
// folded Share/Stats/Files/Settings into a single "Channel details" entry
// point, shrinking the row to just member cluster + invite + Search — see
// the HEADER_ACTIONS_COMPACT_BREAKPOINT comment in channels-page.tsx for the
// re-measured threshold against that lighter, current composition.
//
// A design/product review blocker on the *original* fix (which gated this
// on `window.innerWidth`) caught that a global viewport breakpoint is the
// wrong signal here: the two-pane layout's list↔detail divider is
// user-draggable, so viewport width and the detail pane's actual rendered
// width can diverge in either direction — a wide viewport with the divider
// dragged narrow still overflows; a narrower viewport with a wide detail
// pane doesn't need to collapse. The fix now measures the detail pane's own
// box via `useContainerNarrowerThan` (packages/ui/hooks/use-mobile.ts, a
// `ResizeObserver`-driven sibling of `useIsNarrowerThan`), attached to the
// `<main>` that renders `channelConversationPane` (`detailHeaderContainerRef`
// in channels-page.tsx).
//
// This suite drives the REAL `useContainerNarrowerThan` — only `useIsMobile`
// is mocked. jsdom has no layout engine (every element's
// `getBoundingClientRect()` is 0x0 by default, and `ResizeObserver` isn't
// implemented at all), so both are faked below: `getBoundingClientRect` on
// the page's `<main>` reports a test-controlled width, and the fake
// `ResizeObserver` fires its callback whenever that width is changed,
// exactly like dragging the pane divider would trigger a real one.
const mobile = vi.hoisted(() => ({ value: false }));
vi.mock("@multica/ui/hooks/use-mobile", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/ui/hooks/use-mobile")>();
  return {
    ...actual,
    useIsMobile: () => mobile.value,
  };
});

type ROCallback = ConstructorParameters<typeof ResizeObserver>[0];
type RORegistration = { element: Element; callback: ROCallback };

const containerWidth = vi.hoisted(() => ({ value: 1024 }));
// `react-resizable-panels` (the "list"/"detail" ResizablePanelGroup) also
// constructs its own real `ResizeObserver`s to track panel sizing, on its
// own wrapper elements — NOT on our `<main>`. Track (element, callback)
// pairs, not just callbacks, so `resizeContainerTo` can fire only the
// observer watching our `<main>` and leave the panel library's own resize
// handling alone; broadcasting a synthetic entry shaped for our hook to
// *every* observer crashes react-resizable-panels' handler, which expects
// its own real entry shape (target/borderBoxSize).
const roRegistrations = vi.hoisted(() => new Set<RORegistration>());

class FakeResizeObserver {
  #callback: ROCallback;
  #owned = new Set<RORegistration>();
  constructor(callback: ROCallback) {
    this.#callback = callback;
  }
  observe(element: Element) {
    const registration = { element, callback: this.#callback };
    this.#owned.add(registration);
    roRegistrations.add(registration);
  }
  unobserve(element: Element) {
    for (const registration of this.#owned) {
      if (registration.element === element) {
        this.#owned.delete(registration);
        roRegistrations.delete(registration);
      }
    }
  }
  disconnect() {
    for (const registration of this.#owned) roRegistrations.delete(registration);
    this.#owned.clear();
  }
}

// Simulates the pane divider being dragged so the detail pane's `<main>`
// now renders at `width` — fires only the ResizeObserver callback watching
// that specific element (identified by tag, since only
// `detailHeaderContainerRef` in channels-page.tsx observes a `<main>`),
// exactly like a real one firing after a layout pass.
function resizeContainerTo(width: number) {
  containerWidth.value = width;
  act(() => {
    for (const { element, callback } of roRegistrations) {
      if (element.tagName !== "MAIN") continue;
      callback(
        [{ contentRect: { width }, target: element } as ResizeObserverEntry],
        null as unknown as ResizeObserver,
      );
    }
  });
}

beforeEach(() => {
  mobile.value = false;
  containerWidth.value = 1024;
  roRegistrations.clear();
  vi.stubGlobal("ResizeObserver", FakeResizeObserver as unknown as typeof ResizeObserver);
  // Only the page's own `<main>` (the element `detailHeaderContainerRef`
  // measures) reports the test-controlled width — everything else keeps
  // jsdom's real 0x0 box so unrelated positioning logic (Radix Popover /
  // vaul Drawer, which also read `getBoundingClientRect` on their own
  // elements) isn't perturbed by this fake.
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
    this: HTMLElement,
  ) {
    const width = this.tagName === "MAIN" ? containerWidth.value : 0;
    return {
      width,
      height: 0,
      top: 0,
      left: 0,
      right: width,
      bottom: 0,
      x: 0,
      y: 0,
      toJSON() {},
    } as DOMRect;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const apiMock = vi.hoisted(() => {
  const known: Record<string, unknown> = {};
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

const channelFixture = {
  id: "chan-1",
  workspace_id: "ws-1",
  name: "general",
  kind: "group" as const,
  description: null,
  lark_chat_id: null,
  created_by: "user-1",
  created_at: "2026-06-17T09:00:00Z",
  updated_at: "2026-06-17T09:00:00Z",
  archived_at: null as string | null,
};

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], [channelFixture]),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: () => options(["channel-members"], []),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ messages: [], next_cursor: null }),
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string; name: string } }) => unknown) =>
    selector({ user: { id: "user-1", name: "Alice" } }),
}));

vi.mock("@multica/core/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/hooks")>()),
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    channels: () => "/w/test/channels",
    channelDetail: (id: string) => `/w/test/channels/${id}`,
  }),
}));

vi.mock("@multica/core/realtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/realtime")>()),
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));

vi.mock("@multica/core/dm", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/dm")>()),
  dmListOptions: () => ({ queryKey: ["dm-list"], queryFn: async () => [] }),
}));

vi.mock("@multica/core/workspace/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workspace/queries")>()),
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [] }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: vi.fn(),
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));

// The real settings surface — asserting THIS renders (not just that a click
// handler fired) is what proves the compact "⋯" path actually reaches the
// channel Settings tab (LRM-210's ChannelDetailsPanel), matching how #576's
// own tests prove reachability.
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: (props: { disabled?: boolean }) => (
    <button type="button" disabled={props.disabled}>
      project
    </button>
  ),
}));

vi.mock("./dm-conversation", () => ({ DmConversation: () => <div /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: () => <div data-testid="message-list" />,
}));
vi.mock("./thread-panel", () => ({
  ThreadPanel: (props: { editor?: React.ReactNode }) => (
    <div data-testid="thread-panel">{props.editor}</div>
  ),
}));

function renderPage(channelId?: string) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage channelId={channelId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

// LRM-210 folded the old standalone Share/Stats/Files/Settings icon buttons
// into the "Channel details" panel (opened via the title / member cluster),
// so the direct row's own reachability marker is now the Search button —
// the one icon action that's still inline rather than behind that panel.
function expectCompact() {
  expect(screen.queryByRole("button", { name: "Search in conversation" })).toBeNull();
  expect(screen.getByTestId("channel-header-actions-trigger")).toBeInTheDocument();
}

function expectDirect() {
  expect(screen.getByRole("button", { name: "Search in conversation" })).toBeInTheDocument();
  expect(screen.queryByTestId("channel-header-actions-trigger")).toBeNull();
}

describe("ChannelsPage header actions — container-driven overflow (#568)", () => {
  it("switches compact/direct as the detail pane's own container is resized narrow → wide → narrow, at a fixed (never-mocked) viewport", async () => {
    // Starts with room (above HEADER_ACTIONS_COMPACT_BREAKPOINT) — direct row.
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expectDirect();

    // Drag the divider so the detail pane narrows below the threshold —
    // same container, same (unmocked) viewport, only the container's own
    // rendered width changed. 450px is comfortably below
    // HEADER_ACTIONS_COMPACT_BREAKPOINT (520, re-measured live post-#831 —
    // see the comment on the constant in channels-page.tsx).
    resizeContainerTo(450);
    expectCompact();

    // Drag back out — the row must return, not stay stuck compact.
    resizeContainerTo(1024);
    expectDirect();

    // And narrow again, to prove it isn't a one-shot mount-time decision.
    resizeContainerTo(450);
    expectCompact();
  });

  // The key regression test: a wide viewport does NOT imply room, because
  // the detail pane is independently resizable. This is exactly the
  // scenario the original viewport-gated fix missed.
  it("triggers compact mode from a narrow detail-pane container even at a wide (1440px) viewport", async () => {
    const originalInnerWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 1440 });
    try {
      resizeContainerTo(450);
      renderPage();
      await screen.findByTestId("message-list");

      expectCompact();
    } finally {
      Object.defineProperty(window, "innerWidth", {
        configurable: true,
        value: originalInnerWidth,
      });
    }
  });

  // True-mobile regression guard: the single-pane Drawer entry point must
  // keep working exactly as before, regardless of what the (irrelevant, in
  // that layout) detail-pane container width happens to measure.
  it("keeps the existing true-mobile collapsed trigger unaffected by container width", async () => {
    mobile.value = true;
    resizeContainerTo(1024); // deliberately "wide" — must not matter on mobile.
    renderPage(channelFixture.id);
    await screen.findByTestId("message-list");

    expectCompact();
  });

  // Desktop regression guard: plenty of room, no narrowing at any point —
  // the full icon row renders directly, exactly as before this fix.
  it("still shows the full icon row directly on a wide, unresized desktop container", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");

    expectDirect();
  });

  // Reachability guard (kept from the original #568 fix, updated for
  // LRM-210's Channel details panel): the compact trigger must actually
  // open the same overflow Drawer the true-mobile flow already uses, and
  // its "Settings" menu item must reach the same ChannelDetailsPanel
  // settings tab (project binding etc.) the direct row's title/member
  // click reaches on a wide container — not just render inertly.
  it("reaches channel settings through the compact trigger when the container is narrow", async () => {
    resizeContainerTo(450);
    renderPage();
    await screen.findByTestId("message-list");

    const trigger = screen.getByTestId("channel-header-actions-trigger");
    fireEvent.click(trigger);

    const settingsRow = await screen.findByRole("button", { name: "Settings" });
    fireEvent.click(settingsRow);

    expect(await screen.findByRole("button", { name: "project" })).toBeInTheDocument();
  });
});
