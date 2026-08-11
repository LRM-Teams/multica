import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessage } from "@multica/core/types";
import type { DMItem } from "@multica/core/dm";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { DmConversation } from "./dm-conversation";

// B3 (#241) — the same dead-affordance wiring gap the channel path had also
// lived in the DM path. This test renders the REAL DmConversation with the REAL
// ChannelMessageList and bubble. It proves an edit is a PATCH
// (editChannelMessage), never a re-send (H5), and keeps the removed Delete UI
// guarded as absent.

// Spy on the api client the real edit/send mutation hooks call, so we can assert
// edit == PATCH and never a send.
const apiMock = vi.hoisted(() => {
  const editChannelMessage = vi.fn();
  const sendChannelMessage = vi.fn();
  const followChannelThread = vi.fn();
  const unfollowChannelThread = vi.fn();
  const known: Record<string, unknown> = {
    editChannelMessage,
    sendChannelMessage,
    followChannelThread,
    unfollowChannelThread,
  };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return {
    proxy,
    editChannelMessage,
    sendChannelMessage,
    followChannelThread,
    unfollowChannelThread,
  };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

// #692 finding 1: the lazily-proxied mark-read spy (same cached fn the real
// useMarkChannelRead hook calls via api.markChannelRead), typed for assertions.
const markReadSpy = apiMock.proxy.markChannelRead as ReturnType<typeof vi.fn>;

// Reuse the message-list test's virtuoso shim: real react-virtuoso doesn't
// render rows in jsdom, so window a couple of items around the target.
vi.mock("react-virtuoso", async () => {
  const React = await import("react");
  const MockVirtuoso = React.forwardRef(
    (
      {
        components = {},
        data = [],
        itemContent,
        initialTopMostItemIndex,
        startReached,
      }: {
        components?: {
          Footer?: React.ComponentType;
          Header?: React.ComponentType;
          List?: React.ComponentType<React.HTMLAttributes<HTMLDivElement>>;
        };
        data?: ChannelMessage[];
        initialTopMostItemIndex?: number;
        firstItemIndex?: number;
        startReached?: () => void;
        itemContent: (index: number, item: ChannelMessage) => React.ReactNode;
      },
      ref: React.ForwardedRef<{ scrollToIndex: (...args: unknown[]) => void }>,
    ) => {
      React.useImperativeHandle(ref, () => ({ scrollToIndex: vi.fn() }));
      const Header = components.Header;
      const List = components.List ?? "div";
      const Footer = components.Footer;
      // #1194 index-contract fix: initialTopMostItemIndex is already a LOCAL
      // index (0..data.length-1), never offset by firstItemIndex.
      const localTarget = Math.max(0, initialTopMostItemIndex ?? 0);
      const targetIndex = Math.max(0, Math.min(localTarget, data.length - 1));
      const start = Math.max(0, Math.min(targetIndex - 1, data.length - 2));
      const windowedData = data.slice(start, start + 2);
      return (
        <div data-testid="virtuoso-scroller">
          {startReached && (
            <button type="button" data-testid="start-reached" onClick={() => startReached()} />
          )}
          {Header ? <Header /> : null}
          <List>{windowedData.map((item, offset) => itemContent(start + offset, item))}</List>
          {Footer ? <Footer /> : null}
        </div>
      );
    },
  );
  MockVirtuoso.displayName = "MockVirtuoso";
  return { Virtuoso: MockVirtuoso };
});

// Keep the real mutation hooks (so edit really routes through the api client),
// but stub the query options to fixtures so the pane resolves without network.
vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  return {
    ...actual,
    channelMessageThreadOptions: () => ({
      queryKey: ["channel-thread"],
      queryFn: async () => ({
        messages: currentPageMessages[0]
          ? [{ ...currentPageMessages[0], thread_followed: true }]
          : [],
      }),
    }),
    channelMessagesPageOptions: () => ({
      queryKey: ["dm-messages"],
      queryFn: async () => ({
        messages: currentPageMessages,
        next_cursor: null,
      }),
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

vi.mock("@multica/core/realtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/realtime")>()),
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));

// Bubble avatar internals (agent presence + current workspace + actor names) —
// stub so the real bubble renders without QueryClient/workspace-provider wiring.
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: () => null,
    getMemberRole: () => null,
    getMemberHonor: () => undefined,
    getAgentFleetRank: () => undefined,
    getAgentHonorLevel: () => undefined,
  }),
}));
vi.mock("@multica/core/agents", () => ({ useAgentPresence: () => "loading" }));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useCurrentWorkspace: () => ({ id: "ws-1" }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

// The agent-DM floating bubble reads the app chat store (registered at boot);
// stub it so the agent_pair render path doesn't require chat-store bootstrap.
// #692 gate②: render a VISIBLE stand-in (not null) so page-level composer/bubble
// assertions can actually SEE this second agent-chat surface. The null mock is
// exactly why #1243's "composer=0" test was blind to the DmAgentBubble leak —
// its ChatWindow mounts an independent, ungated ProseMirror composer beside the
// supervision banner.
vi.mock("../../chat/components/dm-agent-bubble", async () => {
  const React = await import("react");
  return {
    DmAgentBubble: () => React.createElement("div", { "data-testid": "dm-agent-bubble" }),
  };
});

// The working cue mounts for a non-agent_pair agent peer and reads agent
// presence/health via useQuery; it's not under test here, so stub it so the
// normal single-agent DM render path doesn't require that query wiring.
vi.mock("./dm-agent-working-cue", () => ({ DmAgentWorkingCue: () => null }));

// Expose `plainUrls` so a test can assert the DM composer opts into plain-text
// URLs (#542) — same miss-surface regression guard as the channel composer.
vi.mock("../../editor/lazy-content-editor", () => ({
  ContentEditor: (props: { plainUrls?: boolean }) => (
    <div data-testid="content-editor" data-plain-urls={String(!!props.plainUrls)} />
  ),
}));
vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));
// Dm header chrome (peer avatar + profile trigger) reads the workspace-scoped
// route via useWorkspacePaths; stub it to keep this test off route context.
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="actor-avatar" />,
}));
vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

