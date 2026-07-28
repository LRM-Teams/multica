import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";

// #838 — drive the REAL page handlers without MediaRecorder: this stand-in
// exposes the voice-send callback as a button and renders the composer prefix
// (where the unsent-voice record lives). Everything under test — which send
// path retry takes, when the record clears — is the page's own logic.
const VOICE = { id: "att-voice-1", url: "https://cdn/v.wav", filename: "v.wav", content_type: "audio/wav", size_bytes: 1234 };
vi.mock("./composer", () => ({
  Composer: ({ prefix, onVoiceSend, onSend, surface, voiceDisabled, voiceBlockedReason }: {
    prefix?: React.ReactNode;
    onVoiceSend?: (d: number, a: unknown) => boolean;
    onSend?: () => void;
    surface?: string;
    voiceDisabled?: boolean;
    voiceBlockedReason?: string;
  }) => {
    // `surface` distinguishes the channel composer from a thread's — both real
    // components render through here, so tests can drive either one.
    const sfx = surface === "thread" ? "-thread" : "";
    return (
      // `data-voice-blocked-reason` surfaces the reason the PAGE resolved —
      // that is the seam #838 broke: the page added a third cause to
      // `voiceDisabled` without giving it copy of its own.
      <div
        data-testid={`composer${sfx}`}
        data-voice-disabled={voiceDisabled ? "true" : "false"}
        data-voice-blocked-reason={voiceBlockedReason ?? ""}
      >
        <div data-testid={`prefix${sfx}`}>{prefix}</div>
        <button data-testid={`fire-voice${sfx}`} onClick={() => onVoiceSend?.(7000, VOICE)}>voice</button>
        <button data-testid={`fire-text${sfx}`} onClick={() => onSend?.()}>text</button>
      </div>
    );
  },
}));

import { ChannelsPage } from "./channels-page";

// #642 — the workspace's immutable system #general channel. These tests
// cover what's reliably assertable through RTL: default-select priority
// (deep-link > remembered > #general > first channel), unpinned-list
// ordering, and the three gated affordances that are plain conditional
// DOM (not a floating Radix menu): the header Settings entry, the header
// member-management popover's per-member remove button, and the mobile
// Drawer's Settings row. The sidebar row's Archive item (inside a
// ContextMenu/DropdownMenu) is covered by direct code inspection in review
// rather than a jsdom floating-menu interaction test.

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
// Keep the real module's other exports (notably `ApiError`, used by the leave
// handler's 409 check) while swapping the `api` singleton for the spy proxy.
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: apiMock.proxy,
}));

const toastMock = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  info: vi.fn(),
}));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), toastMock) }));

// This suite renders the FULL ChannelsPage with the real react-virtuoso message
// list (intentionally unmocked, to exercise real sidebar/selection wiring). That
// render is heavy in jsdom, so under full-suite PARALLEL CI load a single test's
// `findByTestId("message-list")` can exceed vitest's 5s default and flake — this
// has repeatedly reddened UNRELATED PRs (e.g. #1243, #1232, whose diffs don't
// touch views). The tests are correct and pass in isolation; give the render
// timeout headroom under load rather than mask a real failure.
vi.setConfig({ testTimeout: 20000 });

// The system channel is deliberately NOT first in this array — ordering
// must come from `system_key`, never array/list position.
const DEFAULT_CHANNELS = [
  {
    id: "chan-random",
    workspace_id: "ws-1",
    name: "random",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  },
  {
    id: "chan-general",
    workspace_id: "ws-1",
    name: "general",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
    system_key: "general",
  },
];

// Mutable per-test channel list — a handful of tests need a different
// workspace shape (no system channel at all; an unrecognized system_key)
// without duplicating the whole mock setup into a second file.
const channelsFixture = vi.hoisted(() => ({ current: [] as unknown[] }));

const membersByChannel: Record<string, unknown[]> = {
  "chan-general": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
  "chan-random": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
  "chan-no-key": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
};

