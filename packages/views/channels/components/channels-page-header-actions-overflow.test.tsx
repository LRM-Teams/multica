import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// #568 — confirmed live bug (deployed dev tip 9cb4ee3d4a42e972a9d8100e959c3a1e60488222,
// re-confirmed live via agent-browser after #831/LRM-210): at 768px/900px, the
// two-pane layout's header action row (member cluster + invite + search) shows
// no overflow while the Channel Details panel (`ChannelDetailsPanel`
// `variant="panel"`, a docked `ResizablePanel` sibling of the conversation
// pane) is CLOSED — but opening it squeezes the conversation `<main>` further,
// and the row (all `shrink-0`, never yields space to the truncating title)
// overflows for real: live-measured `header.scrollWidth (206) > clientWidth
// (61 at 768px / 145 at 900px)`.
//
// Fix: `useContainerNarrowerThan` (packages/ui/hooks/use-mobile.ts, a
// `ResizeObserver`-driven sibling of `useIsMobile`) attached to the
// conversation `<main>` (`detailHeaderContainerRef` in channels-page.tsx),
// compared against `HEADER_ACTIONS_COMPACT_BREAKPOINT` (360) — a FIXED
// constant derived from a live binary-search measurement of the direct row's
// own natural/worst-case width requirement (see the constant's comment in
// channels-page.tsx), never from the row's own current rendered
// `scrollWidth`. Below the breakpoint, the row collapses into the same "⋯"
// trigger + bottom Drawer the true-mobile path already used.
//
// jsdom has no layout engine (every element is 0x0 by default, and
// `ResizeObserver` isn't implemented at all), so both are faked below:
// `getBoundingClientRect` on the page's `<main>` reports a test-controlled
// width, and a fake `ResizeObserver` fires its callback whenever that width
// is changed — exactly like a real one firing after a layout pass (viewport
// resize, divider drag, OR a docked panel opening/closing, since all three
// ultimately change the SAME element's own rendered box). Only `useIsMobile`
// is mocked; `useContainerNarrowerThan` runs for real against the faked
// primitives.
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
// `react-resizable-panels` (the "list"/"detail" AND "conversation"/aside
// `ResizablePanelGroup`s) also construct their own real `ResizeObserver`s on
// their own wrapper elements — NOT on our `<main>`. Track (element,
// callback) pairs, not just callbacks, so `resizeContainerTo` fires only the
// observer watching our `<main>` and leaves the panel library's own resize
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

// Simulates the conversation `<main>`'s rendered box changing to `width` —
// whether that's a viewport resize, a divider drag, or (the #568 bug) the
// Channel Details panel docking open and squeezing the SAME element — and
// fires only the ResizeObserver callback watching that specific element
// (identified by tag, since only `detailHeaderContainerRef` observes a
// `<main>`), exactly like a real observer notification.
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
  channelName.value = "general-team";
  channelDescription.value = null;
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

// A normal (non-system) group channel WITH members, so the header renders
// its LRM-447 wide rail: Members chip + Search + Stop (Invite is not on-rail).
const channelName = vi.hoisted(() => ({ value: "general-team" }));
const channelDescription = vi.hoisted(() => ({ value: null as string | null }));
const channelFixture = {
  id: "chan-1",
  workspace_id: "ws-1",
  get name() {
    return channelName.value;
  },
  kind: "group" as const,
  get description() {
    return channelDescription.value;
  },
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
    channelMembersOptions: () =>
      options(["channel-members"], [
        {
          member_type: "user",
          member_id: "user-2",
          name: "bob",
          display_name: "Bob",
          avatar_url: null,
        },
      ]),
    channelProjectOptions: () => options(["channel-project"], ""),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ items: [], next_cursor: null }),
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

vi.mock("@multica/core/auth", async () => {
  const { authMock } = await import("./__fixtures/channels-page-mocks");
  return authMock();
});

vi.mock("@multica/core/hooks", async (importOriginal) => {
  const { hooksMock } = await import("./__fixtures/channels-page-mocks");
  return hooksMock(importOriginal);
});

