import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { MobileFileAttachment } from "./mobile-file-attachment";

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
          cannot_preview: "Can't preview",
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

vi.mock("./html-preview-body", () => ({
  HtmlPreviewBody: (props: { title: string }) => (
    <div data-testid="mobile-file-preview-html-body">{props.title}</div>
  ),
}));

describe("MobileFileAttachment (LRM-216 Slack freeze)", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });
  afterEach(() => cleanup());

  it("renders a compact entry with type · size and no stream iframe", () => {
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html"
        sizeBytes={606}
        previewMode="html"
        attachmentId="att-1"
        onDownload={() => {}}
      />,
    );
    expect(screen.getByTestId("mobile-file-entry")).toBeTruthy();
    expect(screen.getByText("lrm201-tall-preview.html")).toBeTruthy();
    expect(screen.getByText("HTML · 606 B")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
    expect(screen.queryByTestId("mobile-file-preview-pane")).toBeNull();
  });

  it("opens Slack shell: back · filename · one download + full preview, no meta/footer", () => {
    const onDownload = vi.fn();
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html; charset=utf-8"
        sizeBytes={606}
        createdAt="2026-07-21T14:45:23Z"
        uploaderName="Frank An"
        previewMode="html"
        attachmentId="att-1"
        onDownload={onDownload}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    const detail = screen.getByTestId("mobile-file-detail");
    expect(detail).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-pane")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-html-body")).toBeTruthy();

    // One download only (top bar) — no second green CTA / Open button.
    expect(screen.getAllByTestId("mobile-file-detail-download")).toHaveLength(1);
    expect(screen.queryByTestId("mobile-file-detail-open")).toBeNull();
    expect(screen.queryByText("Frank An")).toBeNull();
    expect(screen.queryByText("Sender")).toBeNull();
    expect(screen.queryByText("Time")).toBeNull();
    // Top bar must not show type · size under the filename.
    expect(detail.textContent).not.toMatch(/606 B/);

    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId("mobile-file-detail-back"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });

  it("shows image preview when mode is image", () => {
    render(
      <MobileFileAttachment
        filename="shot.png"
        contentType="image/png"
        previewMode="image"
        previewUrl="https://example.com/shot.png"
        onDownload={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-image")).toBeTruthy();
    expect(screen.queryByTestId("mobile-file-detail-open")).toBeNull();
  });

  it("shows cannot-preview placeholder with top-bar download only", () => {
    const onDownload = vi.fn();
    render(
      <MobileFileAttachment
        filename="archive.zip"
        contentType="application/zip"
        previewMode="none"
        onDownload={onDownload}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-unavailable")).toBeTruthy();
    expect(screen.getByText("Can't preview")).toBeTruthy();
    expect(screen.queryByTestId("mobile-file-detail-open")).toBeNull();
    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalledTimes(1);
  });

  it("does not open detail when not openable", () => {
    render(
      <MobileFileAttachment
        filename="gone.html"
        contentType="text/html"
        openable={false}
        onDownload={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });
});
