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
          back: "Back",
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

describe("MobileFileAttachment (LRM-216 Slack chrome)", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });
  afterEach(() => cleanup());

  it("renders a compact entry with type · size subtitle and no stream iframe", () => {
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
    expect(screen.getByText(/Code · /)).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("opens Slack chrome: filename + one Download, preview, no meta/footer buttons", () => {
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
    expect(screen.getByTestId("mobile-file-detail")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-pane")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-html-body")).toBeTruthy();

    // One download only (nav)
    expect(screen.getAllByTestId("mobile-file-detail-download")).toHaveLength(1);
    expect(screen.queryByTestId("mobile-file-detail-open")).toBeNull();
    expect(screen.queryByText("Frank An")).toBeNull();
    expect(screen.queryByText(/Sender|发送者/)).toBeNull();

    // Nav shows filename, not type·size subtitle
    const detail = screen.getByTestId("mobile-file-detail");
    expect(detail.textContent).toContain("lrm201-tall-preview.html");
    expect(detail.querySelector(".text-muted-foreground")).toBeNull();

    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("mobile-file-detail-back"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });

  it("shows cannot-preview placeholder for other types", () => {
    render(
      <MobileFileAttachment
        filename="archive.zip"
        contentType="application/zip"
        previewMode="none"
        onDownload={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-unavailable")).toBeTruthy();
    expect(screen.getByText("Can't preview")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-detail-download")).toBeTruthy();
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
