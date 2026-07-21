// @vitest-environment jsdom
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enIssues from "../../locales/en/issues.json";

const mockMutate = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/issues/mutations", () => ({
  useSetIssueChannel: () => ({ mutate: mockMutate }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

const mockChannels = vi.hoisted(() => ({ current: [] as unknown[] }));
vi.mock("@multica/core/channels", () => ({
  channelsOptions: () => ({
    queryKey: ["channels", "ws-1"],
    queryFn: async () => mockChannels.current,
  }),
}));

import { AssociatedGroupPicker } from "./associated-group-picker";

function renderPicker(channel: React.ComponentProps<typeof AssociatedGroupPicker>["channel"]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <AssociatedGroupPicker issueId="issue-1" channel={channel} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockChannels.current = [
    { id: "g1", name: "Alpha", kind: "group", archived_at: null },
    { id: "g2", name: "Beta", kind: "group", archived_at: null },
    { id: "dm1", name: "Private", kind: "dm", archived_at: null },
    { id: "arch", name: "OldGroup", kind: "group", archived_at: "2026-01-01" },
  ];
});

describe("AssociatedGroupPicker (#629)", () => {
  it("renders the current group name", () => {
    renderPicker({ channel_id: "g1", channel_name: "Alpha", channel_kind: "group" });
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    // Full value is kept in `title` so a narrow column can ellipsize without lying (#629 768px).
    expect(screen.getByRole("button")).toHaveAttribute("title", "Alpha");
  });

  it("renders the empty label when there is no association", () => {
    renderPicker(null);
    expect(screen.getByText("No associated group")).toBeInTheDocument();
    expect(screen.getByRole("button")).toHaveAttribute("title", "No associated group");
  });

  it("lists only visible, unarchived group channels and sets on pick", async () => {
    renderPicker(null);
    fireEvent.click(screen.getByRole("button"));

    expect(await screen.findByText("Alpha")).toBeInTheDocument();
    expect(screen.getByText("Beta")).toBeInTheDocument();
    expect(screen.queryByText("Private")).toBeNull(); // DM excluded
    expect(screen.queryByText("OldGroup")).toBeNull(); // archived excluded

    fireEvent.click(screen.getByText("Alpha"));
    expect(mockMutate).toHaveBeenCalledWith("g1");
  });

  it("requires a confirmation before changing an existing association", async () => {
    renderPicker({ channel_id: "g1", channel_name: "Alpha", channel_kind: "group" });
    fireEvent.click(screen.getByRole("button"));

    fireEvent.click(await screen.findByText("Beta"));
    expect(mockMutate).not.toHaveBeenCalled(); // waits for confirm

    fireEvent.click(screen.getByText("Change"));
    expect(mockMutate).toHaveBeenCalledWith("g2");
  });

  it("clears the association explicitly", async () => {
    renderPicker({ channel_id: "g1", channel_name: "Alpha", channel_kind: "group" });
    fireEvent.click(screen.getByRole("button"));

    fireEvent.click(await screen.findByText("Clear association"));
    expect(mockMutate).toHaveBeenCalledWith(null);
  });
});
