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

describe("MobileFileAttachment (LRM-216 / LRM-217)", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });
  afterEach(() => cleanup());

  it("renders a compact entry without an iframe in the stream", () => {
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html"
        sizeBytes={606}
        previewMode="html"
        attachmentId="att-1"
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    expect(screen.getByTestId("mobile-file-entry")).toBeTruthy();
    expect(screen.getByText("lrm201-tall-preview.html")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
    expect(screen.queryByTestId("mobile-file-preview-pane")).toBeNull();
  });

  it("opens fullscreen with HTML preview pane + download/open", () => {
    const onDownload = vi.fn();
    const onOpen = vi.fn();
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
        onOpen={onOpen}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-detail")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-pane")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-html-body")).toBeTruthy();
    expect(screen.getByText("Frank An")).toBeTruthy();

    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("mobile-file-detail-open"));
    expect(onOpen).toHaveBeenCalled();

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
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-image")).toBeTruthy();
  });

  it("shows cannot-preview placeholder for other types", () => {
    render(
      <MobileFileAttachment
        filename="archive.zip"
        contentType="application/zip"
        previewMode="none"
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-unavailable")).toBeTruthy();
    expect(screen.getByText("Can't preview")).toBeTruthy();
  });

  it("does not open detail when not openable", () => {
    render(
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
