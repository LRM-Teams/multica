import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import { ApiError } from "@multica/core/api";
import { toast } from "sonner";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// LRM-694 — merged from five channels-page-* files (batch 1: voice-failure +
// system-channel; batch 2: create-group-project + role-failure-retry +
// reminder-thread-anchor). One jsdom env + one module graph instead of five.
// Per-suite fixture state lives in the hoisted `channelsFixture` /
// `membersByChannel` / `messagesFixture` / `threadsFixture` /
// `projectsFixture` / `navState` and is reset in each describe's beforeEach so
// every test sees exactly the environment its source file gave it — merging
// must not change what any test observes. Where two source files needed
// different stub SHAPES for the same module (project picker, details panel,
// presence cluster), the mock spreads importOriginal and switches on a
// render-time flag the owning suite raises in its beforeEach.
//
// #642 — the workspace's immutable system #general channel. These tests
// cover what's reliably assertable through RTL: default-select priority
// (deep-link > remembered > #general > first channel), unpinned-list
// ordering, and the three gated affordances that are plain conditional
// DOM (not a floating Radix menu): the header Settings entry, the header
// member-management popover's per-member remove button, and the mobile
// Drawer's Settings row. The sidebar row's Archive item (inside a
// ContextMenu/DropdownMenu) is covered by direct code inspection in review
// rather than a jsdom floating-menu interaction test.

// #838 — drive the REAL page handlers without MediaRecorder: this stand-in
// exposes the voice-send callback as a button and renders the composer prefix
// (where the unsent-voice record lives). Everything under test — which send
// path retry takes, when the record clears — is the page's own logic.
const VOICE = { id: "att-voice-1", url: "https://cdn/v.wav", filename: "v.wav", content_type: "audio/wav", size_bytes: 1234 };
vi.mock("./composer", () => ({
  Composer: ({ prefix, onVoiceSend, onSend, surface, voiceBlock }: {
    prefix?: React.ReactNode;
    onVoiceSend?: (d: number, a: unknown) => boolean;
    onSend?: () => void;
    surface?: string;
    voiceBlock?: { pendingVoice?: boolean; hasTextDraft: boolean; hasAttachmentDraft: boolean };
  }) => {
    // `surface` distinguishes the channel composer from a thread's — both real
    // components render through here, so tests can drive either one.
    const sfx = surface === "thread" ? "-thread" : "";
    return (
      // Surfaces the raw block INPUTS the page supplies. #858 moved sentence
      // selection into the composer shell, so the page's job is now only to
      // report the conditions truthfully — which is the seam #838 broke.
      <div
        data-testid={`composer${sfx}`}
        data-voice-pending={voiceBlock?.pendingVoice ? "true" : "false"}
      >
        <div data-testid={`prefix${sfx}`}>{prefix}</div>
        <button data-testid={`fire-voice${sfx}`} onClick={() => onVoiceSend?.(7000, VOICE)}>voice</button>
        <button data-testid={`fire-text${sfx}`} onClick={() => onSend?.()}>text</button>
      </div>
    );
  },
}));

const apiMock = vi.hoisted(() => {
  // #576 create-flow: the created channel the createChannel spy resolves with.
  // Pre-created (not lazy) so the merged suites can hold a direct `apiMock.X`
  // reference the way their source files did.
  const createChannel = vi.fn().mockResolvedValue({
    id: "chan-new",
    workspace_id: "ws-1",
    name: "New Group",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-07-21T09:00:00Z",
    updated_at: "2026-07-21T09:00:00Z",
  });
  // #832 role-failure: no default impl — each test installs its own rejection.
  const updateChannelMemberRole = vi.fn();
  const known: Record<string, unknown> = { createChannel, updateChannelMemberRole };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy, createChannel, updateChannelMemberRole };
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
  warning: vi.fn(),
}));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), toastMock) }));

// This suite renders the FULL ChannelsPage, which is heavy in jsdom: under
// full-suite PARALLEL CI load a single test's `findByTestId("message-list")` can
// exceed vitest's 5s default and flake — this has repeatedly reddened UNRELATED
// PRs (e.g. #1243, #1232, whose diffs don't touch views). The tests are correct
// and pass in isolation; give the render timeout headroom under load rather than
// mask a real failure.
//
// CORRECTION (task #853, 2026-07-28): `./channel-message-list` (the only place
// react-virtuoso is imported) is mocked wholesale below, so virtuoso never runs
// here and `message-list` is a stub div. The weight is ChannelsPage itself —
// providers, queries and context — not the windowed list. The stale claim that
// "the real react-virtuoso list runs here" misled three of us while triaging
// this exact flake, so: the cost being bought here is FULL-PAGE RENDER time,
// and any fix should target that, not the list.
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

// The voice suite's two thread roots — seeded per-test from its beforeEach.
const VOICE_MESSAGES = {
  messages: [
    { id: "root-1", channel_id: "chan-random", workspace_id: "ws-1", seq: 1, type: "user",
      author_id: "user-2", author_name: "bob", content: "THREADROOTONE", source: "multica",
      external_message_id: null, client_message_id: null, created_at: "2026-06-17T09:01:00Z" },
    { id: "root-2", channel_id: "chan-random", workspace_id: "ws-1", seq: 2, type: "user",
      author_id: "user-2", author_name: "bob", content: "THREADROOTTWO", source: "multica",
      external_message_id: null, client_message_id: null, created_at: "2026-06-17T09:02:00Z" },
  ],
  next_cursor: null as string | null,
};

// ⚠️ SHARED BY EVERY TEST IN THIS FILE. Changing what these return has
// cross-test effects: seeding `channelMessagesPageOptions` with two messages
// (an attempt at the thread A→B case, #838) turned the unrelated, previously
// green "survives the toast being dismissed" RED. So:
//
//   after touching this mock, re-run the WHOLE file and compare against the
//   baseline — a green target test says nothing about the other suites.
//
// Without that step the likely conclusion is "this other test was already
// flaky", and someone edits a healthy test to match a fixture change.
//
// Shape trap, same case: the messages page is `{ messages: [...] }`, NOT
// `{ items: [...] }` — `flattenChannelMessagePages` reads `page.messages`
// (core/channels/queries.ts:142). The wrong key is accepted silently and the
// fixture simply never reaches the component, which on screen is
// indistinguishable from "the fixture arrived and the code is broken".
//
// LRM-694 merge note: the two source files seeded DIFFERENT pages (voice: the
// two roots above; system suites: the legacy `{ items: [] }` shape). The page
// data is therefore per-suite mutable state, reset in each beforeEach to the
// exact value its source file returned inline.
const messagesFixture = vi.hoisted(() => ({
  current: { messages: [] as unknown[], next_cursor: null as string | null } as Record<
    string,
    unknown
  >,
}));

// #656 reminder-anchor suite: thread replies keyed by root message id. Default
// `{}` reproduces the static `{ messages: [] }` the voice/system suites had.
const threadsFixture = vi.hoisted(() => ({
  current: {} as Record<string, unknown[]>,
}));

// #576 create-popover suite: projects the picker lists. The voice/system
// suites never mocked `projects/queries` — the real queryFn ran against the
// proxied api (undefined → empty picker), which a default `[]` reproduces.
const projectsFixture = vi.hoisted(() => ({
  current: [] as unknown[],
}));

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
    channelMessageThreadOptions: (_channelId: string, messageId: string) =>
      options(["channel-thread", messageId], {
        messages: threadsFixture.current[messageId] ?? [],
      }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => messagesFixture.current,
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: () => ({
    queryKey: ["projects"],
    queryFn: async () => projectsFixture.current,
  }),
}));

// NOTE: the store is used BOTH as a hook and imperatively —
// `viewerAuthorFields()` in core/channels/mutations.ts calls
// `useAuthStore.getState()` inside the send mutation's `onMutate`. A mock that
// only provides the selector form makes `onMutate` throw a TypeError, which
// react-query reports as a mutation error: the failure path runs, the request
// is never sent, and a test asserting "failure produced X" passes for entirely
// the wrong reason. (That is exactly how the #838 file's first draft went
// green.)
const authState = { user: { id: "user-1", name: "Alice" } };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector: (s: typeof authState) => unknown) => selector(authState),
    { getState: () => authState },
  ),
}));

// Shared fixtures for byte-identical factories (#1364 direction D / LRM-694).
// auth + dm stay inline: this merged suite needs getState() on useAuthStore and
// a mutable dmListFixture — not the shared authMock/dmMock shapes.
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

// Mutable so the DM-open-never-resolves suite can simulate a DM disappearing
// from the list after selection (the real 2026-07-31 Wendy DM incident shape:
// createOrFind's optimistic setQueryData is overwritten by an
// invalidate-triggered refetch that comes back empty). Every other suite
// never touches this and sees the same `[]` the old static mock returned.
const dmListFixture = vi.hoisted(() => ({ current: [] as import("@multica/core/dm").DMItem[] }));
vi.mock("@multica/core/dm", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/dm")>()),
  dmListOptions: () => ({ queryKey: ["dm-list"], queryFn: async () => dmListFixture.current }),
}));