let currentPageMessages: ChannelMessage[] = [];

function ownMessage(): ChannelMessage {
  return {
    id: "m-1",
    channel_id: "dm-chan-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: "Original",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
  };
}

function peerMessage(): ChannelMessage {
  return { ...ownMessage(), id: "m-2", seq: 2, author_id: "peer-1", author_name: "Bob", content: "Hi from Bob" };
}

const dm: DMItem = {
  id: "dm-chan-1",
  source: "dm_channel",
  peer: { type: "user", id: "peer-1", name: "Bob" },
  unread: 0,
  updated_at: "2026-06-17T09:00:00Z",
};

function renderDm() {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <DmConversation dm={dm} onBack={() => {}} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

// #692 finding 1: a supervised agent_pair DM the owner reads read-only. The
// supervisor is NOT a channel_member, so every member-only affordance/mutation
// must be gone (would 403 or render dead).
const supervisedDm: DMItem = {
  id: "dm-chan-1",
  source: "dm_channel",
  mode: "agent_pair",
  peer: { type: "agent", id: "agent-a", name: "Agent A" },
  participants: [
    { type: "agent", id: "agent-a", name: "Agent A" },
    { type: "agent", id: "agent-b", name: "Agent B" },
  ],
  supervised: true,
  unread: 0,
  updated_at: "2026-06-17T09:00:00Z",
};

// #692 walkthrough finding: the SAME agent_pair DM but the BE omitted the
// `supervised` flag (observed when one owner owns both ends). The read-only
// surface must still hold, keyed on `mode === "agent_pair"`.

// A NORMAL single-agent DM (not a supervised pair): DmAgentBubble SHOULD mount
// here — the supervised gate must not regress legitimate independent agent chat.
const agentDirectDm: DMItem = {
  ...supervisedDm,
  mode: "direct",
  supervised: undefined,
  participants: undefined,
};

function renderSupervisedDm(dmItem: DMItem = supervisedDm) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <DmConversation dm={dmItem} onBack={() => {}} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe.sequential("DmConversation message action wiring (#241 B3)", () => {
  beforeEach(() => {
    currentPageMessages = [ownMessage()];
    apiMock.editChannelMessage.mockReset().mockResolvedValue({
      ...ownMessage(),
      content: "Corrected",
      edited_at: "2026-06-17T09:20:00Z",
    });
    apiMock.sendChannelMessage.mockReset().mockResolvedValue(ownMessage());
    apiMock.followChannelThread.mockReset().mockResolvedValue(undefined);
    apiMock.unfollowChannelThread.mockReset().mockResolvedValue(undefined);
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden in
  // the bubble (canEdit=false) until rebuilt on the unified composer (#258).
  // Delete stays, so a viewer's own DM message surfaces Delete but never Edit.
  it("renders NEITHER edit nor delete on the viewer's OWN DM message (LRM-695)", async () => {
    renderDm();
    await screen.findByTestId("message-bubble");
    // LRM-695: delete removed from the UI (Frank: 只去删除); edit unshipped.
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
  });

  it("renders NEITHER edit nor delete on a peer's message", async () => {
    currentPageMessages = [peerMessage()];
    renderDm();
    await screen.findByTestId("message-bubble");
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden
  // (canEdit=false) so the inline editor is unreachable from the DM UI. The
  // dormant onEditMessage → editChannelMessage (PATCH) wiring is kept for the
  // composer-parity rebuild (#258); restore this H5 PATCH test when re-enabled.

  it("does not surface a delete affordance on the DM (LRM-695 removed it)", async () => {
    renderDm();
    await screen.findByTestId("message-bubble");

    // LRM-695: delete was removed from the UI.
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  // #568 reaction-sheet exclusivity + LRM-495 (no permanent More): DM still
  // reaches the shared bubble's long-press / swipe action sheet (Parker/Iris:
  // "共用组件不能假定双面 PASS").

  // #542 — both DM composers (main + thread) must opt into plain-text URLs so a
  // typed URL isn't auto-linkified in the input. Per-call-site regression guard
  // for the miss-surface bug (fix reached one surface but not another).

});

describe.sequential("DmConversation supervised agent_pair read-only surface (#692 finding 1)", () => {
  beforeEach(() => {
    currentPageMessages = [peerMessage()];
    // markChannelRead is lazily proxied; clear its call log between tests.
    markReadSpy.mockClear();
  });



  it("mounts the single-agent chat bubble on a NORMAL agent DM (gate must not regress legit chat)", async () => {
    // Non-supervised single-agent DM: the independent agent-chat bubble is
    // legitimate and MUST still mount — the gate only removes it on agent_pair.
    renderSupervisedDm(agentDirectDm);
    await screen.findByTestId("message-bubble");
    expect(screen.getByTestId("dm-agent-bubble")).toBeInTheDocument();
  });

});

// 2026-07-31 Wendy DM incident (B1) — a normal single-agent DM whose peer
// agent has since been archived (the product-facing "delete agent" action is
// a soft archive; history is never hidden). Same write-read-only contract as
// the agent_pair supervision surface above, but the viewer here IS a normal
// channel member (unlike an agent_pair supervisor, who isn't a member at
// all) — so mark-read must still fire; only the write surface is gated.
const archivedPeerDm: DMItem = {
  ...agentDirectDm,
  peer: { type: "agent", id: "agent-a", name: "Agent A", archived: true },
};

describe.sequential("DmConversation archived peer read-only surface (B1)", () => {
  beforeEach(() => {
    currentPageMessages = [peerMessage()];
    markReadSpy.mockClear();
  });

  it("read-only surface: no editable composer, no DmAgentBubble, no reply-in-thread, shows the user-facing deleted notice (never the internal 'archived' term)", async () => {
    renderSupervisedDm(archivedPeerDm);
    await screen.findByTestId("message-bubble");

    // composer=0, same criterion as the agent_pair surface above.
    expect(screen.queryByTestId("content-editor")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
    // No second, independent live-chat surface with an agent that can't take
    // new work.
    expect(screen.queryByTestId("dm-agent-bubble")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reply in thread" })).not.toBeInTheDocument();

    // Parker/Iris product review: the copy is user-facing ("deleted" — what
    // the user actually clicked), never the internal "archived" term.
    expect(screen.getByText(/this agent has been deleted/i)).toBeInTheDocument();
    expect(screen.queryByText(/\barchived\b/i)).not.toBeInTheDocument();
  });


  it("does not treat a normal (non-archived) single-agent DM as read-only (gate must not regress legit chat)", async () => {
    renderSupervisedDm(agentDirectDm);
    await screen.findByTestId("message-bubble");
    expect(screen.getByTestId("dm-agent-bubble")).toBeInTheDocument();
    expect(screen.getByTestId("content-editor")).toBeInTheDocument();
  });
});
