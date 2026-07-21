import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const { getAttachmentTextContentMock } = vi.hoisted(() => ({
  getAttachmentTextContentMock: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: { getAttachmentTextContent: getAttachmentTextContentMock },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspaceSlug: () => "acme",
  };
});

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: () => "Frank An",
  }),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      sel: (
        s: Record<string, Record<string, string | Record<string, string>>>,
      ) => string,
    ) => {
      const dict = {
        image: { download: "Download" },
        file_card: { uploading: "Uploading {{filename}}" },
        attachment: {
          open_file: "Open {{filename}}",
          file_detail_title: "File",
          back: "Back",
          open_elsewhere: "Open with another app",
          meta_type: "Type",
          meta_size: "Size",
          meta_sender: "Sender",
          meta_time: "Time",
          preview_chip_html: "Preview · HTML",
          preview_chip_image: "Preview · Image",
          preview_chip_pdf: "Preview · PDF",
          preview_unavailable_chip: "Can't preview",
          preview_unavailable: "Can't preview this file",
          preview_unavailable_hint: "Download it or open with another app.",
          preview_loading: "Loading preview…",
          preview_failed: "Couldn't load preview",
          preview_too_large: "File is too large to preview.",
          preview_unsupported: "This file type can't be previewed.",
          file_type: {
            image: "Image",
            video: "Video",
            audio: "Audio",
            pdf: "PDF",
            word: "Word document",
            excel: "Spreadsheet",
            ppt: "Presentation",
            archive: "Archive",
            code: "Code",
            text: "Text",
            file: "File",
          },
        },
      };
      const raw = sel(dict as never);
      return typeof raw === "string"
        ? raw.replace("{{filename}}", "lrm201-tall-preview.html")
        : raw;
    },
  }),
}));

import { MobileFileAttachment } from "./mobile-file-attachment";

function renderWithQuery(ui: ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("MobileFileAttachment (LRM-216 / LRM-217)", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    getAttachmentTextContentMock.mockReset();
  });
  afterEach(() => cleanup());

  it("renders a compact entry without an iframe", () => {
    renderWithQuery(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html"
        sizeBytes={606}
        attachmentId="att-1"
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    expect(screen.getByTestId("mobile-file-entry")).toBeTruthy();
    expect(screen.getByText("lrm201-tall-preview.html")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("opens fullscreen HTML preview shell with download/open", async () => {
    getAttachmentTextContentMock.mockResolvedValue({
      text: "<p>chart</p>",
      originalContentType: "text/html",
    });
    const onDownload = vi.fn();
    const onOpen = vi.fn();
    renderWithQuery(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html; charset=utf-8"
        sizeBytes={606}
        createdAt="2026-07-21T14:45:23Z"
        uploaderName="Frank An"
        attachmentId="att-1"
        onDownload={onDownload}
        onOpen={onOpen}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-detail")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview")).toHaveAttribute(
      "data-preview-kind",
      "html",
    );
    expect(screen.getByText("Frank An")).toBeTruthy();
    await waitFor(() => {
      expect(
        screen.getByTestId("mobile-file-preview").querySelector("iframe"),
      ).toBeTruthy();
    });

    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("mobile-file-detail-open"));
    expect(onOpen).toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("mobile-file-detail-back"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });

  it("shows image fit preview in the same shell", () => {
    renderWithQuery(
      <MobileFileAttachment
        filename="shot.png"
        contentType="image/png"
        sizeBytes={2048}
        mediaUrl="https://cdn.example/shot.png"
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview")).toHaveAttribute(
      "data-preview-kind",
      "image",
    );
    expect(screen.getByTestId("mobile-file-preview-image")).toHaveAttribute(
      "src",
      "https://cdn.example/shot.png",
    );
  });

  it("shows PDF iframe in the same shell", () => {
    renderWithQuery(
      <MobileFileAttachment
        filename="manual.pdf"
        contentType="application/pdf"
        attachmentId="att-pdf"
        mediaUrl="https://cdn.example/manual.pdf"
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview")).toHaveAttribute(
      "data-preview-kind",
      "pdf",
    );
    const frame = screen.getByTestId("mobile-file-preview-pdf");
    expect(frame.getAttribute("src")).toContain(
      "/api/attachments/att-pdf/download",
    );
  });

  it("shows unavailable placeholder for zip and keeps download/open", () => {
    const onDownload = vi.fn();
    renderWithQuery(
      <MobileFileAttachment
        filename="logs.zip"
        contentType="application/zip"
        sizeBytes={4096}
        mediaUrl="https://cdn.example/logs.zip"
        onDownload={onDownload}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview")).toHaveAttribute(
      "data-preview-kind",
      "none",
    );
    expect(screen.getByTestId("mobile-file-preview-unavailable")).toBeTruthy();
    expect(screen.getByText("Can't preview this file")).toBeTruthy();
    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalled();
  });

  it("does not open detail when not openable", () => {
    renderWithQuery(
      <MobileFileAttachment
        filename="gone.html"
        contentType="text/html"
        openable={false}
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });
});