// LRM-1399 — the page now reads the unified `GET /api/conversations` list as
// its single CHANNELS+DM source. Rebuild it here from the same channel/dm
// fixtures the older suite used so every pre-existing assertion (channels
// render, DM behavior) stays intact while the page only ever asks for the
// conversations query.
vi.mock("@multica/core/conversations", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/conversations")>();
  return {
    ...actual,
    conversationsOptions: () => ({
      queryKey: ["conversations", "ws-1", "list"],
      queryFn: async () => ({
        items: [
          ...(channelsFixture.current as import("@multica/core/types").Channel[]).map(
            (channel) => ({ kind: "channel" as const, channel }),
          ),
          ...dmListFixture.current.map((dm) => ({ kind: "dm" as const, dm })),
        ],
        next_cursor: undefined,
      }),
      initialPageParam: null as string | null,
      getNextPageParam: () => undefined,
    }),
  };
});

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
// test can move between threads the way a navigation would. Every describe's
// beforeEach resets it to empty: a leaked `?thread=` renders ThreadPanel's
// header as a second `active-title`, and `getByTestId` throws "found multiple
// elements" in tests that never mentioned threads.
const navState = vi.hoisted(() => ({ search: new URLSearchParams() }));
// #656 reminder-anchor suite wrote its deep links through
// `currentSearchParams.value`; alias it onto the same holder so both spellings
// address one source of truth.
const currentSearchParams = {
  get value() {
    return navState.search;
  },
  set value(v: URLSearchParams) {
    navState.search = v;
  },
};
vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    // A fresh copy per call, like a real `useSearchParams()` on web — a
    // same-pathname AppLink push changes searchParams WITHOUT remounting, and a
    // one-shot mount-time read would silently miss it (#656).
    searchParams: new URLSearchParams(navState.search),
    push: vi.fn(),
    replace: replaceSpy,
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/lazy-content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
// The #576 SETTINGS tests need the dumb labeled button (enabled/disabled is
// what's asserted); the #576 CREATE-popover suite needs a functional stub that
// drives `onChange` and echoes `value`. Render-time flag, set by the create
// suite's beforeEach — vi.mock factories can't vary per suite, the rendered
// output can.
const pickerStub = vi.hoisted(() => ({ functional: false }));
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: (props: {
    disabled?: boolean;
    value?: string | null;
    onChange?: (id: string | null) => void;
  }) =>
    pickerStub.functional ? (
      <button
        type="button"
        aria-label="Project: pick"
        onClick={() => props.onChange?.(props.value === "proj-1" ? null : "proj-1")}
      >
        picker:{props.value ?? "none"}
      </button>
    ) : (
      <button type="button" disabled={props.disabled}>
        project
      </button>
    ),
}));
vi.mock("./dm-conversation", () => ({ DmConversation: () => <div data-testid="dm-conversation" /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
// #832 role-failure suite: chrome-only details panel (renders the page-built
// membersBody directly) + presence cluster reduced to an "open-members"
// button. Every other suite needs the REAL details panel and the REAL presence
// trigger (the #642 tests click "View members"), so both stubs are gated on
// render-time flags that only the role-failure suite raises.
const chromeStub = vi.hoisted(() => ({ detailsPanel: false, presenceCue: false }));
vi.mock("./channel-details-panel", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./channel-details-panel")>();
  return {
    ...actual,
    ChannelDetailsPanel: (props: React.ComponentProps<typeof actual.ChannelDetailsPanel>) =>
      chromeStub.detailsPanel ? (
        <div data-testid="details-panel">{props.membersBody}</div>
      ) : (
        <actual.ChannelDetailsPanel {...props} />
      ),
  };
});
vi.mock("./channel-agents-live-cue", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./channel-agents-live-cue")>();
  type ClusterProps = React.ComponentProps<typeof actual.ChannelPresenceCluster>;
  return {
    ...actual,
    ChannelPresenceCluster: (props: ClusterProps & { onOpenMembers?: () => void }) =>
      chromeStub.presenceCue ? (
        <button type="button" onClick={props.onOpenMembers}>
          open-members
        </button>
      ) : (
        <actual.ChannelPresenceCluster {...props} />
      ),
  };
});
// #838 thread A→B — `header` MUST be rendered. ThreadPanel passes the pinned
// ThreadRootPreview through it (thread-panel.tsx:262), so a mock that drops
// `header` also drops the only per-thread evidence in the DOM. The previous
// stub took no props at all, which is why switching threads looked unobservable:
// `fire-voice-thread` matches whichever thread is open, and the root — the one
// thing that differs — was being thrown away by the mock, not by the page.
// (Suites that never open a thread get header === undefined → the same plain
// stub div the system-channel file used.)
vi.mock("./channel-message-list", () => ({
  // Superset of the two source stubs: the voice suites need `header` rendered
  // (ThreadRootPreview drives the thread voice flow); the #656 reminder suite
  // needs message ids + the highlight target surfaced so it can tell the main
  // timeline apart from ThreadPanel's reply list. Suites that never open a
  // thread and seed no messages get the same plain stub div as before.
  // LRM-740 — also surface onOpenMember/onOpenAgent so embedded Thread avatar
  // clicks can drive the page dock stack without mounting real bubbles.
  ChannelMessageList: ({
    header,
    messages,
    highlightMessageId,
    onOpenMember,
    onOpenAgent,
  }: {
    header?: React.ReactNode;
    messages?: { id: string }[];
    highlightMessageId?: string | null;
    onOpenMember?: (userId: string) => void;
    onOpenAgent?: (agentId: string, snapshot?: unknown) => void;
  }) => (
    <div
      data-testid="message-list"
      data-highlight={highlightMessageId ?? ""}
      data-count={(messages ?? []).length}
    >
      {header}
      {onOpenMember ? (
        <button
          type="button"
          data-testid="list-open-member"
          onClick={() => onOpenMember("user-9")}
        >
          open-member
        </button>
      ) : null}
      {onOpenAgent ? (
        <button
          type="button"
          data-testid="list-open-agent"
          onClick={() => onOpenAgent("agent-9")}
        >
          open-agent
        </button>
      ) : null}
      {(messages ?? []).filter(Boolean).map((m) => (
        <div key={m.id} data-testid={`msg-${m.id}`} />
      ))}
    </div>
  ),
}));

vi.mock("../../members/member-side-panel", () => ({
  MemberSidePanel: ({
    userId,
    onClose,
  }: {
    userId: string;
    onClose?: () => void;
  }) => (
    <div data-testid="member-side-panel" data-user-id={userId}>
      <button type="button" data-testid="member-side-close" onClick={onClose}>
        close
      </button>
    </div>
  ),
}));

vi.mock("../../common/resolved-agent-side-panel", () => ({
  ResolvedAgentSidePanel: ({
    agentId,
    onClose,
  }: {
    agentId: string;
    onClose?: () => void;
  }) => (
    <div data-testid="agent-side-panel" data-agent-id={agentId}>
      <button type="button" data-testid="agent-side-close" onClick={onClose}>
        close
      </button>
    </div>
  ),
}));

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

function renderPage(
  channelId?: string,
  options?: { embedded?: boolean; embeddedSurface?: "thread" | "channel" },
) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  // Built FRESH on every call, never reused. Re-rendering the identical element
  // object lets React bail out of the subtree, so `?thread=` changes were never
  // observed — the page kept showing the first thread and the switch looked
  // impossible. A new element with the same props forces the re-render.
  const ui = () => (
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage
          channelId={channelId}
          embedded={options?.embedded}
          embeddedSurface={options?.embeddedSurface}
        />
      </QueryClientProvider>
    </I18nProvider>
  );
  const view = render(ui());
  // #838 — re-render against the SAME client so a test can change the `?thread=`
  // deep link (which is read from navigation, not props) without remounting and
  // losing the very state under test.
  // #656: expose the client too — the reminder-anchor suite rerenders with an
  // explicit provider and its whole point is proving the thread switch reuses
  // THIS client (no full reload).
  return { ...view, qc, refresh: () => view.rerender(ui()) };
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
    messagesFixture.current = VOICE_MESSAGES;
    // NOT `api.sendChannelMessage?.mockReset?.()` — optional chaining here is a
    // silent trap (Felix, #839): if this proxy method is ever renamed, the reset
    // (and any `mockRejectedValueOnce` written the same way) quietly does
    // nothing and the tests keep passing for a different reason. Address it
    // directly so a rename is a hard failure.
    sendSpy().mockReset();
    threadSendSpy().mockReset();
    // `navState` is module-level and MUTABLE, so a test that deep-links into a
    // thread leaves `?thread=` set for every test after it — those then render
    // ThreadPanel too, and its header is a second `active-title`, so
    // `getByTestId` throws "found multiple elements" in tests that never
    // mentioned threads. Latent until the thread case stopped being skipped.
    navState.search = new URLSearchParams();
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

  // Thread sends go through a DIFFERENT api method than channel sends
  // (core/channels/mutations.ts:261 `api.sendChannelThreadMessage`). Injecting a
  // rejection into `sendChannelMessage` therefore does NOT fail a thread send —
  // the auto-spy proxy resolves `undefined` instead, which fails downstream for
  // its own reasons. The failure path still runs, so the test LOOKS right while
  // exercising an incidental error rather than the one it meant to simulate.
  function threadSendSpy(): ReturnType<typeof vi.fn> {
    return (apiMock.proxy as Record<string, ReturnType<typeof vi.fn>>)
      .sendChannelThreadMessage as ReturnType<typeof vi.fn>;
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
  // Waits on the pinned ROOT TEXT, never on `fire-voice-thread`: that testid
  // exists for whichever thread is open, so awaiting it after a switch is
  // satisfied by the thread we were already on — it cannot tell "B opened" from
  // "the switch silently did nothing". The root preview (ThreadPanel passes it
  // as ChannelMessageList's `header`) is the only per-thread evidence in the DOM.
  async function openThread(
    view: { refresh: () => void },
    rootId: string,
    rootText: string,
  ) {
    navState.search = new URLSearchParams(`thread=${rootId}`);
    view.refresh();
    await screen.findByText(rootText);
    return screen.getByTestId("fire-voice-thread");
  }

  // Thread-surface wiring (#838 H0, Iris). The channel cases above prove the
  // per-target map; these prove the THREAD composer is really wired to it.
  //
  // Harness requirements, learned the hard way — all four were faults in the
  // TEST, not the page:
  //   1. the ChannelMessageList mock must render `header` (ThreadPanel passes
  //      the pinned root through it — the only per-thread evidence in the DOM);
  //   2. `refresh()` must build a NEW element (re-rendering the identical object
  //      lets React bail out, so `?thread=` changes never land);
  //   3. the messages fixture key is `messages`, not `items`;
  //   4. thread sends reject through `sendChannelThreadMessage` — rejecting
  //      `sendChannelMessage` fails for an unrelated downstream reason and the
  //      test passes while simulating the wrong error.
  //
  // Never switch threads by switching CHANNELS: `threadRoot` is nulled whenever
  // `activeChannelId !== openThreadRoot.channel_id` (channels-page ~977), which
  // is correct behaviour and would be misread as "the thread won't mount".
  it("thread A→B: B does not show A's unsent recording, and each thread keeps its own", async () => {
    navState.search = new URLSearchParams("thread=root-1");
    const view = renderPage("chan-random");
    await screen.findByTestId("composer");
    // PROBE: does the thread composer mount at all when the deep link is
    // present from the very first render?
    // Wait on the pinned ROOT TEXT — the deterministic per-thread evidence —
    // never on `fire-voice-thread`: that testid appears only after the async
    // deep-link → route-reconcile → ThreadPanel mount chain resolves, so
    // awaiting it with the default 1s findBy budget flakes under CPU/CI
    // contention (LRM-1445). Root text is on the same mount, and once it is
    // in the DOM the thread composer is present synchronously (openThread
    // relies on the same guarantee).
    void view;
    await screen.findByText("THREADROOTONE");
    const fireA = screen.getByTestId("fire-voice-thread");
    threadSendSpy().mockRejectedValueOnce(new Error("boom-thread-a"));
    fireEvent.click(fireA);
    await waitFor(() => {
      expect(
        within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
      ).not.toBeNull();
    });

    // Thread B: sentinel proves its composer is mounted, so "absent" is a gate.
    const fireB = await openThread(view, "root-2", "THREADROOTTWO");
    expect(fireB).toBeInTheDocument();
    expect(
      within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
    ).toBeNull();

    // B fails too — must not destroy A's.
    threadSendSpy().mockRejectedValueOnce(new Error("boom-thread-b"));
    fireEvent.click(fireB);
    await waitFor(() => {
      expect(
        within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
      ).not.toBeNull();
    });

    // Back to A: still there, and retry targets THIS thread.
    await openThread(view, "root-1", "THREADROOTONE");
    const item = within(screen.getByTestId("prefix-thread")).queryByTestId(
      "composer-pending-voice",
    );
    expect(item).not.toBeNull();

    const before = threadSendSpy().mock.calls.length;
    fireEvent.click(
      within(screen.getByTestId("prefix-thread")).getByTestId("composer-pending-voice-retry"),
    );
    await waitFor(() => {
      expect(threadSendSpy().mock.calls.length).toBeGreaterThan(before);
    });
    // Thread sends go through sendChannelThreadMessage-shaped args: the thread
    // root must be A's, not B's.
    const payload = JSON.stringify(threadSendSpy().mock.calls.at(-1) ?? "");
    expect(payload).toContain("root-1");
    expect(payload).not.toContain("root-2");
  });

  /** Leaves thread A and thread B each holding their own failed recording. */
  async function failInBothThreads() {
    navState.search = new URLSearchParams("thread=root-1");
    const view = renderPage("chan-random");
    await screen.findByTestId("composer");
    // See the probe in "thread A→B" above: gate on the pinned root text, not
    // on the load-sensitive `fire-voice-thread` findBy (LRM-1445).
    await screen.findByText("THREADROOTONE");
    const fireA = screen.getByTestId("fire-voice-thread");
    threadSendSpy().mockRejectedValueOnce(new Error("boom-thread-a"));
    fireEvent.click(fireA);
    await waitFor(() => {
      expect(
        within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
      ).not.toBeNull();
    });

    const fireB = await openThread(view, "root-2", "THREADROOTTWO");
    threadSendSpy().mockRejectedValueOnce(new Error("boom-thread-b"));
    fireEvent.click(fireB);
    await waitFor(() => {
      expect(
        within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
      ).not.toBeNull();
    });
    return view;
  }

  function threadRecord() {
    return within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice");
  }

  // Iris (#838 H0): delete is its OWN action, not covered by the display/retry
  // cases or by voice-target.test.ts's key unit test. Deleting in one thread must
  // remove exactly that thread's record and leave the other's fully usable —
  // "still visible" is not enough, so each direction also RETRIES the survivor
  // and checks which root the send went to.
  it("thread: deleting A's record removes only A's — B's survives and still retries to B", async () => {
    const view = await failInBothThreads();

    await openThread(view, "root-1", "THREADROOTONE");
    expect(threadRecord()).not.toBeNull();
    fireEvent.click(
      within(screen.getByTestId("prefix-thread")).getByTestId("composer-pending-voice-delete"),
    );
    await waitFor(() => expect(threadRecord()).toBeNull());

    // B is untouched…
    await openThread(view, "root-2", "THREADROOTTWO");
    expect(threadRecord()).not.toBeNull();

    // …and still targets B. Without this, "visible" could be a stale render.
    const before = threadSendSpy().mock.calls.length;
    fireEvent.click(
      within(screen.getByTestId("prefix-thread")).getByTestId("composer-pending-voice-retry"),
    );
    await waitFor(() => {
      expect(threadSendSpy().mock.calls.length).toBeGreaterThan(before);
    });
    const payload = JSON.stringify(threadSendSpy().mock.calls.at(-1) ?? "");
    expect(payload).toContain("root-2");
    expect(payload).not.toContain("root-1");
  });

  it("thread: deleting B's record removes only B's — A's survives and still retries to A", async () => {
    const view = await failInBothThreads();

    // Already on B after the setup.
    expect(threadRecord()).not.toBeNull();
    fireEvent.click(
      within(screen.getByTestId("prefix-thread")).getByTestId("composer-pending-voice-delete"),
    );
    await waitFor(() => expect(threadRecord()).toBeNull());

    await openThread(view, "root-1", "THREADROOTONE");
    expect(threadRecord()).not.toBeNull();

    const before = threadSendSpy().mock.calls.length;
    fireEvent.click(
      within(screen.getByTestId("prefix-thread")).getByTestId("composer-pending-voice-retry"),
    );
    await waitFor(() => {
      expect(threadSendSpy().mock.calls.length).toBeGreaterThan(before);
    });
    const payload = JSON.stringify(threadSendSpy().mock.calls.at(-1) ?? "");
    expect(payload).toContain("root-1");
    expect(payload).not.toContain("root-2");
  });

  it("an unsent recording is reported to the composer as the pending-voice cause", async () => {
    // #858 moved the SENTENCE into the composer shell (composer.test.tsx covers
    // which words each cause gets). What still belongs here is the page-level
    // seam #838 broke: the page must report the pending-voice condition, since
    // a cause it never reports can never be explained downstream.
    const fire = await openChannel();
    expect(screen.getByTestId("composer")).toHaveAttribute("data-voice-pending", "false");

    sendSpy().mockRejectedValueOnce(new Error("boom"));
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    expect(screen.getByTestId("composer")).toHaveAttribute("data-voice-pending", "true");

    // …and it clears with the record, not on a timer.
    fireEvent.click(screen.getByTestId("composer-pending-voice-delete"));
    await waitFor(() => {
      expect(screen.getByTestId("composer")).toHaveAttribute("data-voice-pending", "false");
    });
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

  // LRM-1356 — the in-flight state the record shows must be THIS record's own
  // retry, not the surface's send mutation.
  //
  // `sendMessage` is one mutation for the whole channel composer and it outlives
  // a channel switch, so `isPending` is true for any send in flight anywhere on
  // the page. Wired to that, an unrelated send dimmed the unsent recording and
  // — because both actions guard on the same flag (LRM-1354) — took away Delete
  // too: the user could not even discard a recording that had nothing to do with
  // the send that was running. Delete is the record's only exit besides a
  // committed retry, so this is a real dead end, not just a cosmetic dim.
  describe("in-flight state is scoped to this record's own retry (LRM-1356)", () => {
    /** A send that never settles — leaves `sendMessage.isPending` true. */
    function hangNextSend() {
      sendSpy().mockReturnValueOnce(new Promise<never>(() => {}));
    }

    /** Fail a voice send in `random`, then leave an unrelated send hanging in `general`. */
    async function recordInAThenHangSendInB() {
      sendSpy().mockRejectedValueOnce(new Error("boom-a"));
      const fire = await openChannel();
      fireEvent.click(fire);
      await screen.findByTestId("composer-pending-voice");

      switchTo("general");
      await waitFor(() => {
        expect(screen.getByTestId("active-title")).toHaveTextContent("general");
      });
      // B has no record of its own, so this send is unrelated to A's.
      expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
      hangNextSend();
      fireEvent.click(screen.getByTestId("fire-voice"));
      await waitFor(() => {
        expect(sendSpy().mock.calls.length).toBeGreaterThan(1);
      });

      switchTo("random");
      await waitFor(() => {
        expect(screen.getByTestId("active-title")).toHaveTextContent("random");
      });
      return await screen.findByTestId("composer-pending-voice");
    }

    it("an unrelated send in flight does not mark the record as retrying", async () => {
      await recordInAThenHangSendInB();

      expect(screen.getByTestId("composer-pending-voice-retry")).not.toHaveAttribute(
        "aria-disabled",
      );
      expect(screen.getByTestId("composer-pending-voice-delete")).not.toHaveAttribute(
        "aria-disabled",
      );
      const status = screen.getByTestId("composer-pending-voice-status");
      expect(status).not.toHaveAttribute("aria-busy");
      expect(status).toHaveTextContent("not sent");
    });

    it("an unrelated send in flight still lets the user discard the recording", async () => {
      await recordInAThenHangSendInB();

      fireEvent.click(screen.getByTestId("composer-pending-voice-delete"));
      await waitFor(() => {
        expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
      });
    });

    // The other half of the contract: the record's OWN retry must still show the
    // in-flight state (LRM-1354), so scoping the flag cannot silently disable it.
    it("this record's own retry in flight is still reported as retrying", async () => {
      sendSpy().mockRejectedValueOnce(new Error("boom-a"));
      const fire = await openChannel();
      fireEvent.click(fire);
      await screen.findByTestId("composer-pending-voice");

      hangNextSend();
      fireEvent.click(screen.getByTestId("composer-pending-voice-retry"));

      await waitFor(() => {
        expect(screen.getByTestId("composer-pending-voice-retry")).toHaveAttribute(
          "aria-disabled",
          "true",
        );
      });
      expect(screen.getByTestId("composer-pending-voice-delete")).toHaveAttribute(
        "aria-disabled",
        "true",
      );
      const status = screen.getByTestId("composer-pending-voice-status");
      expect(status).toHaveAttribute("aria-busy", "true");
      expect(status).toHaveTextContent("Resending");
    });


    // Same over-broad flag on the thread surface: a hanging thread send in
    // thread B must not freeze thread A's record.
    it("thread: a send in flight in another thread does not mark this record as retrying", async () => {
      navState.search = new URLSearchParams();
      const view = renderPage("chan-random");
      await screen.findByTestId("composer");
      // Open thread A through the same helper the other thread cases use: it
      // waits on the pinned root text, which is the only per-thread evidence in
      // the DOM (the thread composer mounts a tick later than the channel one).
      const fireA = await openThread(view, "root-1", "THREADROOTONE");
      threadSendSpy().mockRejectedValueOnce(new Error("boom-thread-a"));
      fireEvent.click(fireA);
      await waitFor(() => {
        expect(
          within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
        ).not.toBeNull();
      });

      const fireB = await openThread(view, "root-2", "THREADROOTTWO");
      threadSendSpy().mockReturnValueOnce(new Promise<never>(() => {}));
      fireEvent.click(fireB);
      await waitFor(() => {
        expect(threadSendSpy().mock.calls.length).toBeGreaterThan(1);
      });

      await openThread(view, "root-1", "THREADROOTONE");
      const prefix = within(screen.getByTestId("prefix-thread"));
      expect(prefix.getByTestId("composer-pending-voice-retry")).not.toHaveAttribute(
        "aria-disabled",
      );
      expect(prefix.getByTestId("composer-pending-voice-delete")).not.toHaveAttribute(
        "aria-disabled",
      );

      // …and the exit really works.
      fireEvent.click(prefix.getByTestId("composer-pending-voice-delete"));
      await waitFor(() => {
        expect(
          within(screen.getByTestId("prefix-thread")).queryByTestId("composer-pending-voice"),
        ).toBeNull();
      });
    });
  });
});

describe("ChannelsPage — system #general channel (#642)", () => {
  beforeEach(() => {
    replaceSpy.mockReset();
    mobileViewport.value = false;
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    channelsFixture.current = DEFAULT_CHANNELS;
    messagesFixture.current = { items: [], next_cursor: null };
    navState.search = new URLSearchParams();
  });

  it("sorts the system channel first in the sidebar regardless of API order", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    const rows = screen
      .getAllByRole("button")
      .filter((el) => el.textContent?.includes("general") || el.textContent?.includes("random"));
    const generalIdx = rows.findIndex((el) => el.textContent?.includes("general"));
    const randomIdx = rows.findIndex((el) => el.textContent?.includes("random"));
    expect(generalIdx).toBeGreaterThanOrEqual(0);
    expect(randomIdx).toBeGreaterThanOrEqual(0);
    expect(generalIdx).toBeLessThan(randomIdx);
  });

  it("defaults to the system channel over the first channel when there's no deep-link/remembered target", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
  });

  it("still lets a deep-link to a non-system channel win over the system default", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
  });

  it("still restores a remembered non-system channel over the system default", async () => {
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: "chan-random" });
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
  });

  it("hides the Settings entry for the system channel in Channel details", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    fireEvent.click(screen.getByLabelText("Open channel details"));
    expect(await screen.findByTestId("channel-details-home")).toBeTruthy();
    expect(screen.queryByTestId("channel-details-settings")).toBeNull();
  });

  it("shows the Settings entry for a normal channel in Channel details", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("Open channel details"));
    expect(await screen.findByTestId("channel-details-settings")).toBeTruthy();
  });

  it("hides the per-member remove button in the system channel's member panel", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    fireEvent.click(screen.getByLabelText("View members"));
    const panel = await screen.findByText("Bob");
    const row = panel.closest("div")!;
    expect(within(row.parentElement as HTMLElement).queryByLabelText("Remove member")).toBeNull();
    // Read-only roster: no Invite/Members tab switcher for the system channel.
    expect(screen.queryByText("Invite")).toBeNull();
  });

  it("drops the legacy per-member Remove for a normal group's member panel (owner-only menu now; #801)", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    // Presence stack opens the Members (browse) tab directly.
    fireEvent.click(screen.getByLabelText("View members"));
    await screen.findByText("Bob");
    // Ordinary groups route removal through the owner-only management menu (mock
    // to #801). The ungated legacy per-member Remove must NOT remain reachable —
    // it let a non-channel-owner (or the owner's own row) remove members outside
    // the gate. This viewer's channel role is fail-closed to member, so no ⋯ menu.
    expect(screen.queryByLabelText("Remove member")).toBeNull();
  });

  it("hides the mobile details Settings row for the system channel", async () => {
    mobileViewport.value = true;
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    fireEvent.click(screen.getByLabelText("More"));
    expect(await screen.findByTestId("channel-details-home")).toBeTruthy();
    expect(screen.queryByTestId("channel-details-settings")).toBeNull();
    // Members stay reachable from the Slack members row.
    expect(screen.getByTestId("channel-details-members-row")).toBeTruthy();
  });

  it("keeps the mobile details Settings row for a normal channel", async () => {
    mobileViewport.value = true;
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("More"));
    expect(await screen.findByTestId("channel-details-settings")).toBeTruthy();
  });

  // Slack-style header: faces + count open View-members (browse). System
  // #general has no Invite text button (read-only auto-managed roster).
  it("desktop header shows View-members presence trigger for the system channel, no Invite / +", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    const trigger = screen.getByLabelText("View members");
    expect(trigger).toBeTruthy();
    expect(screen.queryByLabelText("Invite people")).toBeNull();
    expect(screen.queryByLabelText("Manage members")).toBeNull();
    // Presence trigger itself must not carry a hollow "+" affordance.
    expect(trigger.querySelector("svg.lucide-plus")).toBeNull();
  });

  // LRM-447 — Invite left the wide header rail (Members · Search · Stop only).
  // Normal channels still reach Invite via Members dialog / overflow menu.
  it("desktop header keeps View-members without Invite on the action rail (LRM-447)", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    expect(screen.getByLabelText("View members")).toBeTruthy();
    expect(screen.queryByLabelText("Invite people")).toBeNull();
    expect(screen.queryByLabelText("Manage members")).toBeNull();
  });

  // Iris/Parker review of PR #810's first head: code/design PASS on the
  // surface sweep, but flagged these 3 regressions as missing before code
  // GO. All three lock a #general edge case without touching the two
  // pre-existing auto-select effects' architecture (explicitly out of
  // scope for this PR).
  it("mobile cold-load with no deep-link/remembered stays list-first, not stolen by #general", async () => {
    mobileViewport.value = true;
    renderPage();
    // Give the auto-select effects a chance to run — mobile must never
    // land on a detail view (system channel or otherwise) on a bare load.
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByTestId("active-title")).not.toBeInTheDocument();
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it("desktop active → resize to mobile → back to list stays list, not re-grabbed by #general (Iris timing fix)", async () => {
    // The merged auto-select effect must sync its previous/current mobile
    // snapshot on EVERY run, not only when there's no active selection —
    // otherwise the snapshot goes stale while a channel is selected, and
    // clearing the selection afterward misreads as a fresh
    // desktop→mobile transition and wrongly re-grabs #general.
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    });
    const page = (channelId?: string) => (
      <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
        <QueryClientProvider client={qc}>
          <ChannelsPage channelId={channelId} />
        </QueryClientProvider>
      </I18nProvider>
    );
    const { rerender } = render(page("chan-random"));
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    // Resize to mobile while chan-random is still the active selection.
    mobileViewport.value = true;
    rerender(page("chan-random"));
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    // "Back to list" — clears the selection client-side, same instance,
    // no remount (mobileBackToList).
    fireEvent.click(screen.getByLabelText("Back"));
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByTestId("active-title")).not.toBeInTheDocument();
  });

  it("desktop still falls back to the first channel when no system channel exists at all", async () => {
    channelsFixture.current = DEFAULT_CHANNELS.filter((c) => c.system_key !== "general");
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
  });

  it("degrades an unrecognized system_key to a normal, fully-mutable channel", async () => {
    channelsFixture.current = [
      { ...DEFAULT_CHANNELS[0], id: "chan-unknown-key", name: "unknownkey", system_key: "future" },
    ];
    renderPage("chan-unknown-key");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("unknownkey");
    });
    expect(screen.getByLabelText("Open channel details")).toBeTruthy();
  });

  it("degrades an absent system_key to a normal, fully-mutable channel", async () => {
    channelsFixture.current = [{ ...DEFAULT_CHANNELS[0], id: "chan-no-key", name: "nokey" }];
    renderPage("chan-no-key");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("nokey");
    });
    fireEvent.click(screen.getByLabelText("Open channel details"));
    expect(await screen.findByTestId("channel-details-settings")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("View members"));
    await screen.findByText("Bob");
    // "Mutable" is evidenced by the Settings surface (asserted above); the system
    // channel hides it. The legacy per-member Remove is gone for ordinary groups
    // (owner-only menu; #801), so it must NOT be present here either.
    expect(screen.queryByLabelText("Remove member")).toBeNull();
  });

  describe("group leave affordance — real page wiring (#1298)", () => {
    beforeEach(() => {
      toastMock.success.mockClear();
      toastMock.error.mockClear();
      toastMock.info.mockClear();
      const remove = apiMock.proxy.removeChannelMember as ReturnType<typeof vi.fn>;
      remove.mockReset();
      remove.mockResolvedValue(undefined);
    });

    async function openLeaveDanger(channelId: string) {
      renderPage(channelId);
      await screen.findByTestId("message-list");
      fireEvent.click(screen.getByLabelText("Open channel details"));
      fireEvent.click(await screen.findByTestId("channel-details-settings"));
    }

    it("actionable leave → exact self removeChannelMember + deselect + success toast", async () => {
      const remove = apiMock.proxy.removeChannelMember as ReturnType<typeof vi.fn>;
      await openLeaveDanger("chan-random");
      const leave = await screen.findByTestId("channel-details-leave");
      fireEvent.click(within(leave).getByRole("button"));
      await waitFor(() =>
        expect(remove).toHaveBeenCalledWith("chan-random", "user", "user-1"),
      );
      await waitFor(() => expect(toastMock.success).toHaveBeenCalledTimes(1));
      // Deselected off the channel just left.
      await waitFor(() =>
        expect(screen.getByTestId("active-title")).not.toHaveTextContent("random"),
      );
      expect(toastMock.error).not.toHaveBeenCalled();
    });

    it("409 → retain the selected channel, transfer-first toast, no success/deselect", async () => {
      const remove = apiMock.proxy.removeChannelMember as ReturnType<typeof vi.fn>;
      remove.mockReset();
      remove.mockRejectedValue(new ApiError("only owner", 409, "Conflict"));
      await openLeaveDanger("chan-random");
      fireEvent.click(
        within(await screen.findByTestId("channel-details-leave")).getByRole("button"),
      );
      await waitFor(() =>
        expect(remove).toHaveBeenCalledWith("chan-random", "user", "user-1"),
      );
      await waitFor(() =>
        expect(toastMock.error).toHaveBeenCalledWith(
          "Transfer ownership before leaving the group.",
          expect.objectContaining({ duration: Infinity, closeButton: true }),
        ),
      );
      expect(toastMock.success).not.toHaveBeenCalled();
      // Channel retained — never navigate away on a server rejection.
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    it("sole human owner → disabled Leave (no button)", async () => {
      membersByChannel["chan-owned"] = [
        {
          member_type: "user",
          member_id: "user-1",
          name: "alice",
          display_name: "Alice",
          avatar_url: null,
          role: "owner",
        },
      ];
      channelsFixture.current = [
        { ...DEFAULT_CHANNELS[0], id: "chan-owned", name: "owned" },
      ];
      await openLeaveDanger("chan-owned");
      const leave = await screen.findByTestId("channel-details-leave");
      expect(leave).toHaveTextContent("Leave group");
      expect(within(leave).queryByRole("button")).toBeNull();
    });

    it("system channel → no leave affordance", async () => {
      renderPage("chan-general");
      await screen.findByTestId("message-list");
      fireEvent.click(screen.getByLabelText("Open channel details"));
      await screen.findByTestId("channel-details-home");
      expect(screen.queryByTestId("channel-details-leave")).toBeNull();
    });

    it("non-group active channel (kind !== group) → no leave affordance", async () => {
      // A real DM routes through a separate DmConversation shell (dm-list path,
      // not exercised here); this drives the leave gate's `kind !== "group"` arm
      // directly with a non-group active channel and proves the affordance is
      // omitted regardless of how the pane itself renders.
      channelsFixture.current = [
        { ...DEFAULT_CHANNELS[0], id: "chan-dm", name: "dm", kind: "dm" },
      ];
      renderPage("chan-dm");
      await screen.findByTestId("message-list");
      expect(screen.queryByTestId("channel-details-leave")).toBeNull();
    });

    it("archived ordinary group → no leave affordance", async () => {
      channelsFixture.current = [
        {
          ...DEFAULT_CHANNELS[0],
          id: "chan-arch",
          name: "arch",
          archived_at: "2026-07-01T00:00:00Z",
        },
      ];
      await openLeaveDanger("chan-arch");
      expect(screen.queryByTestId("channel-details-leave")).toBeNull();
    });
  });
});