// ⚠️ SHARED BY EVERY TEST IN THIS FILE. Changing what these return has
// cross-test effects: seeding `channelMessagesPageOptions` with two messages
// (an attempt at the thread A→B case, #838) turned the unrelated, previously
// green "survives the toast being dismissed" RED. So:
//
//   after touching this mock, re-run the WHOLE file and compare against the
//   baseline — a green target test says nothing about the other nine.
//
// Without that step the likely conclusion is "this other test was already
// flaky", and someone edits a healthy test to match a fixture change.
//
// Shape trap, same case: the messages page is `{ messages: [...] }`, NOT
// `{ items: [...] }` — `flattenChannelMessagePages` reads `page.messages`
// (core/channels/queries.ts:142). The wrong key is accepted silently and the
// fixture simply never reaches the component, which on screen is
// indistinguishable from "the fixture arrived and the code is broken".
vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], channelsFixture.current),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: (channelId: string) =>
      options(["channel-members", channelId], membersByChannel[channelId] ?? []),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ items: [], next_cursor: null }),
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

// NOTE: the store is used BOTH as a hook and imperatively —
// `viewerAuthorFields()` in core/channels/mutations.ts calls
// `useAuthStore.getState()` inside the send mutation's `onMutate`. A mock that
// only provides the selector form makes `onMutate` throw a TypeError, which
// react-query reports as a mutation error: the failure path runs, the request
// is never sent, and a test asserting "failure produced X" passes for entirely
// the wrong reason. (That is exactly how this file's first draft went green.)
const authState = { user: { id: "user-1", name: "Alice" } };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector: (s: typeof authState) => unknown) => selector(authState),
    { getState: () => authState },
  ),
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

const mobileViewport = vi.hoisted(() => ({ value: false }));
// #568 — `useContainerNarrowerThan` (ResizeObserver-driven) isn't relevant
// to what this file tests; keep it a no-op ("plenty of room", direct row)
// so pre-existing desktop-direct-row assumptions here are unaffected.
// jsdom's default `getBoundingClientRect` is 0x0 for every element and
// `ResizeObserver` isn't implemented at all, so leaving the real hook
// running here would default to "compact" instead.
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileViewport.value,
  useContainerNarrowerThan: () => [false, () => {}] as const,
}));

const replaceSpy = vi.hoisted(() => vi.fn());
// #838 — thread surfaces open via the `?thread=` deep link; keep it mutable so a
// test can move between threads the way a navigation would.
const navState = vi.hoisted(() => ({ search: new URLSearchParams() }));
vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: navState.search,
    replace: replaceSpy,
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: () => <button type="button">project</button>,
}));
vi.mock("./dm-conversation", () => ({ DmConversation: () => <div data-testid="dm-conversation" /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({ ChannelMessageList: () => <div data-testid="message-list" /> }));

vi.mock("./conversation-surface", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./conversation-surface")>()),
  ConversationHeader: ({
    title,
    leading,
    actions,
  }: {
    title?: React.ReactNode;
    leading?: React.ReactNode;
    actions?: React.ReactNode;
  }) => (
    <div data-testid="active-title">
      {leading}
      {title}
      {actions}
    </div>
  ),
}));

function renderPage(channelId?: string) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  const ui = (
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage channelId={channelId} />
      </QueryClientProvider>
    </I18nProvider>
  );
  const view = render(ui);
  // #838 — re-render against the SAME client so a test can change the `?thread=`
  // deep link (which is read from navigation, not props) without remounting and
  // losing the very state under test.
  return { ...view, refresh: () => view.rerender(ui) };
}


