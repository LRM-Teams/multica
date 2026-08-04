// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ComponentType, ReactNode } from "react";
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

// LRM-714: the panel is virtualized; jsdom has no layout, so render every
// row through the same List/Item components the panel hands to Virtuoso.
vi.mock("react-virtuoso", () => {
  const MockVirtuoso = ({
    components = {},
    data = [],
    itemContent,
  }: {
    components?: {
      List?: ComponentType<{ children?: ReactNode }>;
      Item?: ComponentType<{ children?: ReactNode }>;
    };
    data?: { id: string }[];
    itemContent: (index: number, item: { id: string }) => ReactNode;
  }) => {
    const List = components.List ?? "div";
    const Item = components.Item ?? "div";
    return (
      <div data-testid="virtuoso-scroller">
        <List>
          {data.map((item, i) => (
            <Item key={item.id}>{itemContent(i, item)}</Item>
          ))}
        </List>
      </div>
    );
  };
  return { Virtuoso: MockVirtuoso };
});

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

// LRM-1264 R3 — panel imports preview/download leaf modules (not the TipTap barrel).
vi.mock("../../editor/attachment-preview-modal", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../../editor/attachment-preview-modal")>();
  return {
    ...actual,
    useAttachmentPreview: () => ({
      tryOpen,
      open: vi.fn(),
      modal: null,
    }),
  };
});
vi.mock("../../editor/use-download-attachment", () => ({
  useDownloadAttachment: () => download,
}));

vi.mock("../../editor/use-download-attachment", () => ({
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

describe("ChannelFilesPanel channel attachments (LRM-607 / LRM-675)", () => {
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

    fireEvent.click(screen.getByRole("button", { name: "Download" }));
    await waitFor(() => expect(download).toHaveBeenCalledWith("att-1"));
  });

  it("image rows render a visible thumbnail (LRM-675)", async () => {
    listChannelAttachments.mockResolvedValue([
      {
        id: "att-img",
        workspace_id: "ws",
        issue_id: null,
        comment_id: null,
        chat_session_id: null,
        chat_message_id: "m2",
        uploader_type: "member",
        uploader_id: "user-1",
        filename: "shot.png",
        url: "/u",
        download_url: "/api/attachments/att-img/download",
        markdown_url: "/api/attachments/att-img/download",
        content_type: "image/png",
        size_bytes: 200_000,
        created_at: "2026-07-26T02:00:00Z",
      },
    ]);

    renderPanel();

    const thumb = await screen.findByTestId("channel-file-thumb");
    expect(thumb).toHaveAttribute("src", "/api/attachments/att-img/download");
    // image is previewable → Open stays available and the row click previews
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(tryOpen).toHaveBeenCalled();
  });

  it("clicking the row opens the preview modal", async () => {
    listChannelAttachments.mockResolvedValue([
      {
        id: "att-md",
        workspace_id: "ws",
        issue_id: null,
        comment_id: null,
        chat_session_id: null,
        chat_message_id: "m3",
        uploader_type: "member",
        uploader_id: "user-1",
        filename: "notes.md",
        url: "/u",
        download_url: "/api/attachments/att-md/download",
        markdown_url: "/api/attachments/att-md/download",
        content_type: "text/markdown",
        size_bytes: 3_000,
        created_at: "2026-07-26T02:00:00Z",
      },
    ]);

    renderPanel();

    const row = await screen.findByTestId("channel-file-row");
    fireEvent.click(row.firstElementChild as HTMLElement);
    expect(tryOpen).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "full" }),
    );
  });

  it("non-previewable binaries drop the Open action and download on click", async () => {
    tryOpen.mockReturnValue(false);
    listChannelAttachments.mockResolvedValue([
      {
        id: "att-zip",
        workspace_id: "ws",
        issue_id: null,
        comment_id: null,
        chat_session_id: null,
        chat_message_id: "m4",
        uploader_type: "member",
        uploader_id: "user-1",
        filename: "build-log.zip",
        url: "/u",
        download_url: "/api/attachments/att-zip/download",
        markdown_url: "/api/attachments/att-zip/download",
        content_type: "application/zip",
        size_bytes: 4_600_000,
        created_at: "2026-07-26T02:00:00Z",
      },
    ]);

    renderPanel();

    await screen.findByText("build-log.zip");
    expect(screen.queryByRole("button", { name: "Open" })).toBeNull();
    const row = screen.getByTestId("channel-file-row");
    fireEvent.click(row.firstElementChild as HTMLElement);
    await waitFor(() => expect(download).toHaveBeenCalledWith("att-zip"));
  });

  it("renders through the virtualized scroller, newest first (LRM-714)", async () => {
    const make = (id: string, created_at: string) => ({
      id,
      workspace_id: "ws",
      issue_id: null,
      comment_id: null,
      chat_session_id: null,
      chat_message_id: `m-${id}`,
      uploader_type: "member",
      uploader_id: "user-1",
      filename: `${id}.txt`,
      url: "/u",
      download_url: `/api/attachments/${id}/download`,
      markdown_url: `/api/attachments/${id}/download`,
      content_type: "text/plain",
      size_bytes: 100,
      created_at,
    });
    listChannelAttachments.mockResolvedValue([
      make("older", "2026-07-20T02:00:00Z"),
      make("newest", "2026-07-28T02:00:00Z"),
      make("middle", "2026-07-24T02:00:00Z"),
    ]);

    renderPanel();

    expect(await screen.findByTestId("virtuoso-scroller")).toBeInTheDocument();
    const rows = await screen.findAllByTestId("channel-file-row");
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("newest.txt"),
      expect.stringContaining("middle.txt"),
      expect.stringContaining("older.txt"),
    ]);
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