vi.mock("@multica/core/paths", async (importOriginal) => {
  const { pathsMock } = await import("./__fixtures/channels-page-mocks");
  return pathsMock(importOriginal);
});

vi.mock("@multica/core/realtime", async (importOriginal) => {
  const { realtimeMock } = await import("./__fixtures/channels-page-mocks");
  return realtimeMock(importOriginal);
});

vi.mock("@multica/core/hooks/use-file-upload", async () => {
  const { fileUploadMock } = await import("./__fixtures/channels-page-mocks");
  return fileUploadMock();
});

vi.mock("@multica/core/dm", async (importOriginal) => {
  const { dmMock } = await import("./__fixtures/channels-page-mocks");
  return dmMock(importOriginal);
});

vi.mock("@multica/core/conversations", async (importOriginal) => {
  const { conversationsMock } = await import("./__fixtures/channels-page-mocks");
  return conversationsMock(importOriginal, () => [channelFixture]);
});

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

vi.mock("../../editor/lazy-content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: (props: { disabled?: boolean }) => (
    <button type="button" disabled={props.disabled}>
      project
    </button>
  ),
}));
vi.mock("./dm-conversation", () => ({ DmConversation: () => <div /> }));
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
        <ChannelsPage channelId={channelId ?? channelFixture.id} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

// The direct row's own reachability marker is Search — the one icon action
// that's still inline (not behind the Channel details panel, post-#831).
function expectCompact() {
  expect(screen.queryByRole("button", { name: "Search in conversation" })).toBeNull();
  expect(screen.getByRole("button", { name: "More" })).toBeInTheDocument();
}

function expectDirect() {
  expect(screen.getByRole("button", { name: "Search in conversation" })).toBeInTheDocument();
  expect(screen.getByTestId("channel-header-action-rail")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "More" })).toBeNull();
  expect(screen.queryByLabelText("Invite people")).toBeNull();
}