// #833 — the group owner could not remove ANYONE. The ⋯ menu's "Remove from
// group" routed to a handler that only showed a toast, while the standalone
// Remove button renders `only when there is no menu` — so the moment the menu
// appeared (i.e. for the owner, the one person allowed to remove) the real
// affordance disappeared and the fake one took its place. The two entry points
// cancelled out. These pin the real contract: the menu item performs the actual
// removal, with the same permission gate and the same mobile confirm step.
describe("ChannelsPage — group member removal is really wired (#833)", () => {
  const OWNER_ROSTER = [
    {
      member_type: "user",
      member_id: "user-1",
      name: "alice",
      display_name: "Alice",
      avatar_url: null,
      role: "owner",
    },
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
      role: "member",
    },
  ];
  const ORIGINAL_RANDOM = membersByChannel["chan-random"] ?? [];

  beforeEach(() => {
    replaceSpy.mockReset();
    mobileViewport.value = false;
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    channelsFixture.current = DEFAULT_CHANNELS;
    messagesFixture.current = { items: [], next_cursor: null };
    navState.search = new URLSearchParams();
    // The viewer (user-1) is the group OWNER here — that is the only role that
    // gets the management menu, and therefore the only role that hit the bug.
    membersByChannel["chan-random"] = OWNER_ROSTER;
    (apiMock.proxy as Record<string, { mockClear: () => void }>).removeChannelMember?.mockClear();
  });

  afterEach(() => {
    membersByChannel["chan-random"] = ORIGINAL_RANDOM;
  });

  async function openOwnerMemberMenu() {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("View members"));
    // Sentinel: the roster really rendered. Without it, "no menu" / "no call"
    // assertions below could pass simply because nothing had mounted yet.
    await screen.findByText("Bob");
    const trigger = screen.getByLabelText("Member actions");
    fireEvent.click(trigger);
    return screen.findByText("Remove from group");
  }

  // Removal is irreversible, so the confirm step is not a mobile nicety — it
  // gates BOTH platforms. Desktop used to mutate immediately, and the menu
  // rewire would have made that one-click-and-they're-gone path primary.
  it.each([
    ["desktop", false],
    ["mobile", true],
  ])("%s: menu Remove asks first — nothing is removed before Confirm", async (_name, mobile) => {
    mobileViewport.value = mobile;
    const removeItem = await openOwnerMemberMenu();
    fireEvent.click(removeItem);

    // The confirm step is up and NOTHING has been removed yet. This is the
    // assertion that matters: an irreversible action must not fire on the
    // first click.
    const confirm = await screen.findByRole("button", { name: "Confirm remove" });
    expect(apiMock.proxy.removeChannelMember).not.toHaveBeenCalled();

    fireEvent.click(confirm);
    await waitFor(() => {
      expect(apiMock.proxy.removeChannelMember).toHaveBeenCalledWith(
        "chan-random",
        "user",
        "user-2",
      );
    });
  });

  it("a FAILED removal says so — silence would read as 'my click did nothing'", async () => {
    (
      apiMock.proxy as Record<string, { mockRejectedValueOnce: (e: unknown) => void } | undefined>
    ).removeChannelMember?.mockRejectedValueOnce(new Error("boom"));

    const removeItem = await openOwnerMemberMenu();
    fireEvent.click(removeItem);
    fireEvent.click(await screen.findByRole("button", { name: "Confirm remove" }));

    // There is no optimistic removal, so on failure nothing on screen changes —
    // without this toast the owner cannot tell a failure from a no-op.
    await waitFor(() => {
      expect(toastMock.error).toHaveBeenCalled();
    });

    // POSITIVE CONTROL (#838's lesson, applied here): the assertion above only
    // proves a failure was announced — it cannot distinguish "the request went
    // out and the server refused" from "we never got as far as sending". A
    // throw anywhere before the call (e.g. an imperative store read against a
    // selector-only mock) is swallowed by react-query as a mutation failure and
    // looks identical. Note the mock above is set with `?.`, so if this proxy
    // method were ever renamed the rejection would silently not be installed
    // and this test would still pass on some other failure. Pin the send.
    expect(apiMock.proxy.removeChannelMember).toHaveBeenCalledWith(
      expect.any(String),
      "user",
      expect.any(String),
    );
  });

  // #839 — the toast is the announcement, NOT the record. Dismissing it (or its
  // 4s default lifetime expiring) must not erase the fact that the removal
  // failed, so the failure also lands in the target's own row.
  // LRM-1327 / LRM-1300 §5: failure keeps the dialog open — dismiss Cancel
  // first so the row notice is not under aria-hidden.
  async function failRemoveAndDismissConfirm() {
    (
      apiMock.proxy as Record<string, { mockRejectedValueOnce: (e: unknown) => void } | undefined>
    ).removeChannelMember?.mockRejectedValueOnce(new Error("boom"));

    const removeItem = await openOwnerMemberMenu();
    fireEvent.click(removeItem);
    fireEvent.click(await screen.findByRole("button", { name: "Confirm remove" }));
    await waitFor(() => {
      expect(toastMock.error).toHaveBeenCalled();
    });
    // Dialog stays open on failure; Cancel reveals the in-row record.
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    return screen.findByTestId("channel-members-row-remove-failed");
  }

  it("a failed removal leaves a durable in-row notice — surviving the toast", async () => {
    const notice = await failRemoveAndDismissConfirm();
    expect(notice).toHaveTextContent("Couldn't remove this member");
    expect(screen.getByTestId("channel-members-row-remove-retry")).toBeInTheDocument();
    // Scoped to the failed member's row, not a global banner.
    expect(
      notice.closest("[data-testid='channel-members-row']"),
    ).not.toBeNull();
  });

  it("retry re-opens the confirmation — it never removes on one click (#839)", async () => {
    await failRemoveAndDismissConfirm();

    const callsAfterFailure = (
      apiMock.proxy.removeChannelMember as ReturnType<typeof vi.fn>
    ).mock.calls.length;

    fireEvent.click(screen.getByTestId("channel-members-row-remove-retry"));

    // The confirmation is back and NOTHING was requested by the retry click —
    // the second confirmation stays the destructive commitment point.
    await screen.findByRole("button", { name: "Confirm remove" });
    expect(
      (apiMock.proxy.removeChannelMember as ReturnType<typeof vi.fn>).mock.calls.length,
    ).toBe(callsAfterFailure);
  });

  it("a successful retry clears the notice — the row (and its state) go together (#839)", async () => {
    const remove = apiMock.proxy.removeChannelMember as ReturnType<typeof vi.fn>;
    await failRemoveAndDismissConfirm();

    // Retry → confirm again, this time the request succeeds.
    remove.mockResolvedValueOnce(undefined);
    fireEvent.click(screen.getByTestId("channel-members-row-remove-retry"));
    fireEvent.click(await screen.findByRole("button", { name: "Confirm remove" }));

    await waitFor(() => {
      expect(screen.queryByTestId("channel-members-row-remove-failed")).toBeNull();
    });
  });

  it("the in-row notice clears only when the user dismisses it (#839)", async () => {
    await failRemoveAndDismissConfirm();

    fireEvent.click(screen.getByTestId("channel-members-row-remove-dismiss"));
    await waitFor(() => {
      expect(screen.queryByTestId("channel-members-row-remove-failed")).toBeNull();
    });
  });

  it("an ARCHIVED group exposes no member-management menu at all", async () => {
    channelsFixture.current = DEFAULT_CHANNELS.map((c) =>
      (c as { id: string }).id === "chan-random"
        ? { ...(c as object), archived_at: "2026-07-01T00:00:00Z" }
        : c,
    );
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("View members"));
    // Sentinel first — the roster IS on screen, so the absence below is a real
    // gate rather than an unmounted panel.
    await screen.findByText("Bob");
    expect(screen.queryByLabelText("Member actions")).toBeNull();
  });
});