// Flip-verified (whole file, `channels/components/channels-page-voice-failure`):
//   · failure path back to conflict-only / no record  → ALL 5 red
//   · retry rewired to the text send (the forbidden case) → the retry + "only a
//     committed retry clears it" cases red, the other 3 stay green
// The first draft of this file was a FALSE GREEN: `api.sendChannelMessage` was
// never invoked at all, because the auth-store mock lacked `getState()` and
// `onMutate` threw before the request — so "failure produced a record" passed
// without any send happening. The positive control below (voice submit called
// WITH the reused attachment id) is what exposed it; keep it.
describe("voice send failure leaves a durable record (#838)", () => {
  beforeEach(() => {
    replaceSpy.mockReset();
    mobileViewport.value = false;
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    channelsFixture.current = DEFAULT_CHANNELS;
    // NOT `api.sendChannelMessage?.mockReset?.()` — optional chaining here is a
    // silent trap (Felix, #839): if this proxy method is ever renamed, the reset
    // (and any `mockRejectedValueOnce` written the same way) quietly does
    // nothing and the tests keep passing for a different reason. Address it
    // directly so a rename is a hard failure.
    sendSpy().mockReset();
  });

  async function openChannel() {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    return screen.getByTestId("fire-voice");
  }

  /** Click the real sidebar row for a channel (no invented test hooks). */
  function switchTo(name: string) {
    const row = screen
      .getAllByRole("button")
      .find((el) => el.textContent?.includes(name));
    if (!row) throw new Error(`sidebar row not found: ${name}`);
    fireEvent.click(row);
  }

  function sendSpy(): ReturnType<typeof vi.fn> {
    return (apiMock.proxy as Record<string, ReturnType<typeof vi.fn>>)
      .sendChannelMessage as ReturnType<typeof vi.fn>;
  }

  it("a failed voice send leaves the recording on screen (the toast is not the record)", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    expect(await screen.findByTestId("composer-pending-voice")).toBeInTheDocument();
  });

  it("retry re-sends THIS recording through the voice path and never the text send", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    const callsBefore = sendSpy().mock.calls.length;
    fireEvent.click(screen.getByTestId("composer-pending-voice-retry"));

    // Positive control FIRST: the voice submit really ran, carrying the SAME
    // already-uploaded attachment. Without this, "no text send" would also pass
    // when retry did nothing at all.
    await waitFor(() => {
      expect(sendSpy().mock.calls.length).toBeGreaterThan(callsBefore);
    });
    // api.sendChannelMessage(channelId, payload) — the payload is arg 1, not 0.
    const retried = sendSpy().mock.calls.at(-1)?.[1] as { parts?: unknown; content?: string };
    expect(JSON.stringify(retried?.parts ?? "")).toContain(VOICE.id);
    // …and it is a voice payload, not the text composer's content.
    expect(retried?.content).toBe("");
  });

  it("only a committed retry clears it", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    sendSpy().mockResolvedValueOnce({ id: "m1" });
    fireEvent.click(screen.getByTestId("composer-pending-voice-retry"));
    await waitFor(() => {
      expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
    });
  });

  it("explicit delete clears it — and nothing else does", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    const item = await screen.findByTestId("composer-pending-voice");
    expect(item).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("composer-pending-voice-delete"));
    await waitFor(() => {
      expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
    });
  });

  // #838 H0 (Iris) — the record lives on the page, which outlives the current
  // channel. Bound only to "a voice is pending", a failure in A would render on
  // B's composer and retry would send A's recording INTO B. These pin the
  // isolation in both directions: invisible in B, still retryable back in A.
  it("a failure in channel A does not surface in channel B", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    // …switch to the other channel.
    switchTo("general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });

    // Sentinel first: the composer really is mounted here, so "absent" below is
    // a real gate and not an unmounted surface.
    expect(screen.getByTestId("composer")).toBeInTheDocument();
    expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
  });

  // NOTE on what this does and does NOT catch: reintroducing the H0 reddens the
  // A→B case above, but NOT this one — back in A the stored channel and the
  // active channel coincide, so "retry sent to chan-random" holds either way.
  // This is a round-trip control (the record survives leaving and returning, and
  // retry names the right channel), not an H0 detector. The wrong-channel send
  // is prevented structurally: once the item is invisible outside its own
  // surface there is no retry button to press there.
  it("switching back to channel A still shows it, and retry sends to A", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    switchTo("general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    switchTo("random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    expect(await screen.findByTestId("composer-pending-voice")).toBeInTheDocument();

    const before = sendSpy().mock.calls.length;
    fireEvent.click(screen.getByTestId("composer-pending-voice-retry"));
    await waitFor(() => {
      expect(sendSpy().mock.calls.length).toBeGreaterThan(before);
    });
    // Sent to the recording's OWN channel — not whatever happened to be active.
    expect(sendSpy().mock.calls.at(-1)?.[0]).toBe("chan-random");
  });

  // #838 H0 (Iris, 2nd pass) — the previous fix tagged ONE record with its
  // target, which hid it correctly but still let a later failure overwrite it:
  // fail in A, fail in B, and A's recording was gone on return. An unsent
  // recording may only vanish via a committed retry or an explicit delete, so
  // each target keeps its own entry.
  it("a failure in B does not destroy the unsent recording in A", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom-a"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    switchTo("general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    // B is free to record — and to fail on its own.
    expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
    sendSpy().mockRejectedValueOnce(new Error("boom-b"));
    fireEvent.click(screen.getByTestId("fire-voice"));
    await screen.findByTestId("composer-pending-voice");

    // Back in A: still there. (Before the map, B's failure had overwritten it.)
    switchTo("random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    expect(await screen.findByTestId("composer-pending-voice")).toBeInTheDocument();

    // …and retrying A targets A, not B.
    const before = sendSpy().mock.calls.length;
    fireEvent.click(screen.getByTestId("composer-pending-voice-retry"));
    await waitFor(() => {
      expect(sendSpy().mock.calls.length).toBeGreaterThan(before);
    });
    expect(sendSpy().mock.calls.at(-1)?.[0]).toBe("chan-random");

    // B's own record is independent and still waiting.
    switchTo("general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    expect(await screen.findByTestId("composer-pending-voice")).toBeInTheDocument();
  });

  it("deleting the record in A leaves B's untouched", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom-a"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    switchTo("general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    sendSpy().mockRejectedValueOnce(new Error("boom-b"));
    fireEvent.click(screen.getByTestId("fire-voice"));
    await screen.findByTestId("composer-pending-voice");

    // Explicitly discard B's…
    fireEvent.click(screen.getByTestId("composer-pending-voice-delete"));
    await waitFor(() => {
      expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
    });

    // …A's is untouched.
    switchTo("random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    expect(await screen.findByTestId("composer-pending-voice")).toBeInTheDocument();
  });

  // #838 H0 (Iris) — the channel cases above prove the map; these prove the
  // THREAD composer is really wired to it (display, retry target, per-thread
  // isolation), which a key-collision unit test cannot show.
  async function openThread(view: { refresh: () => void }, rootId: string) {
    navState.search = new URLSearchParams(`thread=${rootId}`);
    view.refresh();
    return screen.findByTestId("fire-voice-thread");
  }

  // ⚠️ SKIPPED — NOT COVERAGE. Two findings from probing it (so the next
  // attempt doesn't repeat them):
  //   1. The thread composer DOES mount — but only when `?thread=` is present
  //      at MOUNT. `threadDeepLinkId` seeds from a mount-time `useState`; setting
  //      the param afterwards and re-rendering did not open it here.
  //   2. Switching threads mid-test is the actual blocker, and my helper hid it:
  //      `fire-voice-thread` exists for EITHER thread, so `findByTestId` after a
  //      switch proves nothing — it matches thread A's composer just as happily.
  //      A real switch needs thread-identifying evidence in the DOM (the mocked
  //      Composer receives no root id; ThreadPanel has it).
  // Not a data-leak risk either way: `threadRoot` is nulled whenever
  // `activeChannelId !== openThreadRoot.channel_id` (channels-page ~977), so a
  // thread's content cannot render under another channel (verified by Felix) —
  // which is also why this case must NOT switch threads by switching channels:
  // that clearing is correct behaviour and would be misread as "won't mount".
  //
  // What IS proven meanwhile: the channel A/B sequences above (display,
  // overwrite-resistance, retry target, delete isolation) and voice-target.test.ts
  // (the key can't collide across channel/thread/root). The thread composer's
  // OWN wiring to the map is therefore NOT verified end-to-end. Tracked on #838.
  it.skip("thread A→B: B does not show A's unsent recording, and each thread keeps its own", async () => {
    navState.search = new URLSearchParams("thread=root-1");
    const view = renderPage("chan-random");
    await screen.findByTestId("composer");
    // PROBE: does the thread composer mount at all when the deep link is
    // present from the very first render?
    const fireA = await screen.findByTestId("fire-voice-thread");
    void view;
    sendSpy().mockRejectedValueOnce(new Error("boom-thread-a"));
    fireEvent.click(fireA);
    await waitFor(() => {
      expect(
        within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
      ).not.toBeNull();
    });

    // Thread B: sentinel proves its composer is mounted, so "absent" is a gate.
    const fireB = await openThread(view, "root-2");
    expect(fireB).toBeInTheDocument();
    expect(
      within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
    ).toBeNull();

    // B fails too — must not destroy A's.
    sendSpy().mockRejectedValueOnce(new Error("boom-thread-b"));
    fireEvent.click(fireB);
    await waitFor(() => {
      expect(
        within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
      ).not.toBeNull();
    });

    // Back to A: still there, and retry targets THIS thread.
    await openThread(view, "root-1");
    const item = within(screen.getByTestId("prefix-thread")).queryByTestId(
      "composer-pending-voice",
    );
    expect(item).not.toBeNull();

    const before = sendSpy().mock.calls.length;
    fireEvent.click(
      within(screen.getByTestId("prefix-thread")).getByTestId("composer-pending-voice-retry"),
    );
    await waitFor(() => {
      expect(sendSpy().mock.calls.length).toBeGreaterThan(before);
    });
    // Thread sends go through sendChannelThreadMessage-shaped args: the thread
    // root must be A's, not B's.
    const payload = JSON.stringify(sendSpy().mock.calls.at(-1) ?? "");
    expect(payload).toContain("root-1");
    expect(payload).not.toContain("root-2");
  });

  it("while a recording is unsent, the mic is blocked with THAT reason — never the generic 'clear text and attachments'", async () => {
    const GENERIC = enChannels.composer.voice_blocked;
    const PENDING = enChannels.composer.voice_blocked_pending_voice;

    const fire = await openChannel();
    // Control: nothing pending yet — not blocked, and no reason claimed.
    expect(screen.getByTestId("composer")).toHaveAttribute("data-voice-disabled", "false");
    expect(screen.getByTestId("composer")).toHaveAttribute("data-voice-blocked-reason", "");

    sendSpy().mockRejectedValueOnce(new Error("boom"));
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    // Now blocked — and the reason must name the actual cause. The generic copy
    // tells a user with an EMPTY composer to clear text and attachments, which
    // does nothing and never mentions the retry/delete that would.
    const composer = screen.getByTestId("composer");
    expect(composer).toHaveAttribute("data-voice-disabled", "true");
    expect(composer).toHaveAttribute("data-voice-blocked-reason", PENDING);
    expect(composer.getAttribute("data-voice-blocked-reason")).not.toBe(GENERIC);
    // Guards the trap directly: the sentence must not survive as a substring
    // either (e.g. if someone later concatenates the two).
    expect(composer.getAttribute("data-voice-blocked-reason")).not.toContain("attachments");

    // …and it goes away with the record, not on a timer.
    fireEvent.click(screen.getByTestId("composer-pending-voice-delete"));
    await waitFor(() => {
      expect(screen.getByTestId("composer")).toHaveAttribute("data-voice-disabled", "false");
    });
    expect(screen.getByTestId("composer")).toHaveAttribute("data-voice-blocked-reason", "");
  });

  it("survives the toast being dismissed — the toast is the announcement, not the storage", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    // The failure was announced…
    expect(toastMock.error).toHaveBeenCalled();
    // …and dismissing that announcement (sonner is mocked; the toast simply
    // goes away) must not remove the record.
    expect(screen.getByTestId("composer-pending-voice")).toBeInTheDocument();
  });
});
