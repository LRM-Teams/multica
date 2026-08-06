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
          preview_unavailable: "Can't preview this file",
          preview_unavailable_hint:
            "Download it, then open with your browser or another app.",
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

vi.mock("./html-preview-body", () => ({
  HtmlPreviewBody: ({
    source,
    errorTestId,
  }: {
    source: { kind: string; attachmentId?: string };
    errorTestId?: string;
  }) => (
    <div
      data-testid="mobile-file-preview-html-iframe"
      data-attachment-id={
        source.kind === "attachment" ? source.attachmentId : undefined
      }
      data-error-testid={errorTestId}
    >
      html-preview
    </div>
  ),
}));

describe("resolveMobilePreviewMode (LRM-230)", () => {
  it("images and html preview; pdf and others stay none", () => {
    expect(resolveMobilePreviewMode("image", "image/png", "a.png")).toBe(
      "image",
    );
    expect(resolveMobilePreviewMode(undefined, "image/jpeg", "a.jpg")).toBe(
      "image",
    );
    expect(resolveMobilePreviewMode("html", "text/html", "a.html")).toBe(
      "html",
    );
    expect(
      resolveMobilePreviewMode(undefined, "text/html; charset=utf-8", "a.html"),
    ).toBe("html");
    expect(resolveMobilePreviewMode(undefined, "application/octet-stream", "a.html")).toBe(
      "html",
    );
    expect(resolveMobilePreviewMode("pdf", "application/pdf", "a.pdf")).toBe(
      "none",
    );
    expect(resolveMobilePreviewMode(undefined, "application/zip", "a.zip")).toBe(
      "none",
    );
  });
});

describe("MobileFileAttachment (LRM-230 html + download guidance)", () => {
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
    expect(screen.queryByTestId("mobile-file-preview-unavailable")).toBeNull();
    expect(screen.getAllByTestId("mobile-file-detail-download")).toHaveLength(1);
    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalled();
  });

  it("html: compact card → fullscreen sandboxed HTML body (not empty pane)", () => {
    render(
      <MobileFileAttachment
        filename="design-composer-activity.html"
        contentType="text/html; charset=utf-8"
        sizeBytes={10070}
        previewMode="html"
        attachmentId="att-html-1"
        previewUrl="https://cdn.example/a.html"
        onDownload={() => {}}
      />,
    );
    expect(screen.getByTestId("mobile-file-entry").getAttribute("data-preview")).toBe(
      "compact-card",
    );
    expect(screen.getByText("design-composer-activity.html")).toBeTruthy();
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-preview-html-body")).toBeTruthy();
    expect(screen.getByTestId("mobile-file-preview-html-iframe")).toHaveAttribute(
      "data-attachment-id",
      "att-html-1",
    );
    expect(screen.queryByTestId("mobile-file-preview-unavailable")).toBeNull();
    expect(screen.queryByTestId("mobile-file-preview-empty")).toBeNull();
    expect(screen.queryByTestId("mobile-file-preview-image")).toBeNull();
  });

  // LRM-359 — mobile compact chip mirrors desktop semantic tokens.
  it("compact card uses muted surface + foreground filename (no wash / link hex)", () => {
    render(
      <MobileFileAttachment
        filename="design-agent-profile-polish.html"
        contentType="text/html"
        sizeBytes={2048}
        previewMode="html"
        attachmentId="att-1"
        onDownload={() => {}}
      />,
    );
    const entry = screen.getByTestId("mobile-file-entry");
    expect(entry.className).toContain("bg-muted");
    expect(entry.className).toContain("border-border");
    expect(entry.className).toContain("text-foreground");
    expect(entry.className).not.toMatch(/bg-muted\/\d+|bg-\[#|text-\[#/);

    const filename = screen.getByTestId("mobile-file-filename");
    expect(filename.className).toContain("text-foreground");
    expect(filename.className).not.toMatch(/text-\[#|text-muted|text-gray/);
  });

  it("zip: compact card → fullscreen download guidance (never blank)", () => {
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
    expect(screen.getByText("Can't preview this file")).toBeTruthy();
    expect(
      screen.getByText(
        "Download it, then open with your browser or another app.",
      ),
    ).toBeTruthy();
    expect(screen.queryByTestId("mobile-file-preview-empty")).toBeNull();
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
