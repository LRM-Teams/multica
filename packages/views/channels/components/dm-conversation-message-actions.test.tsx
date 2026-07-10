import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
// lived in the DM path: `dm-conversation` rendered the shared ChannelMessageList
// (which forwards onEditMessage/onDeleteMessage into the bubble) but never
// supplied those callbacks, so a user's OWN DM messages showed no edit/delete.
// This test renders the REAL DmConversation with the REAL ChannelMessageList and
// bubble — the affordances only appear if the DM parent actually wires the
// handlers (it does NOT pass onEdit/onDelete to the bubble directly). It also
// proves an edit is a PATCH (editChannelMessage), never a re-send (H5), and a
// delete is a soft-delete that renders a tombstone.

// Spy on the api client the real edit/delete/send mutation hooks call, so we can
// assert edit == PATCH and never a send. A module-level `deleted` flag lets the
// message page query re-resolve the row as soft-deleted after a delete, so the
// invalidation-driven refetch renders the tombstone.
const apiMock = vi.hoisted(() => {
  const state = { deleted: false };
  const editChannelMessage = vi.fn();
  const deleteChannelMessage = vi.fn(async () => {
    state.deleted = true;
    return undefined;
  });
  const sendChannelMessage = vi.fn();
  const known: Record<string, unknown> = {
    editChannelMessage,
    deleteChannelMessage,
    sendChannelMessage,
  };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy, state, editChannelMessage, deleteChannelMessage, sendChannelMessage };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

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
        firstItemIndex = 0,
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
      const localTarget = Math.max(0, (initialTopMostItemIndex ?? firstItemIndex) - firstItemIndex);
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

// Keep the real mutation hooks (so edit/delete really route through the api
// client), but stub the query options to fixtures so the pane resolves without
// any network. The message page re-resolves the own row as soft-deleted once a
// delete has been issued, so the invalidation-driven refetch shows the tombstone.
vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  return {
    ...actual,
    activeChannelTasksOptions: () => ({ queryKey: ["channel-tasks"], queryFn: async () => [] }),
    channelMessageThreadOptions: () => ({ queryKey: ["channel-thread"], queryFn: async () => ({ messages: [] }) }),
    channelMessagesPageOptions: () => ({
      queryKey: ["dm-messages", apiMock.state.deleted],
      queryFn: async () => ({
        messages: currentPageMessages.map((m) =>
          apiMock.state.deleted && m.author_id === "user-1"
            ? { ...m, deleted_at: "2026-06-17T09:30:00Z" }
            : m,
        ),
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
  useActorName: () => ({ getActorAvatarUrl: () => null, getActorName: () => null }),
}));
vi.mock("@multica/core/agents", () => ({ useAgentPresenceDetail: () => "loading" }));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useCurrentWorkspace: () => ({ id: "ws-1" }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));
vi.mock("../../issues/components/comment-card", () => ({ AttachmentList: () => null }));
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

describe.sequential("DmConversation message edit / delete wiring (#241 B3)", () => {
  beforeEach(() => {
    apiMock.state.deleted = false;
    currentPageMessages = [ownMessage()];
    apiMock.editChannelMessage.mockReset().mockResolvedValue({
      ...ownMessage(),
      content: "Corrected",
      edited_at: "2026-06-17T09:20:00Z",
    });
    apiMock.deleteChannelMessage.mockClear();
    apiMock.sendChannelMessage.mockReset().mockResolvedValue(ownMessage());
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden in
  // the bubble (canEdit=false) until rebuilt on the unified composer (#258).
  // Delete stays, so a viewer's own DM message surfaces Delete but never Edit.
  it("renders delete but never edit on the viewer's OWN DM message (real wiring)", async () => {
    renderDm();
    await screen.findByTestId("message-bubble");
    // findByRole already asserts the Delete button is present at find-time — do
    // NOT chain .toBeInTheDocument() on the awaited element. Under a loaded CI run
    // the list can transiently re-render between the await resolving and the
    // assertion, detaching that element reference → jest-dom reports "element
    // could not be found in the document" (green locally, red only on overloaded
    // CI). Re-query with waitFor so we assert against the currently-mounted node.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
    });
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
  it.skip("routes an edit through editChannelMessage (PATCH) and never a send (H5)", async () => {
    const user = userEvent.setup();
    renderDm();
    await screen.findByTestId("message-bubble");

    await user.click(await screen.findByRole("button", { name: "Edit" }));
    const editor = await screen.findByRole("textbox", { name: "Edit" });
    await user.clear(editor);
    await user.type(editor, "Corrected");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMock.editChannelMessage).toHaveBeenCalledWith("dm-chan-1", "m-1", "Corrected", undefined),
    );
    expect(apiMock.sendChannelMessage).not.toHaveBeenCalled();
  });

  it("routes a delete through deleteChannelMessage (soft-delete) and renders a tombstone", async () => {
    const user = userEvent.setup();
    renderDm();
    await screen.findByTestId("message-bubble");

    await user.click(await screen.findByRole("button", { name: "Delete" }));

    await waitFor(() => expect(apiMock.deleteChannelMessage).toHaveBeenCalledWith("dm-chan-1", "m-1"));
    expect(apiMock.sendChannelMessage).not.toHaveBeenCalled();
    expect(await screen.findByTestId("message-tombstone")).toBeInTheDocument();
  });
});