// #576 — create-group dialog project field. The channels-page scope shipped
// the group-settings Project section (#800, ChannelProjectSettingsPanel) for
// binding an EXISTING channel to a project; this covers the remaining piece —
// picking a project AT CREATION time in the same inline create-channel
// popover (channels-page.tsx's sidebar "+" Popover), reusing the identical
// ProjectPickerButton + PropRow pattern instead of a bespoke picker. Leaving
// the field untouched must behave exactly like the pre-existing create flow
// (no `project_id` on the wire).

function openCreatePopover() {
  // The sidebar "+" trigger and the popover's submit button share the same
  // accessible name ("Create channel") once the popover is open, so grab the
  // trigger while it's still the only match.
  fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
}

describe("ChannelsPage create-group popover — optional project field (#576)", () => {
  beforeEach(() => {
    // Fresh-file environment for this suite: its source file started with an
    // empty jsdom + default fixtures, so reset everything earlier suites may
    // have touched (see the merged-file header note).
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    mobileViewport.value = false;
    navState.search = new URLSearchParams();
    channelsFixture.current = [
    {
      id: "chan-1",
      workspace_id: "ws-1",
      name: "general",
      kind: "group" as const,
      description: null,
      lark_chat_id: null,
      created_by: "user-1",
      created_at: "2026-06-17T09:00:00Z",
      updated_at: "2026-06-17T09:00:00Z",
    },
  ];
    messagesFixture.current = { messages: [], next_cursor: null };
    threadsFixture.current = {};
    projectsFixture.current = [
      { id: "proj-1", title: "Apollo" },
      { id: "proj-2", title: "Zeus" },
    ];
    pickerStub.functional = true;
    apiMock.createChannel.mockClear();
  });

  afterEach(() => {
    pickerStub.functional = false;
  });

  it("renders an optional project field in the create-group popover, defaulted to unset", async () => {
    renderPage();
    openCreatePopover();

    await waitFor(() => {
      expect(screen.getByPlaceholderText("Channel name")).toBeInTheDocument();
    });
    // Reuses the same "Project" / "No project" copy as the group-settings panel.
    expect(screen.getByText("Project")).toBeInTheDocument();
    expect(screen.getByText("No project")).toBeInTheDocument();
    expect(screen.getByText("picker:none")).toBeInTheDocument();
  });

  it("includes the selected project id in the create submission", async () => {
    renderPage();
    openCreatePopover();

    const nameInput = await screen.findByPlaceholderText("Channel name");
    fireEvent.change(nameInput, { target: { value: "New Group" } });

    fireEvent.click(screen.getByLabelText("Project: pick"));
    await waitFor(() => expect(screen.getByText("Apollo")).toBeInTheDocument());

    fireEvent.keyDown(nameInput, { key: "Enter" });

    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalledTimes(1));
    expect(apiMock.createChannel).toHaveBeenCalledWith(
      expect.objectContaining({ name: "New Group", project_id: "proj-1" }),
    );
  });

  it("omits project_id when no project is picked — existing create flow unaffected", async () => {
    renderPage();
    openCreatePopover();

    const nameInput = await screen.findByPlaceholderText("Channel name");
    fireEvent.change(nameInput, { target: { value: "Plain Group" } });
    fireEvent.keyDown(nameInput, { key: "Enter" });

    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalledTimes(1));
    const payload = apiMock.createChannel.mock.calls[0]?.[0] as { project_id?: string | null };
    expect(payload).toMatchObject({ name: "Plain Group", lark_chat_id: undefined });
    // Not just falsy — genuinely absent from the wire payload (JSON.stringify
    // drops an `undefined` value), matching the pre-#576 create request shape.
    expect(payload.project_id).toBeUndefined();
  });

  it("create-group UI has no auto-Beckham / group_manager affordance (LRM-399)", async () => {
    renderPage();
    openCreatePopover();
    const popover = await screen.findByPlaceholderText("Channel name");
    const root = popover.closest("[data-slot='popover-content'], [role='dialog'], div") ?? document.body;
    const text = (root.textContent ?? "").toLowerCase();
    expect(text).not.toMatch(/beckham|贝克汉姆|group[_\s-]?manager|自动.*群管|auto.?provision/);
    // Create payload stays name/project only — no manager flag.
    fireEvent.change(popover, { target: { value: "No Manager Group" } });
    fireEvent.keyDown(popover, { key: "Enter" });
    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalledTimes(1));
    expect(Object.keys(apiMock.createChannel.mock.calls[0]?.[0] ?? {})).toEqual(
      expect.arrayContaining(["name"]),
    );
    expect(apiMock.createChannel.mock.calls[0]?.[0]).not.toHaveProperty(
      "provision_group_manager",
    );
  });

  it("pins the freshly-created group to the creator's own sidebar (Beckham v2 §4)", async () => {
    const pinChannel = apiMock.proxy.pinChannel as ReturnType<typeof vi.fn>;
    pinChannel.mockClear();
    renderPage();
    openCreatePopover();
    const nameInput = await screen.findByPlaceholderText("Channel name");
    fireEvent.change(nameInput, { target: { value: "New Group" } });
    fireEvent.keyDown(nameInput, { key: "Enter" });
    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalledTimes(1));
    // The created group (kind:"group", id "chan-new") is pinned for the creator
    // only — a per-user pin reusing the existing channel pin, best-effort.
    await waitFor(() => expect(pinChannel).toHaveBeenCalledWith("chan-new"));
  });

  it("surfaces a non-blocking toast when the creator pin fails — creation still succeeds (Beckham v2 §4, Iris)", async () => {
    const pinChannel = apiMock.proxy.pinChannel as ReturnType<typeof vi.fn>;
    pinChannel.mockClear();
    pinChannel.mockRejectedValueOnce(new Error("pin failed"));
    const infoToast = toast.info as ReturnType<typeof vi.fn>;
    infoToast.mockClear();
    renderPage();
    openCreatePopover();
    const nameInput = await screen.findByPlaceholderText("Channel name");
    fireEvent.change(nameInput, { target: { value: "New Group" } });
    fireEvent.keyDown(nameInput, { key: "Enter" });
    // Creation proceeds and the pin is still attempted…
    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(pinChannel).toHaveBeenCalledWith("chan-new"));
    // …but the pin rejection is NOT swallowed — an info toast tells the user
    // the group exists yet wasn't pinned and can be pinned manually.
    await waitFor(() =>
      expect(infoToast).toHaveBeenCalledWith(
        "Group created, but it couldn't be pinned. You can pin it manually later.",
      ),
    );
  });
});

