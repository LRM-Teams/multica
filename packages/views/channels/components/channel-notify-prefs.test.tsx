import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Channel } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import {
  ChannelNotifyPrefsDialog,
  ChannelNotifyPrefsOptions,
} from "./channel-notify-prefs";
import { resolveChannelNotifyLevel } from "./channel-notify-level";
import { ChannelDetailsPanel } from "./channel-details-panel";

vi.mock("@multica/core/api", () => ({
  api: {
    uploadFile: vi.fn(),
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

function renderWithI18n(node: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>{node}</QueryClientProvider>
    </I18nProvider>,
  );
}

function renderPanel(
  overrides: Partial<ComponentProps<typeof ChannelDetailsPanel>> = {},
) {
  const props = {
    onClose: vi.fn(),
    onOpenNotificationPrefs: vi.fn(),
    onSelectNotifyLevel: vi.fn(),
    onOpenGlobalNotifySettings: vi.fn(),
  };
  renderWithI18n(
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
      onRename={() => {}}
      onUpdateLarkChatId={() => {}}
      membersBody={<div>Members body</div>}
      initialTab="about"
      notifyPrefLabel="Default (follow global)"
      notifyLevel="default"
      onClose={props.onClose}
      onOpenNotificationPrefs={props.onOpenNotificationPrefs}
      onSelectNotifyLevel={props.onSelectNotifyLevel}
      onOpenGlobalNotifySettings={props.onOpenGlobalNotifySettings}
      {...overrides}
    />,
  );
  return props;
}

describe("resolveChannelNotifyLevel (LRM-748)", () => {
  it("prefers the API notify_level when present", () => {
    expect(
      resolveChannelNotifyLevel({ ...testChannel, notify_level: "all", muted_at: "2026-01-01T00:00:00Z" }),
    ).toBe("all");
  });

  it("maps legacy muted_at to mentions per the LRM-769 backfill contract", () => {
    expect(
      resolveChannelNotifyLevel({ ...testChannel, muted_at: "2026-01-01T00:00:00Z" }),
    ).toBe("mentions");
    expect(resolveChannelNotifyLevel({ ...testChannel, muted: true })).toBe("mentions");
  });

  it("falls to default for an unmuted channel without notify_level", () => {
    expect(resolveChannelNotifyLevel(testChannel)).toBe("default");
  });
});

describe("ChannelNotifyPrefsOptions (LRM-748)", () => {
  it("renders the four frozen options with the current level selected", () => {
    renderWithI18n(
      <ChannelNotifyPrefsOptions
        level="mentions"
        onSelect={() => {}}
        onOpenGlobalSettings={() => {}}
      />,
    );
    const mentions = screen.getByRole("radio", { name: /^Only @mentions/ });
    expect(mentions.getAttribute("aria-checked")).toBe("true");
    expect(
      screen.getByRole("radio", { name: /^Default \(follow global\)/ }).getAttribute("aria-checked"),
    ).toBe("false");
    expect(screen.getByRole("radio", { name: /^All new messages/ })).toBeTruthy();
    expect(screen.getByRole("radio", { name: /^Muted/ })).toBeTruthy();
  });

  it("calls onSelect with the chosen level and routes the footer link", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const onOpenGlobalSettings = vi.fn();
    renderWithI18n(
      <ChannelNotifyPrefsOptions
        level="default"
        onSelect={onSelect}
        onOpenGlobalSettings={onOpenGlobalSettings}
      />,
    );
    await user.click(screen.getByRole("radio", { name: /^All new messages/ }));
    expect(onSelect).toHaveBeenCalledWith("all");
    await user.click(screen.getByTestId("notify-prefs-global-settings"));
    expect(onOpenGlobalSettings).toHaveBeenCalledTimes(1);
  });
});

describe("ChannelNotifyPrefsDialog (LRM-748)", () => {
  it("shows the title and channel subtitle without a page push", () => {
    renderWithI18n(
      <ChannelNotifyPrefsDialog
        open
        onOpenChange={() => {}}
        channelName="multica-frank"
        level="default"
        onSelect={() => {}}
        onOpenGlobalSettings={() => {}}
      />,
    );
    const dialog = screen.getByTestId("channel-notify-prefs-dialog");
    expect(dialog.textContent).toContain("Notification preference");
    expect(dialog.textContent).toContain("#multica-frank");
  });
});

describe("ChannelDetailsPanel notify-prefs entry (LRM-748)", () => {
  it("desktop (panel variant): row closes the panel and opens the dialog", async () => {
    const user = userEvent.setup();
    const { onClose, onOpenNotificationPrefs, onSelectNotifyLevel } = renderPanel();
    await user.click(screen.getByTestId("channel-details-notify-pref"));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onOpenNotificationPrefs).toHaveBeenCalledTimes(1);
    expect(onSelectNotifyLevel).not.toHaveBeenCalled();
  });

  it("mobile (page variant): row drills into the notify-prefs sub-view and selecting an option calls onSelectNotifyLevel", async () => {
    const user = userEvent.setup();
    const { onOpenNotificationPrefs, onSelectNotifyLevel } = renderPanel({
      variant: "page",
    });
    await user.click(screen.getByTestId("channel-details-notify-pref"));
    expect(onOpenNotificationPrefs).not.toHaveBeenCalled();
    expect(screen.getByTestId("channel-details-notify-prefs-view")).toBeTruthy();

    await user.click(screen.getByRole("radio", { name: /^Muted/ }));
    expect(onSelectNotifyLevel).toHaveBeenCalledWith("muted");
  });

  it("mobile sub-view back returns to details home", async () => {
    const user = userEvent.setup();
    renderPanel({ variant: "page" });
    await user.click(screen.getByTestId("channel-details-notify-pref"));
    expect(screen.getByTestId("channel-details-notify-prefs-view")).toBeTruthy();
    await user.click(screen.getByTestId("channel-details-back"));
    expect(screen.getByTestId("channel-details-home")).toBeTruthy();
  });
});
