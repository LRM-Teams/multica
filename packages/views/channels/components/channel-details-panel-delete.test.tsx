// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Channel } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelDetailsPanel } from "./channel-details-panel";

const TEST_RESOURCES = {
  en: { common: enCommon, channels: enChannels },
};

const channel: Channel = {
  id: "chan-1",
  workspace_id: "ws-1",
  name: "multica-frank",
  kind: "group",
  description: null,
  lark_chat_id: null,
  created_by: "user-1",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

vi.mock("@multica/core/projects/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/projects/queries")>()),
  projectListOptions: () => ({
    queryKey: ["projects", "ws-1", "list"],
    queryFn: async () => [],
  }),
}));

function renderSettings(opts: {
  canManage?: boolean;
  canDelete?: boolean;
  isArchived?: boolean;
  onArchive?: () => void;
  onDelete?: () => void;
}) {
  const onArchive = opts.onArchive ?? vi.fn();
  const onDelete = opts.onDelete ?? vi.fn();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ChannelDetailsPanel
          channel={channel}
          members={[]}
          wsId="ws-1"
          projectId={null}
          projectBound={false}
          onChangeProject={() => {}}
          projectEditable={false}
          canManage={opts.canManage ?? true}
          canDelete={opts.canDelete ?? false}
          isArchived={opts.isArchived ?? false}
          onMuteToggle={() => {}}
          onShare={() => {}}
          onArchive={onArchive}
          onDelete={opts.canDelete === false ? undefined : onDelete}
          onRename={() => {}}
          onUpdateLarkChatId={() => {}}
          membersBody={null}
          initialTab="settings"
          onClose={() => {}}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { onArchive, onDelete };
}

describe("ChannelDetailsPanel permanent delete (LRM-237)", () => {
  it("shows Slack-style archive + delete rows for owner/admin", () => {
    const { onArchive, onDelete } = renderSettings({ canManage: true, canDelete: true });

    expect(screen.getByText("Archive this channel")).toBeInTheDocument();
    expect(
      screen.getByText(/Hide it from the sidebar/i),
    ).toBeInTheDocument();
    expect(screen.getByText("Delete this channel")).toBeInTheDocument();
    expect(
      screen.getByText(/Permanently remove the channel/i),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByText("Archive this channel"));
    expect(onArchive).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByText("Delete this channel"));
    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it("hides delete for non-owner/admin while keeping archive when allowed", () => {
    renderSettings({ canManage: true, canDelete: false });

    expect(screen.getByText("Archive this channel")).toBeInTheDocument();
    expect(screen.queryByText("Delete this channel")).not.toBeInTheDocument();
  });

  it("keeps delete for archived channels (Slack: still deletable after archive)", () => {
    const { onDelete } = renderSettings({
      canManage: true,
      canDelete: true,
      isArchived: true,
    });

    expect(screen.getByText(/already archived/i)).toBeInTheDocument();
    expect(screen.getByText("Delete this channel")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Delete this channel"));
    expect(onDelete).toHaveBeenCalledTimes(1);
  });
});
