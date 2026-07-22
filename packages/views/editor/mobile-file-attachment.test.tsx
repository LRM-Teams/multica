import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { MobileFileAttachment } from "./mobile-file-attachment";
import { resolveMobilePreviewMode } from "./mobile-preview-mode";

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
        ? raw.replace("{{filename}}", "shot.png")
        : raw;
    },
  }),
}));

describe("resolveMobilePreviewMode (LRM-219)", () => {
  it("only images preview; html/pdf narrow to none", () => {
    expect(resolveMobilePreviewMode("image", "image/png", "a.png")).toBe(
      "image",
    );
    expect(resolveMobilePreviewMode(undefined, "image/jpeg", "a.jpg")).toBe(
      "image",
    );
    expect(resolveMobilePreviewMode("html", "text/html", "a.html")).toBe(
      "none",
    );
    expect(resolveMobilePreviewMode("pdf", "application/pdf", "a.pdf")).toBe(
      "none",
    );
    expect(resolveMobilePreviewMode(undefined, "application/zip", "a.zip")).toBe(
      "none",
    );
  });
});

describe("MobileFileAttachment (LRM-219 image-only preview)", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });
  afterEach(() => cleanup());

  it("image: stream thumbnail, opens fullscreen big image", () => {
    const onDownload = vi.fn();
    render(
      <MobileFileAttachment
        filename="shot.png"
        contentType="image/png"
        sizeBytes={1200}
        previewMode="image"
        previewUrl="https://cdn.example/shot.png"
        onDownload={onDownload}
      />,
    );
    expect(screen.getByTestId("mobile-file-stream-thumb")).toBeTruthy();
    expect(screen.queryByText("shot.png")).toBeNull(); // filename not on compact card
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-detail")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-image")).toBeTruthy();
    expect(screen.queryByTestId("mobile-file-preview-empty")).toBeNull();
    expect(screen.getAllByTestId("mobile-file-detail-download")).toHaveLength(1);
    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalled();
  });

  it("html: compact card, no content preview (empty pane)", () => {
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html"
        sizeBytes={606}
        previewMode="html"
        attachmentId="att-1"
        previewUrl="https://cdn.example/a.html"
        onDownload={() => {}}
      />,
    );
    expect(screen.getByTestId("mobile-file-entry").getAttribute("data-preview")).toBe(
      "compact-card",
    );
    expect(screen.getByText("lrm201-tall-preview.html")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-empty")).toBeTruthy();
    expect(screen.queryByTestId("mobile-file-preview-html-body")).toBeNull();
    expect(screen.queryByTestId("mobile-file-preview-image")).toBeNull();
  });

  it("zip: compact card → fullscreen filename + Download only", () => {
    render(
      <MobileFileAttachment
        filename="archive.zip"
        contentType="application/zip"
        previewMode="none"
        onDownload={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-empty")).toBeTruthy();
    expect(screen.queryByText("Can't preview")).toBeNull();
    expect(screen.getByTestId("mobile-file-detail-download")).toBeTruthy();
    const detail = screen.getByTestId("mobile-file-detail");
    expect(detail.textContent).toContain("archive.zip");
  });

  it("does not open detail when not openable", () => {
    render(
      <MobileFileAttachment
        filename="gone.png"
        contentType="image/png"
        openable={false}
        previewUrl="https://cdn.example/gone.png"
        onDownload={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });
});
