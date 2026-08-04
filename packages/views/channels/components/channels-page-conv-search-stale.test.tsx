import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessageSearchResult } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// LRM-1296 (slice 1) — in-conversation search fires a DEBOUNCED request, and the
// effect cleanup only clears the pending timer. Once a request is already in
// flight, changing the query cannot stop it, so its late response used to land
// last and overwrite the newer results (count + jump target + index reset) while
// the input already showed the newer query. The DM surface already drops stale
// responses (`dm-conversation.tsx` reducer `setSearchResults` compares
// `action.query`); the group-channel surface had no equivalent guard.
//
// These tests drive the real ChannelsPage search UI and force out-of-order
// resolution, which is the only way to catch this class — every existing search
// test resolves in order.

const apiMock = vi.hoisted(() => {
  const searchChannelMessages = vi.fn();
  const known: Record<string, unknown> = { searchChannelMessages };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy, searchChannelMessages };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

const channelFixture = vi.hoisted(() => ({
  current: {
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
  },
}));

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], [channelFixture.current]),
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

vi.mock("@multica/core/workspace/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workspace/queries")>()),
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [] }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
  useContainerNarrowerThan: () => [false, () => {}] as const,
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
  ProjectPickerButton: () => <button type="button">project</button>,
}));

vi.mock("./dm-conversation", () => ({ DmConversation: () => <div /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: () => <div data-testid="message-list" />,
}));
vi.mock("./thread-panel", () => ({ ThreadPanel: () => <div data-testid="thread-panel" /> }));

function hit(id: string): ChannelMessageSearchResult {
  return {
    message_id: id,
    channel_id: "chan-1",
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: id,
    created_at: "2026-06-17T09:15:00Z",
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

async function openSearch() {
  renderPage();
  await screen.findByTestId("message-list");
  fireEvent.click(await screen.findByRole("button", { name: "Search in conversation" }));
  const input = await screen.findByPlaceholderText("Search this channel's messages…");
  return { input };
}

function typeQuery(input: HTMLElement, value: string) {
  fireEvent.change(input, { target: { value } });
}

// The count is rendered via i18n interpolation, which splits it across text
// nodes — read the whole node instead of a string text matcher.
function count() {
  return screen.getByTestId("conv-search-count").textContent;
}

describe("ChannelsPage in-conversation search — stale response guard (LRM-1296)", () => {
  beforeEach(() => {
    apiMock.searchChannelMessages.mockReset();
  });

  it("drops a late response from a superseded query instead of overwriting the newer count", async () => {
    const first = deferred<{ results: ChannelMessageSearchResult[]; total: number }>();
    apiMock.searchChannelMessages.mockImplementation((_channelId: string, query: string) =>
      query === "alpha"
        ? first.promise
        : Promise.resolve({ results: [hit("b-1"), hit("b-2")], total: 2 }),
    );

    const { input } = await openSearch();

    typeQuery(input, "alpha");
    await waitFor(() =>
      expect(apiMock.searchChannelMessages).toHaveBeenCalledWith("chan-1", "alpha"),
    );

    typeQuery(input, "beta");
    await waitFor(() =>
      expect(apiMock.searchChannelMessages).toHaveBeenCalledWith("chan-1", "beta"),
    );
    await waitFor(() => expect(count()).toBe("1 / 2"));

    // The superseded "alpha" request finally resolves — with MORE hits, so a
    // regression is unmistakable in the rendered count.
    await act(async () => {
      first.resolve({ results: [hit("a-1"), hit("a-2"), hit("a-3")], total: 3 });
      await first.promise;
    });

    expect(count()).toBe("1 / 2");
  });

  it("does not let a late response reset the result index the user navigated to", async () => {
    const first = deferred<{ results: ChannelMessageSearchResult[]; total: number }>();
    apiMock.searchChannelMessages.mockImplementation((_channelId: string, query: string) =>
      query === "alpha"
        ? first.promise
        : Promise.resolve({ results: [hit("b-1"), hit("b-2")], total: 2 }),
    );

    const { input } = await openSearch();

    typeQuery(input, "alpha");
    await waitFor(() =>
      expect(apiMock.searchChannelMessages).toHaveBeenCalledWith("chan-1", "alpha"),
    );

    typeQuery(input, "beta");
    await waitFor(() => expect(count()).toBe("1 / 2"));

    fireEvent.click(screen.getByRole("button", { name: "Next result" }));
    await waitFor(() => expect(count()).toBe("2 / 2"));

    await act(async () => {
      first.resolve({ results: [hit("a-1"), hit("a-2"), hit("a-3")], total: 3 });
      await first.promise;
    });

    expect(count()).toBe("2 / 2");
  });
});