describe("ChannelsPage header actions — container-driven overflow (#568)", () => {
  it("switches compact/direct as the conversation pane's own container is resized narrow → wide → narrow, at a fixed (never-mocked) viewport", async () => {
    // Starts with room (above HEADER_ACTIONS_COMPACT_BREAKPOINT) — direct row.
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expectDirect();

    // Same container narrows below the threshold — same (unmocked)
    // viewport, only the container's own rendered width changed. 300px is
    // comfortably below HEADER_ACTIONS_COMPACT_BREAKPOINT (360 — see the
    // comment on the constant in channels-page.tsx for the live-measured
    // derivation).
    resizeContainerTo(300);
    expectCompact();

    // Widen back out — the row must return, not stay stuck compact.
    resizeContainerTo(1024);
    expectDirect();

    // And narrow again, to prove it isn't a one-shot mount-time decision.
    resizeContainerTo(300);
    expectCompact();
  });

  // The key regression test: a wide viewport does NOT imply room, because
  // the conversation pane is independently resizable.
  it("triggers compact mode from a narrow container even at a wide (1440px) viewport", async () => {
    const originalInnerWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 1440 });
    try {
      resizeContainerTo(300);
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

  // #568's actual reported bug: overflow only manifests once the Channel
  // Details panel is genuinely OPEN (real DOM — `ChannelDetailsPanel` is
  // NOT mocked in this suite, so its docked `<aside>` really mounts
  // alongside the conversation pane), squeezing the SAME conversation
  // `<main>` the header lives in. Open it through the real UI (not by
  // poking internal state), then simulate the resulting container squeeze
  // (768px-viewport-equivalent — live-measured 61-145px, well under the
  // 360px breakpoint) exactly like the docked aside taking width would.
  it("stays overflow-safe (collapses to the compact trigger) once the real Channel Details panel is opened and squeezes the conversation pane", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expectDirect();

    fireEvent.click(screen.getByLabelText("Open channel details"));
    expect(
      await screen.findByTestId("channel-details-home", {}, { timeout: 8000 }),
    ).toBeInTheDocument();
    // Panel just opened — still plenty of (mocked) room, so the row hasn't
    // had a reason to collapse yet; this isolates "panel is open" from
    // "container got squeezed" as two independent inputs to the decision.
    expectDirect();

    // The panel opening is what squeezes the conversation pane in the real
    // app; simulate that squeeze landing on the same observed `<main>`.
    resizeContainerTo(120);
    expectCompact();

    // And the compact trigger must still reach the same panel that's
    // already open — proving the two entry points (title / compact
    // trigger) aren't fighting each other.
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(await screen.findByTestId("channel-details-settings")).toBeInTheDocument();
  });

  // Coordinator requirement: the decision must be a pure function of the
  // CURRENT container width, not a self-referential function of the row's
  // own last rendered state — so repeated ResizeObserver ticks reporting
  // the SAME width (as real observers commonly do around a stable layout)
  // must not thrash between compact and direct.
  it("does not flap direct→compact→direct on repeated same-width ResizeObserver ticks", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expectDirect();

    // Below the breakpoint — collapses once.
    resizeContainerTo(300);
    expectCompact();
    // Same width reported again (and again) — a real observer often fires
    // more than once for a settled size (e.g. one tick per layout pass
    // during an animation). Must stay compact every time, not bounce.
    resizeContainerTo(300);
    expectCompact();
    resizeContainerTo(300);
    expectCompact();

    // Above the breakpoint — switches once.
    resizeContainerTo(1024);
    expectDirect();
    resizeContainerTo(1024);
    expectDirect();
  });

  // True-mobile regression guard: the single-pane Drawer entry point must
  // keep working exactly as before, regardless of what the (irrelevant, in
  // that layout) conversation-pane container width happens to measure.
  it("keeps the existing true-mobile collapsed trigger unaffected by container width", async () => {
    mobile.value = true;
    resizeContainerTo(1024); // deliberately "wide" — must not matter on mobile.
    renderPage();
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

  // Reachability guard: the compact trigger must open the LRM-494 Slack
  // channel-details page, and its Settings row must reach the same
  // ChannelDetailsPanel settings body (project binding etc.).
  it("reaches channel settings through the compact trigger when the container is narrow", async () => {
    resizeContainerTo(300);
    renderPage();
    await screen.findByTestId("message-list");

    const trigger = screen.getByRole("button", { name: "More" });
    fireEvent.click(trigger);

    const settingsRow = await screen.findByTestId("channel-details-settings");
    fireEvent.click(settingsRow);

    expect(await screen.findByRole("button", { name: "project" })).toBeInTheDocument();
  });

  // Exact-boundary guard: the contract is `< HEADER_ACTIONS_COMPACT_BREAKPOINT`
  // (360), so 359 must collapse and 360/361 must not — and each side must be
  // stable under repeated same-width ResizeObserver ticks right at the edge,
  // not just "far from the boundary" (which the earlier no-flap test only
  // proved at 300/1024).
  it("resolves the exact 360px boundary correctly and stays stable under repeated ticks at 359/360/361", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expectDirect();

    // 359 — one px below the breakpoint — must collapse, and stay collapsed
    // across repeated identical-width ticks.
    resizeContainerTo(359);
    expectCompact();
    resizeContainerTo(359);
    expectCompact();
    resizeContainerTo(359);
    expectCompact();

    // 360 — exactly at the breakpoint — contract is `< 360`, so this is
    // "enough room," must be direct, and stay direct across repeated ticks.
    resizeContainerTo(360);
    expectDirect();
    resizeContainerTo(360);
    expectDirect();
    resizeContainerTo(360);
    expectDirect();

    // 361 — one px above — must also be direct, stable across repeated ticks.
    resizeContainerTo(361);
    expectDirect();
    resizeContainerTo(361);
    expectDirect();
  });

  // Single-flip guard right at the edge: crossing the boundary in either
  // direction switches exactly once per crossing — no double-fire, no
  // settling on the wrong side after the second tick.
  it("flips exactly once when crossing the boundary 359 → 360 → 359", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expectDirect();

    resizeContainerTo(359);
    expectCompact();

    resizeContainerTo(360);
    expectDirect();

    resizeContainerTo(359);
    expectCompact();
  });

  // Ghost-reopen guard: opening the overflow Drawer while compact, then
  // widening past the breakpoint, must not just unmount the Drawer out from
  // under an `open={true}` state — it must declaratively close first. A
  // later re-narrow must land back in a genuinely closed state (only the
  // "More" trigger, nothing already open), never silently reopen whatever
  // tab was showing when the container widened.
  it("does not ghost-reopen the overflow Drawer after narrow → open → wide → narrow, with no state left over", async () => {
    resizeContainerTo(300);
    renderPage();
    await screen.findByTestId("message-list");
    expectCompact();

    // Open the Drawer and drill into Settings while compact.
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    const settingsRow = await screen.findByTestId("channel-details-settings");
    fireEvent.click(settingsRow);
    expect(await screen.findByRole("button", { name: "project" })).toBeInTheDocument();

    // Widen past the breakpoint — the eligibility effect must declaratively
    // clear `mobilePanel`, closing the Drawer, not just unmount it mid-open.
    resizeContainerTo(1024);
    expectDirect();
    expect(screen.queryByRole("button", { name: "project" })).toBeNull();
    expect(screen.queryByTestId("channel-details-settings")).toBeNull();
    // Cleanup guard: the real (unmocked) Vaul overlay/backdrop must not be
    // left in the document, and Radix/Vaul's own body scroll-lock attribute
    // must not linger — both are real Vaul-driven DOM side effects (not
    // this component's own state), so asserting them here is exercising
    // Vaul's actual unmount cleanup, not a stand-in.
    expect(document.querySelector("[data-vaul-overlay]")).toBeNull();
    expect(document.body).not.toHaveAttribute("data-scroll-locked");

    // Narrow again — must land on the closed "More" trigger only, never
    // resurrect the Settings tab that was open before it widened.
    resizeContainerTo(300);
    expectCompact();
    expect(screen.queryByRole("button", { name: "project" })).toBeNull();
    expect(screen.queryByTestId("channel-details-settings")).toBeNull();

    // A fresh click still reaches the panel normally — the guard only
    // prevents an AUTOMATIC reopen, it doesn't disable the entry point.
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(await screen.findByTestId("channel-details-settings")).toBeInTheDocument();
  });

  // First-paint guard: once the header actually mounts on a narrow
  // container, it must already be compact — never a "direct" frame that
  // then flips. `useContainerNarrowerThan` measures in `useLayoutEffect`
  // (synchronous, before the browser paints) rather than `useEffect`
  // (deferred to after paint) specifically so the corrected value lands in
  // the same commit a real browser paints, not a visibly later one.
  //
  // A synchronous post-`render()` assertion (no `await`) would be a
  // stronger proof, but the header itself only mounts once the channel's
  // async data resolves — asserting before that just observes "nothing
  // rendered yet," not the flash this test is guarding against. Awaiting
  // the header's first appearance and checking it lands compact is the
  // meaningful boundary: is the FIRST paint of the actions row itself ever
  // wrong.
  it("the header's first paint on a narrow container is already compact — never direct-then-flip", async () => {
    resizeContainerTo(300);
    renderPage();

    await screen.findByRole("button", { name: "More" });
    expectCompact();
  });
});