/**
 * #832 — the page decides which role failures may be retried.
 *
 * The gap this closes: `owner_changed` and `gone` were covered at BOTH ends and
 * nowhere in the middle.
 *   - core `role-change-failure.test.ts` proves error → kind;
 *   - `channel-members-list-role-pending.test.tsx` proves that a descriptor
 *     WITHOUT `onRetry` renders no Retry button — but the test hands it that
 *     descriptor directly.
 * Nothing drove the page from a real failure to the descriptor, and the page is
 * where the decision lives (`channels-page.tsx`, `retryable = kind ===
 * "transient"`). Relaxing that line to `!== "forbidden"`, or to `true`, left
 * the entire core + views suite green.
 *
 * Same shape as the transfer seam in #1367: two self-consistent halves, and the
 * joint between them untested.
 *
 * These render the real page, so the real `classifyRoleChangeFailure` →
 * `roleFailures` → `roleFailureFor` chain runs; only the surrounding chrome is
 * stubbed. Copy comes from the real `en/channels.json`, not a mock dictionary —
 * a hand-written dictionary drifts and renders "" instead of failing.
 *
 * HOW TO FLIP-VERIFY: widen `retryable` in channels-page.tsx (e.g. `kind !==
 * "forbidden"`) → the owner_changed and gone cases go red while the transient
 * case stays green. That asymmetry is the point: a guard that reddens for every
 * mutation is not discriminating between these kinds.
 */

