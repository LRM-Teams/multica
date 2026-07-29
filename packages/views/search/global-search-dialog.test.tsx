import { type ReactNode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { GlobalSearchDialog } from "./global-search-dialog";
import { useGlobalSearchStore } from "./global-search-store";
import enCommon from "../locales/en/common.json";
import enSearch from "../locales/en/search.json";

const TEST_RESOURCES = { en: { common: enCommon, search: enSearch } };

const { mockSearchWorkspace, mockPush, mockOpenDM } = vi.hoisted(() => ({
  mockSearchWorkspace: vi.fn(),
  mockPush: vi.fn(),
  mockOpenDM: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: { searchWorkspace: mockSearchWorkspace },
}));

vi.mock("@multica/core", () => ({ useWorkspaceId: () => "ws-test" }));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    channelDetail: (id: string) => `/ws-test/channels/${id}`,
    memberDetail: (id: string) => `/ws-test/members/${id}`,
    agentDetail: (id: string) => `/ws-test/agents/${id}`,
  }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mockPush }),
}));

vi.mock("../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: mockOpenDM, isPending: false }),
}));

vi.mock("../common/actor-avatar", () => ({
  ActorAvatar: ({ actorType, actorId }: { actorType: string; actorId: string }) => (
    <span data-testid="avatar" data-type={actorType} data-id={actorId} />
  ),
}));

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={makeClient()}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        {children}
      </I18nProvider>
    </QueryClientProvider>
  );
}

const renderDialog = () => render(<GlobalSearchDialog />, { wrapper: Wrapper });

beforeEach(() => {
  vi.clearAllMocks();
  mockSearchWorkspace.mockResolvedValue({
    query: "",
    scope: "all",
    counts: { messages: 0, channels: 0, dms: 0, people: 0 },
    messages: [],
    channels: [],
    dms: [],
    people: [],
  });
  useGlobalSearchStore.setState({
    open: false,
    scope: "all",
    recentByWorkspace: {},
  });
});

describe("GlobalSearchDialog", () => {
  it("renders nothing interactive when closed", () => {
    renderDialog();
    expect(screen.queryByPlaceholderText(/Search channels/i)).not.toBeInTheDocument();
  });

  it("shows the input + scope tabs + idle placeholder when opened", async () => {
    useGlobalSearchStore.setState({ open: true });
    renderDialog();
    expect(await screen.findByPlaceholderText(/Search channels/i)).toBeInTheDocument();
    // All five scope tabs are present.
    expect(screen.getByRole("button", { name: "All" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Messages" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Channels" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "DMs" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "People" })).toBeInTheDocument();
  });

  it("renders the empty state when the query matches nothing", async () => {
    mockSearchWorkspace.mockResolvedValue({
      query: "zzz",
      scope: "all",
      counts: { messages: 0, channels: 0, dms: 0, people: 0 },
      messages: [],
      channels: [],
      dms: [],
      people: [],
    });
    useGlobalSearchStore.setState({ open: true });
    const user = userEvent.setup();
    renderDialog();
    const input = await screen.findByPlaceholderText(/Search channels/i);
    await user.type(input, "zzz");
    await waitFor(() => {
      expect(screen.getByText(/No results for/i)).toBeInTheDocument();
    });
  });

  it("⌘K / Ctrl-K toggles the dialog open then closed (LRM-606 reclaim)", () => {
    useGlobalSearchStore.setState({ open: false });
    renderDialog();
    expect(screen.queryByPlaceholderText(/Search channels/i)).not.toBeInTheDocument();
    // ⌘K opens (metaKey); Ctrl-K (ctrlKey) works too.
    fireEvent.keyDown(document, { key: "k", metaKey: true });
    expect(useGlobalSearchStore.getState().open).toBe(true);
    // ⌘K again closes (toggle).
    fireEvent.keyDown(document, { key: "k", ctrlKey: true });
    expect(useGlobalSearchStore.getState().open).toBe(false);
  });
});