// LRM-234 — desktop channel title must show a visible ▾ affordance so
// Channel details / Settings aren't discoverable only by "knowing to click
// the name". Entry model unchanged (same control opens details).
describe("ChannelsPage header — desktop title chevron (LRM-234)", () => {
  it("shows a visible chevron next to the channel title on desktop", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expect(screen.getByTestId("channel-title-chevron")).toBeInTheDocument();
    const toggle = screen.getByRole("button", { name: "Open channel details" });
    expect(toggle).toContainElement(screen.getByTestId("channel-title-chevron"));
  });

  it("opens Channel details (Settings reachable) from the title+chevron control", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Open channel details" }));
    expect(await screen.findByTestId("channel-details-home")).toBeInTheDocument();
    expect(await screen.findByTestId("channel-details-settings")).toBeInTheDocument();
  });

  it("hides the title chevron on mobile (⋯ remains the overflow path)", async () => {
    mobile.value = true;
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expect(screen.queryByTestId("channel-title-chevron")).toBeNull();
    expect(screen.getByRole("button", { name: "More" })).toBeInTheDocument();
  });
});

// LRM-254 / LRM-447 — channel landmark stays a # glyph (never a roster collage).
// Design gate A moves the desktop # into the left meta tile; mobile keeps the
// inline ChannelHashLandmark beside the name.
describe("ChannelsPage — channel hash landmark (LRM-254 / LRM-447)", () => {
  it("puts a # meta tile left of the desktop title (no leading collage avatar)", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    const toggle = screen.getByRole("button", { name: "Open channel details" });
    expect(screen.getByTestId("channel-header-meta-tile")).toHaveTextContent("#");
    // Title control must not host an <img> collage tile or the hash landmark.
    expect(toggle.querySelector("img")).toBeNull();
    expect(within(toggle).queryByTestId("channel-hash-landmark")).toBeNull();
  });
});