/** Open the roster and fire "promote" on Bob's row. */
async function promoteBob() {
  renderPage();
  fireEvent.click(await screen.findByText("open-members"));
  // Exactly one row offers a menu: the viewer's own row never does, so this is
  // Bob's. Asserted rather than indexed blindly — if that ever changes, this
  // fails here instead of silently driving the wrong row.
  const triggers = await screen.findAllByLabelText("Member actions");
  expect(triggers).toHaveLength(1);
  fireEvent.click(triggers[0]!);
  fireEvent.click(await screen.findByTestId("group-member-menu-promote"));
}

// The viewer is the group owner, so the management menu is offered; `bob` is an
// ordinary member, so `promote` is available on his row.
const ROLE_FAILURE_MEMBERS = [
  {
    channel_id: "chan-1",
    member_id: "user-1",
    member_type: "user" as const,
    display_name: "Alice",
    role: "owner" as const,
    joined_at: "2026-06-17T09:00:00Z",
  },
  {
    channel_id: "chan-1",
    member_id: "user-2",
    member_type: "user" as const,
    display_name: "Bob",
    role: "member" as const,
    joined_at: "2026-06-17T09:00:00Z",
  },
];

describe("ChannelsPage — which role failures offer Retry (#832)", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    mobileViewport.value = false;
    navState.search = new URLSearchParams();
    channelsFixture.current = [
    {
      id: "chan-1",
      workspace_id: "ws-1",
      name: "general",
      kind: "group" as const,
      description: null,
      lark_chat_id: null,
      created_by: "user-1",
      created_at: "2026-06-17T09:00:00Z",
      updated_at: "2026-06-17T09:00:00Z",
    },
  ];
    membersByChannel["chan-1"] = ROLE_FAILURE_MEMBERS;
    messagesFixture.current = { messages: [], next_cursor: null };
    threadsFixture.current = {};
    projectsFixture.current = [];
    // Chrome-only stubs — this suite drives the REAL members roster through a
    // panel that renders membersBody directly; other suites need the real
    // details panel + presence trigger, so lower the flags again after.
    chromeStub.detailsPanel = true;
    chromeStub.presenceCue = true;
    apiMock.updateChannelMemberRole.mockReset();
  });

  afterEach(() => {
    chromeStub.detailsPanel = false;
    chromeStub.presenceCue = false;
    delete membersByChannel["chan-1"];
  });

  it("owner_changed: shows its own message and NO retry — the roster moved, repeating cannot help", async () => {
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("someone else took ownership", 403, "Forbidden", { code: "owner_changed" }),
    );

    await promoteBob();

    expect(
      await screen.findByText("Ownership has changed; the member list has been refreshed."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("channel-members-row-role-retry")).toBeNull();
  });

  it("gone: shows its own message and NO retry — the target is no longer there", async () => {
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("not found", 404, "Not Found"),
    );

    await promoteBob();

    expect(
      await screen.findByText("The member or channel state has changed. Refresh and try again."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("channel-members-row-role-retry")).toBeNull();
  });

  it("transient: DOES offer retry — without this the other two prove nothing", async () => {
    // The positive control. If the page stopped offering Retry entirely, the two
    // assertions above would still pass while the feature was silently dead.
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("upstream boom", 503, "Service Unavailable"),
    );

    await promoteBob();

    expect(
      await screen.findByText("Couldn't update the member's role. Please try again."),
    ).toBeInTheDocument();
    expect(screen.getByTestId("channel-members-row-role-retry")).toBeInTheDocument();
  });

  it("retry re-issues the SAME action, not a default one", async () => {
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("upstream boom", 503, "Service Unavailable"),
    );

    await promoteBob();
    await screen.findByTestId("channel-members-row-role-retry");
    apiMock.updateChannelMemberRole.mockClear();
    fireEvent.click(screen.getByTestId("channel-members-row-role-retry"));

    await waitFor(() => expect(apiMock.updateChannelMemberRole).toHaveBeenCalled());
    // promote → "manager"; a retry that sent "member" would be a demotion the
    // user never asked for.
    expect(apiMock.updateChannelMemberRole).toHaveBeenCalledWith(
      "chan-1",
      "user",
      "user-2",
      "manager",
    );
  });
});

