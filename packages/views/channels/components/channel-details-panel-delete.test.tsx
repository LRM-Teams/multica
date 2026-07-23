import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Channel } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelDetailsPanel } from "./channel-details-panel";

vi.mock("@multica/core/projects/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/projects/queries")>()),
  projectListOptions: () => ({
    queryKey: ["projects", "ws-1", "list"],
    queryFn: async () => ({ projects: [], total: 0 }),
    select: (data: { projects: unknown[] }) => data.projects,
  }),
}));

const testChannel: Channel = {
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

function renderPanel(
  overrides: Partial<ComponentProps<typeof ChannelDetailsPanel>> = {},
) {
  const onArchive = vi.fn();
  const onDelete = vi.fn();
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
          projectBound={false}
          onChangeProject={() => {}}
          projectEditable={false}
          canManage
          isArchived={false}
          onMuteToggle={() => {}}
          onShare={() => {}}
          onArchive={onArchive}
          onDelete={onDelete}
          onRename={() => {}}
          onUpdateLarkChatId={() => {}}
          membersBody={null}
          initialTab="settings"
          onClose={() => {}}
          {...overrides}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { onArchive, onDelete };
}

describe("ChannelDetailsPanel danger zone (LRM-239)", () => {
  it("shows archive and red delete entries when onDelete is provided", () => {
    renderPanel();
    expect(screen.getByRole("button", { name: /Archive this channel/i })).toBeInTheDocument();
    const deleteBtn = screen.getByRole("button", { name: /Delete this channel/i });
    expect(deleteBtn).toBeInTheDocument();
    expect(deleteBtn.querySelector(".text-destructive")).toBeTruthy();
  });

  it("hides delete when onDelete is omitted (member / creator-member)", () => {
    renderPanel({ onDelete: undefined, canManage: true });
    expect(screen.getByText("Archive this channel")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Delete this channel/i })).not.toBeInTheDocument();
  });

  it("keeps delete available on archived channels when onDelete is set", async () => {
    const user = userEvent.setup();
    const { onDelete, onArchive } = renderPanel({
      canManage: true,
      isArchived: true,
    });
    // Archive is not clickable when already archived.
    expect(screen.queryByRole("button", { name: /Archive this channel/i })).not.toBeInTheDocument();
    expect(screen.getByText(/already archived/i)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Delete this channel/i }));
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onArchive).not.toHaveBeenCalled();
  });

  it("invokes onArchive from the archive entry", async () => {
    const user = userEvent.setup();
    const { onArchive } = renderPanel();
    await user.click(screen.getByRole("button", { name: /Archive this channel/i }));
    expect(onArchive).toHaveBeenCalledTimes(1);
  });
});