// LRM-279 — channel title column must flex to fill header space before the
// shrink-0 action cluster; name truncates with tooltip; ▾ stays shrink-0.
describe("ChannelsPage header — title column width (LRM-279)", () => {
  it("uses flex-1 min-w-0 on the title control and surfaces the full name via tooltip", async () => {
    channelName.value = "LRM2.0开发群";
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");

    const toggle = screen.getByRole("button", { name: "Open channel details" });
    expect(toggle).toHaveClass("flex-1", "min-w-0");

    const nameSpan = within(toggle).getByText("LRM2.0开发群");
    expect(nameSpan).toHaveClass("flex-1", "min-w-0", "truncate");
    expect(within(toggle).getByTestId("channel-title-chevron")).toHaveClass("shrink-0");
    expect(screen.getByTestId("channel-header-meta-tile")).toBeInTheDocument();
  });
});

// LRM-452 — Frank: members chip must not carry a visible bg/border container.
// Wide rail stays equal-weight ghost (transparent default; muted on hover only).
describe("ChannelsPage header — members chip ghost chrome (LRM-452)", () => {
  it("keeps the action rail and members chip free of stacked border/bg chrome", async () => {
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");

    const rail = screen.getByTestId("channel-header-action-rail");
    expect(rail.className).not.toMatch(/\bborder\b/);
    expect(rail.className).not.toMatch(/\bbg-/);

    const members = screen.getByTestId("channel-header-members-chip");
    expect(members.className).not.toMatch(/\bborder\b/);
    expect(members.className).not.toMatch(/\bbg-background\b/);
    expect(members.className).toMatch(/hover:bg-muted/);
    // Avatars only — outer N · M / K working text removed (Frank 2026-07-25).
    expect(members).toHaveAttribute("aria-label", "View members");
    expect(within(members).getByTestId("channel-presence-faces")).toBeInTheDocument();
    expect(within(members).queryByTestId("channel-presence-counts")).toBeNull();
    expect(within(members).queryByTestId("channel-presence-working")).toBeNull();

    const search = screen.getByRole("button", { name: "Search in conversation" });
    expect(search.className).not.toMatch(/\bborder-border\b/);
    expect(search.className).not.toMatch(/\bbg-background\b/);
  });
});

// LRM-1067 — description under channel name in the main chat header.
describe("ChannelsPage header — description under name (LRM-1067)", () => {
  it("renders description under the title when present", async () => {
    channelDescription.value = "2.0开发群";
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    const desc = screen.getByTestId("channel-header-description");
    expect(desc).toHaveTextContent("2.0开发群");
    const title = screen.getByRole("button", { name: "Open channel details" });
    expect(title.compareDocumentPosition(desc) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("omits the description row when empty (no placeholder)", async () => {
    channelDescription.value = null;
    resizeContainerTo(1024);
    renderPage();
    await screen.findByTestId("message-list");
    expect(screen.queryByTestId("channel-header-description")).toBeNull();
  });

  it("keeps the description under the title on mobile", async () => {
    mobile.value = true;
    channelDescription.value = "2.0开发群";
    resizeContainerTo(390);
    renderPage();
    await screen.findByTestId("message-list");
    expect(screen.getByTestId("channel-header-description")).toHaveTextContent("2.0开发群");
  });
});