// #656 Reminder anchor `?thread=<root>&message=<reply>` deep-link: it must
// really open ThreadPanel and highlight the reply inside it — a plain
// `?message=` main-timeline highlight (what channels-page-routing.test.tsx
// already covers) is NOT the same thing and doesn't satisfy this.

const THREAD_ROOT_ID = "root-msg-1";
const THREAD_REPLY_ID = "reply-msg-1";
const SECOND_THREAD_ROOT_ID = "root-msg-2";
const SECOND_THREAD_REPLY_ID = "reply-msg-2";

const REMINDER_THREADS: Record<string, unknown[]> = {
  [THREAD_ROOT_ID]: [
    {
      id: THREAD_ROOT_ID,
      channel_id: "chan-1",
      workspace_id: "ws-1",
      seq: 1,
      type: "user",
      author_id: "user-2",
      author_name: "Bob",
      content: "root",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      created_at: "2026-07-20T00:00:00Z",
    },
    {
      id: THREAD_REPLY_ID,
      channel_id: "chan-1",
      workspace_id: "ws-1",
      seq: 2,
      type: "user",
      author_id: "user-2",
      author_name: "Bob",
      content: "the anchored reply",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      thread_root_message_id: THREAD_ROOT_ID,
      created_at: "2026-07-21T01:00:00Z",
    },
  ],
  [SECOND_THREAD_ROOT_ID]: [
    {
      id: SECOND_THREAD_ROOT_ID,
      channel_id: "chan-1",
      workspace_id: "ws-1",
      seq: 3,
      type: "user",
      author_id: "user-2",
      author_name: "Bob",
      content: "second root",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      created_at: "2026-07-20T00:00:00Z",
    },
    {
      id: SECOND_THREAD_REPLY_ID,
      channel_id: "chan-1",
      workspace_id: "ws-1",
      seq: 4,
      type: "user",
      author_id: "user-2",
      author_name: "Bob",
      content: "the second anchored reply",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      thread_root_message_id: SECOND_THREAD_ROOT_ID,
      created_at: "2026-07-22T01:00:00Z",
    },
  ],
};

