// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { VISIBILITY_LABEL } from "@multica/core/agents";

const { listChannels } = vi.hoisted(() => ({
  listChannels: vi.fn(),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  return {
    ...actual,
    channelsOptions: () => ({
      queryKey: ["channels", "ws-1", "list"],
      queryFn: () => listChannels(),
      enabled: true,
    }),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    listChannels: (...args: unknown[]) => listChannels(...args),
  },
}));

const navigationStub: NavigationAdapter = {
  push: vi.fn(),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/",
  searchParams: new URLSearchParams(),
  getShareableUrl: (path: string) => path,
};

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

import { VisibilityBadge } from "./visibility-badge";
import { VisibilityPicker } from "./inspector/visibility-picker";

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <WorkspaceSlugProvider slug="test-ws">
          <NavigationProvider value={navigationStub}>{ui}</NavigationProvider>
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("VisibilityBadge channel display (LRM-371)", () => {
  beforeEach(() => {
    listChannels.mockResolvedValue([
      {
        id: "ch-1",
        workspace_id: "ws-1",
        name: "dev-group",
        kind: "group",
        description: null,
        lark_chat_id: null,
        created_by: "u1",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
    ]);
  });
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("shows 仅本群 + #channel chip when home is bound", async () => {
    wrap(
      <VisibilityBadge value="channel" homeChannelId="ch-1" />,
    );
    await waitFor(() => {
      expect(screen.getByTestId("visibility-badge-channel")).toBeTruthy();
      expect(screen.getByText(VISIBILITY_LABEL.channel)).toBeTruthy();
      expect(screen.getByText("dev-group")).toBeTruthy();
    });
  });

  it("shows explicit missing home error — no Personal fallback", async () => {
    wrap(<VisibilityBadge value="channel" homeChannelId={null} />);
    await waitFor(() => {
      expect(
        screen.getByText(/Bind a home group|home_channel_id/i),
      ).toBeTruthy();
    });
    expect(screen.queryByText(VISIBILITY_LABEL.private)).toBeNull();
  });
});

describe("VisibilityPicker channel option (LRM-371)", () => {
  beforeEach(() => {
    listChannels.mockResolvedValue([
      {
        id: "ch-1",
        workspace_id: "ws-1",
        name: "dev-group",
        kind: "group",
        description: null,
        lark_chat_id: null,
        created_by: "u1",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
    ]);
  });
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("commits visibility=channel with home_channel_id", async () => {
    const onChange = vi.fn().mockResolvedValue(undefined);
    wrap(
      <VisibilityPicker
        value="workspace"
        homeChannelId={null}
        onChange={onChange}
      />,
    );
    // Wait for channel list so the first group can auto-bind.
    await waitFor(() => expect(listChannels).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: /Visibility/i }));
    await waitFor(() => {
      expect(screen.getByText(VISIBILITY_LABEL.channel)).toBeTruthy();
    });
    // Click the option row (label text lives inside the PickerItem button).
    fireEvent.click(screen.getByText(VISIBILITY_LABEL.channel));
    await waitFor(() => {
      expect(onChange).toHaveBeenCalledWith({
        visibility: "channel",
        home_channel_id: "ch-1",
      });
    });
  });
});
