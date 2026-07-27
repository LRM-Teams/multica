// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelFilesPanel } from "./channel-files-panel";

const TEST_RESOURCES = {
  en: { common: enCommon, channels: enChannels },
};

const listChannelAttachments = vi.fn();
const tryOpen = vi.fn(() => true);
const download = vi.fn(async () => {});

vi.mock("@multica/core/api", () => ({
  api: {
    listChannelAttachments: (...args: unknown[]) => listChannelAttachments(...args),
  },
}));

vi.mock("@multica/core/channels", async () => {
  const { queryOptions } = await import("@tanstack/react-query");
  return {
    channelAttachmentsOptions: (channelId: string) =>
      queryOptions({
        queryKey: ["channel-attachments", channelId],
        queryFn: () => listChannelAttachments(channelId),
      }),
  };
});

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getMemberName: (id: string) => (id === "user-1" ? "Frank" : "Unknown"),
    getAgentName: () => "Agent",
  }),
}));

vi.mock("../../editor", () => ({
  useAttachmentPreview: () => ({
    tryOpen,
    open: vi.fn(),
    modal: null,
  }),
  useDownloadAttachment: () => download,
}));

vi.mock("../../i18n/use-message-time", () => ({
  useMessageTime: () => ({
    format: () => "10:15",
    full: () => "full",
    clock: () => "10:15",
  }),
}));

function renderPanel() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <ChannelFilesPanel channelId="ch-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ChannelFilesPanel channel attachments (LRM-607)", () => {
  beforeEach(() => {
    listChannelAttachments.mockReset();
    tryOpen.mockReset();
    tryOpen.mockReturnValue(true);
    download.mockReset();
  });

  it("lists channel uploads with open/download (no project-files tree)", async () => {
    listChannelAttachments.mockResolvedValue([
      {
        id: "att-1",
        workspace_id: "ws",
        issue_id: null,
        comment_id: null,
        chat_session_id: null,
        chat_message_id: "m1",
        uploader_type: "member",
        uploader_id: "user-1",
        filename: "design.pdf",
        url: "/u",
        download_url: "/api/attachments/att-1/download",
        markdown_url: "/api/attachments/att-1/download",
        content_type: "application/pdf",
        size_bytes: 1_200_000,
        created_at: "2026-07-26T02:00:00Z",
      },
    ]);

    renderPanel();

    expect(await screen.findByText("design.pdf")).toBeInTheDocument();
    expect(screen.getByText(/Frank/)).toBeInTheDocument();
    expect(screen.queryByText("MEMORY.md")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(tryOpen).toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Download" }));
    await waitFor(() => expect(download).toHaveBeenCalledWith("att-1"));
  });

  it("shows empty state when the channel has no uploads", async () => {
    listChannelAttachments.mockResolvedValue([]);
    renderPanel();
    expect(await screen.findByTestId("channel-files-empty")).toHaveTextContent(
      "No uploads yet",
    );
  });

  it("shows error with retry (no silent project-files fallback)", async () => {
    listChannelAttachments.mockRejectedValue(new Error("boom"));
    renderPanel();
    expect(await screen.findByTestId("channel-files-error")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