describe("ChannelsPage — Reminder anchor ?thread=&message= deep-link (#656)", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    mobileViewport.value = false;
    channelsFixture.current = [
    {
      id: "chan-1",
      workspace_id: "ws-1",
      name: "general",
      kind: "group" as const,
      description: null,
      lark_chat_id: null,
      created_by: "user-1",
      created_at: "2026-06-17T09:00:00Z",
      updated_at: "2026-06-17T09:00:00Z",
    },
  ];
    messagesFixture.current = { messages: [], limit: 50, has_more: false, next_cursor: null };
    threadsFixture.current = REMINDER_THREADS;
    projectsFixture.current = [];
    currentSearchParams.value = new URLSearchParams({ thread: THREAD_ROOT_ID, message: THREAD_REPLY_ID });
  });

  it("opens ThreadPanel (not just a main-timeline highlight) and routes the highlight to the reply inside it", async () => {
    renderPage("chan-1");

    // Both the main conversation header and ThreadPanel's own header render
    // through the same mocked ConversationHeader, so this now matches two —
    // the group's own title is enough to confirm the right channel resolved.
    await waitFor(() => {
      expect(
        screen.getAllByTestId("active-title").some((el) => el.textContent?.includes("general")),
      ).toBe(true);
    });

    // Two ChannelMessageList instances now exist: the main timeline and the
    // ThreadPanel's reply list. The reply-list one is the one carrying the
    // highlight target and the anchored reply.
    const lists = await screen.findAllByTestId("message-list");
    expect(lists.length).toBeGreaterThanOrEqual(2);
    const threadList = lists.find((el) => el.querySelector(`[data-testid="msg-${THREAD_REPLY_ID}"]`));
    expect(threadList).toBeTruthy();
    expect(threadList).toHaveAttribute("data-highlight", THREAD_REPLY_ID);

    // The main timeline must NOT have absorbed the highlight — it belongs to
    // a message that was never in the main list's page.
    const mainList = lists.find((el) => el !== threadList);
    expect(mainList).toHaveAttribute("data-highlight", "");
  });

  it("LRM-740: embedded Thread avatar opens Member dock (not skeleton) and close restores Thread", async () => {
    renderPage("chan-1", { embedded: true, embeddedSurface: "thread" });

    await waitFor(() => {
      expect(screen.getByTestId(`msg-${THREAD_REPLY_ID}`)).toBeInTheDocument();
    });
    // Thread reply list is the one that received onOpenMember from ThreadPanel.
    const openMember = screen.getAllByTestId("list-open-member").at(-1)!;
    fireEvent.click(openMember);

    await waitFor(() => {
      expect(screen.getByTestId("member-side-panel")).toHaveAttribute(
        "data-user-id",
        "user-9",
      );
    });
    // Profile replaced Thread in the exclusive slot — must not be a skeleton gap.
    expect(screen.queryByTestId(`msg-${THREAD_REPLY_ID}`)).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("member-side-close"));

    await waitFor(() => {
      expect(screen.getByTestId(`msg-${THREAD_REPLY_ID}`)).toBeInTheDocument();
    });
    expect(screen.queryByTestId("member-side-panel")).not.toBeInTheDocument();
  });

  it("LRM-740: embedded Thread avatar opens Agent dock and close restores Thread", async () => {
    renderPage("chan-1", { embedded: true, embeddedSurface: "thread" });

    await waitFor(() => {
      expect(screen.getByTestId(`msg-${THREAD_REPLY_ID}`)).toBeInTheDocument();
    });
    fireEvent.click(screen.getAllByTestId("list-open-agent").at(-1)!);

    await waitFor(() => {
      expect(screen.getByTestId("agent-side-panel")).toHaveAttribute(
        "data-agent-id",
        "agent-9",
      );
    });

    fireEvent.click(screen.getByTestId("agent-side-close"));

    await waitFor(() => {
      expect(screen.getByTestId(`msg-${THREAD_REPLY_ID}`)).toBeInTheDocument();
    });
  });

  it("opens a SECOND, different thread when a same-pathname AppLink navigation changes ?thread=&message= without remounting (no full reload)", async () => {
    const { rerender, qc } = renderPage("chan-1");

    await waitFor(() => {
      expect(
        screen.getAllByTestId("active-title").some((el) => el.textContent?.includes("general")),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(screen.queryByTestId(`msg-${THREAD_REPLY_ID}`)).toBeInTheDocument();
    });

    // Simulate an AppLink push: only searchParams changes (new URLSearchParams
    // instance, same pathname) — the SAME QueryClient/component tree stays
    // mounted, proving this isn't a full reload. A one-shot mount-time read
    // would silently miss this.
    currentSearchParams.value = new URLSearchParams({
      thread: SECOND_THREAD_ROOT_ID,
      message: SECOND_THREAD_REPLY_ID,
    });
    rerender(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
        <QueryClientProvider client={qc}>
          <ChannelsPage channelId="chan-1" />
        </QueryClientProvider>
      </I18nProvider>,
    );

    await waitFor(() => {
      expect(screen.queryByTestId(`msg-${SECOND_THREAD_REPLY_ID}`)).toBeInTheDocument();
    });
    const lists = await screen.findAllByTestId("message-list");
    const secondThreadList = lists.find((el) =>
      el.querySelector(`[data-testid="msg-${SECOND_THREAD_REPLY_ID}"]`),
    );
    expect(secondThreadList).toHaveAttribute("data-highlight", SECOND_THREAD_REPLY_ID);
  });
});

// A production incident (2026-07-31): createOrFind's optimistic setQueryData
// puts the new DM in the list, but the invalidate-triggered refetch that
// follows can come back from a backend gap without it — `dms` goes back to
// `[]`, `activeDm` resolves to null forever, and ConversationSwitchSkeleton
// (built for the brief "list hasn't caught up yet" window) spun forever. That
// reads as a blank page to the user. This suite reproduces the same shape —
// a DM that resolves once, then vanishes from the list — entirely through
// dmListFixture, and proves the page now surfaces an explicit retry instead
// of a stuck skeleton.
describe("ChannelsPage — DM that never reappears in the list shows a retry, not a stuck skeleton", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    mobileViewport.value = false;
    channelsFixture.current = [];
    currentSearchParams.value = new URLSearchParams();
    dmListFixture.current = [
      {
        id: "dm-1",
        source: "dm_channel",
        peer: { type: "agent", id: "agent-1", name: "Wendy" },
        unread: 0,
        updated_at: "2026-07-31T00:00:00Z",
      },
    ];
  });

  afterEach(() => {
    vi.useRealTimers();
    dmListFixture.current = [];
  });

  it("swaps the stuck skeleton for a retry affordance after the timeout, and retry recovers once the DM reappears", async () => {
    const { qc } = renderPage("dm-1");

    // Resolves normally at first — proves the DM path itself isn't broken.
    await screen.findByTestId("dm-conversation");

    // Fake timers from here on, so the timeout effect's setTimeout (scheduled
    // fresh once `activeDm` goes null below) is one we control.
    vi.useFakeTimers({ shouldAdvanceTime: true });

    // Simulate the list losing the DM on a subsequent fetch (the real bug:
    // an invalidate-triggered refetch that comes back without it). The page
    // reads the unified conversations query (LRM-1399), not the legacy
    // `dm-list` key — invalidate what actually feeds the sidebar.
    dmListFixture.current = [];
    await act(async () => {
      await qc.invalidateQueries({ queryKey: ["conversations", "ws-1", "list"] });
    });
    await waitFor(() => {
      expect(screen.queryByTestId("dm-conversation")).not.toBeInTheDocument();
    });
    // Still within the timeout window: the retry state hasn't appeared yet —
    // this is the skeleton window.
    expect(screen.queryByText("Couldn't open conversation. Please try again.")).not.toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(8000);
    });

    const retryButton = screen.getByText("Couldn't open conversation. Please try again.");

    // Recovery: the DM reappears in the list, then retry clears the failed
    // state and remounts the real conversation.
    dmListFixture.current = [
      {
        id: "dm-1",
        source: "dm_channel",
        peer: { type: "agent", id: "agent-1", name: "Wendy" },
        unread: 0,
        updated_at: "2026-07-31T00:00:00Z",
      },
    ];
    vi.useRealTimers();
    fireEvent.click(retryButton);

    await screen.findByTestId("dm-conversation");
    expect(screen.queryByText("Couldn't open conversation. Please try again.")).not.toBeInTheDocument();
  });
});
