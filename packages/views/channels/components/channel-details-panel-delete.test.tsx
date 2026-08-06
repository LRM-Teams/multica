import { type ComponentProps } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Channel } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelDetailsPanel } from "./channel-details-panel";

// #808 — the overview hosts GroupManagerHint, which reads the viewer from the
// auth store and the roster from the members query. Neither is what this file
// tests, so stub the viewer; the hint self-gates to owner + zero-manager and
// stays hidden here.
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
          members={[
            {
              member_type: "user",
              member_id: "u-1",
              name: "alice",
              display_name: "Alice",
              avatar_url: null,
            },
            {
              member_type: "agent",
              member_id: "a-1",
              name: "bot",
              display_name: "Bot",
              avatar_url: null,
            },
          ]}
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
          onArchive={onArchive}
          onDelete={onDelete}
          onRename={() => {}}
          onUpdateLarkChatId={() => {}}
          membersBody={<div>Members body</div>}
          initialTab="about"
          onClose={() => {}}
          notifyPrefLabel="All"
          {...overrides}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { onArchive, onDelete };
}

describe("ChannelDetailsPanel danger zone (LRM-239 / LRM-494)", () => {
  it("shows Delete on the home overview, archive under Settings", async () => {
    const user = userEvent.setup();
    renderPanel();
    expect(screen.getByTestId("channel-details-home")).toBeTruthy();
    expect(screen.getByTestId("channel-details-delete")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Archive this channel/i })).not.toBeInTheDocument();

    await user.click(screen.getByTestId("channel-details-settings"));
    expect(screen.getByRole("button", { name: /Archive this channel/i })).toBeInTheDocument();
  });

  it("hides delete when onDelete is omitted (member / creator-member)", () => {
    renderPanel({ onDelete: undefined });
    expect(screen.queryByTestId("channel-details-delete")).not.toBeInTheDocument();
  });

  it("invokes onDelete from the danger card", async () => {
    const user = userEvent.setup();
    const { onDelete } = renderPanel();
    await user.click(screen.getByTestId("channel-details-delete"));
    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it("invokes onArchive from Settings", async () => {
    const user = userEvent.setup();
    const { onArchive } = renderPanel();
    await user.click(screen.getByTestId("channel-details-settings"));
    await user.click(screen.getByRole("button", { name: /Archive this channel/i }));
    expect(onArchive).toHaveBeenCalledTimes(1);
  });

  it("keeps archive disabled copy on archived channels in Settings", async () => {
    const user = userEvent.setup();
    renderPanel({
      access: {
        canManage: true,
        isArchived: true,
        hideSettingsTab: false,
        projectBound: false,
        projectEditable: false,
      },
    });
    await user.click(screen.getByTestId("channel-details-settings"));
    expect(screen.queryByRole("button", { name: /Archive this channel/i })).not.toBeInTheDocument();
    expect(screen.getByText(/already archived/i)).toBeInTheDocument();
  });
});

describe("ChannelDetailsPanel Slack overview (LRM-494)", () => {
  it("renders hero meta, member stack, sections, and Done on page variant", () => {
    renderPanel({ variant: "page" });
    expect(screen.getByTestId("channel-details-hero-avatar")).toBeTruthy();
    expect(screen.getByText(/1 members · 1 agents/i)).toBeTruthy();
    expect(screen.getByText("R&D channel")).toBeTruthy();
    expect(screen.getByTestId("channel-details-member-stack")).toBeTruthy();
    expect(screen.getByTestId("channel-details-notify-pref")).toBeTruthy();
    expect(screen.getByText("All")).toBeTruthy();
    expect(screen.getByTestId("channel-details-mute-switch")).toBeTruthy();
    expect(screen.getByTestId("channel-details-done")).toHaveTextContent("Done");
  });

  // #821 — Overview Invite removed (adding people lives on the Members
  // sub-page), so the old "disables invite" row test is gone with it.
});

describe("ChannelDetailsPanel — group leave affordance (danger zone)", () => {
  async function openDanger(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByTestId("channel-details-settings"));
  }

  it("owner: disabled Leave with a transfer-first reason, not clickable", async () => {
    const user = userEvent.setup();
    renderPanel({ groupLeave: { disabledReason: "Transfer ownership first." } });
    await openDanger(user);
    const leave = screen.getByTestId("channel-details-leave");
    expect(leave).toHaveTextContent("Leave group");
    expect(leave).toHaveTextContent("Transfer ownership first.");
    expect(within(leave).queryByRole("button")).toBeNull();
  });

  it("when onLeave is provided: clickable destructive Leave invokes it", async () => {
    const user = userEvent.setup();
    const onLeave = vi.fn();
    renderPanel({ groupLeave: { onLeave } });
    await openDanger(user);
    await user.click(
      within(screen.getByTestId("channel-details-leave")).getByRole("button"),
    );
    expect(onLeave).toHaveBeenCalledTimes(1);
  });

  it("no groupLeave (DM / system / non-group): Leave row not rendered", async () => {
    const user = userEvent.setup();
    renderPanel();
    await openDanger(user);
    expect(screen.queryByTestId("channel-details-leave")).toBeNull();
  });
});
