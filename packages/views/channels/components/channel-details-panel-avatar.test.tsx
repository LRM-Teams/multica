import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Channel } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelDetailsPanel } from "./channel-details-panel";

// LRM-724 / LRM-860 — channel icon upload from the hero (no About → avatar sub-view).
const uploadFile = vi.fn();
vi.mock("@multica/core/api", () => ({
  api: {
    uploadFile: (...args: unknown[]) => uploadFile(...args),
    getBaseUrl: () => "",
  },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "viewer-1" } }),
}));

vi.mock("@multica/core/projects/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/projects/queries")>()),
  projectListOptions: () => ({
    queryKey: ["projects", "ws-1", "list"],
    queryFn: async () => ({ projects: [], total: 0 }),
    select: (data: { projects: unknown[] }) => data.projects,
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid={`avatar-${actorId}`}>{actorId}</span>
  ),
}));

const testChannel: Channel = {
  id: "chan-1",
  workspace_id: "ws-1",
  name: "multica-frank",
  kind: "group",
  description: "R&D channel",
  lark_chat_id: null,
  avatar_url: null,
  created_by: "user-1",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

function renderPanel(
  overrides: Partial<ComponentProps<typeof ChannelDetailsPanel>> = {},
) {
  const onUpdateAvatar = vi.fn();
  const onRename = vi.fn();
  const onUpdateDescription = vi.fn();
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelDetailsPanel
          channel={testChannel}
          members={[]}
          wsId="ws-1"
          projectId={null}
          onChangeProject={() => {}}
          access={{
            canManage: true,
            isArchived: false,
            hideSettingsTab: false,
            projectBound: false,
            projectEditable: false,
          }}
          onMuteToggle={() => {}}
          onShare={() => {}}
          onArchive={() => {}}
          onRename={onRename}
          onUpdateDescription={onUpdateDescription}
          onUpdateLarkChatId={() => {}}
          onUpdateAvatar={onUpdateAvatar}
          membersBody={<div>Members body</div>}
          initialTab="about"
          onClose={() => {}}
          notifyPrefLabel="All"
          {...overrides}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { onUpdateAvatar, onRename, onUpdateDescription };
}


describe("ChannelDetailsPanel hero inline edit (LRM-860)", () => {
  beforeEach(() => {
    uploadFile.mockReset();
  });

  it("removes the About section rows", () => {
    renderPanel();
    expect(screen.queryByTestId("channel-details-about-name")).toBeNull();
    expect(screen.queryByTestId("channel-details-about-avatar")).toBeNull();
    expect(screen.getByTestId("channel-details-hero-name")).toBeTruthy();
  });



  it("hides the avatar change control without manage permission", () => {
    renderPanel({
      access: {
        canManage: false,
        isArchived: false,
        hideSettingsTab: false,
        projectBound: false,
        projectEditable: false,
      },
      manageDisabledReason: "Only the channel creator or workspace admins can manage this channel.",
    });
    expect(screen.queryByTestId("channel-details-avatar-change")).toBeNull();
    expect(screen.getByTestId("channel-details-hero-name").tagName).toBe("P");
  });

  it("renames from the hero name click path on Enter", async () => {
    const user = userEvent.setup();
    const { onRename } = renderPanel();
    await user.click(screen.getByTestId("channel-details-hero-name"));
    const input = screen.getByTestId("channel-details-hero-name-input");
    await user.clear(input);
    await user.type(input, "new-name{Enter}");
    expect(onRename).toHaveBeenCalledWith("new-name");
  });

  // LRM-1067 — name → description → members/agents (desc not below meta).
  it("orders hero as name → description → meta", () => {
    renderPanel();
    const name = screen.getByTestId("channel-details-hero-name");
    const description = screen.getByTestId("channel-details-hero-description");
    const meta = screen.getByTestId("channel-details-hero-meta");
    expect(name.compareDocumentPosition(description) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(
      description.compareDocumentPosition(meta) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(description).toHaveTextContent("R&D channel");
  });

  it("hides the description placeholder for read-only empty description", () => {
    renderPanel({
      channel: { ...testChannel, description: null },
      access: {
        canManage: false,
        isArchived: false,
        hideSettingsTab: false,
        projectBound: false,
        projectEditable: false,
      },
      onUpdateDescription: undefined,
    });
    expect(screen.queryByTestId("channel-details-hero-description")).toBeNull();
    expect(screen.getByTestId("channel-details-hero-meta")).toBeInTheDocument();
  });
});
