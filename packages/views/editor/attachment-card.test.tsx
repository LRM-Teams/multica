import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      sel: (
        s: Record<string, Record<string, string | Record<string, string>>>,
      ) => string,
    ) =>
      sel({
        image: { download: "Download" },
        attachment: {
          preview: "Preview",
          preview_loading: "Loading preview…",
          remove: "Remove attachment",
          open_file: "Open {{filename}}",
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
        file_card: { uploading: "Uploading {{filename}}" },
      }),
  }),
}));

import { AttachmentCard } from "./attachment-card";

beforeEach(() => vi.clearAllMocks());
afterEach(() => vi.restoreAllMocks());

describe("AttachmentCard — chrome row", () => {
  it("renders chrome only and never an inline iframe (HTML rich preview lives in HtmlAttachmentPreview)", () => {
    render(
      <AttachmentCard
        filename="report.html"
        contentType="text/html"
        attachmentId="att-1"
        href="https://cdn.example/report.html"
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    expect(screen.getByText("report.html")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("html URL-only source is inert body text + download (no primary Open)", () => {
    // Regression: a cross-comment / copy-pasted `!file[report.html](url)`
    // used to surface a dead preview affordance — text kinds need an
    // attachmentId, otherwise the /content proxy rejects. Without one the
    // body is not openable; download stays available.
    render(
      <AttachmentCard
        filename="report.html"
        contentType="text/html"
        href="https://cdn.example/report.html"
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    expect(screen.queryByRole("button", { name: /open/i })).toBeNull();
    // Download stays available — the underlying URL is still reachable.
    expect(screen.getByTitle("Download")).toBeTruthy();
  });

  it("html source with an attachmentId makes the body a primary Open button", () => {
    render(
      <AttachmentCard
        filename="report.html"
        contentType="text/html"
        attachmentId="att-1"
        href="https://cdn.example/report.html"
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    expect(screen.getByRole("button", { name: /open/i })).toBeTruthy();
  });

  it("URL-only pdf source is openable from the body (modal renders pdfs from URL)", () => {
    // Media kinds (pdf/video/audio) ARE URL-previewable because the modal
    // renders them via <iframe src=url>/<video>/<audio>, not via the
    // ID-keyed /content proxy.
    render(
      <AttachmentCard
        filename="manual.pdf"
        contentType="application/pdf"
        href="https://cdn.example/manual.pdf"
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    expect(screen.getByRole("button", { name: /open/i })).toBeTruthy();
  });
});

describe("AttachmentCard — open / download", () => {
  it("invokes onDownload when Download is clicked", () => {
    const onDownload = vi.fn();
    render(
      <AttachmentCard
        filename="manual.pdf"
        contentType="application/pdf"
        attachmentId="att-1"
        href="https://cdn.example/manual.pdf"
        onPreview={() => {}}
        onDownload={onDownload}
      />,
    );
    fireEvent.mouseDown(screen.getByTitle("Download"));
    expect(onDownload).toHaveBeenCalled();
  });

  it("previewable file body is a primary Open button that fires onPreview", () => {
    const onPreview = vi.fn();
    render(
      <AttachmentCard
        filename="manual.pdf"
        contentType="application/pdf"
        href="https://cdn.example/manual.pdf"
        onPreview={onPreview}
        onDownload={() => {}}
      />,
    );
    // The icon+name region is the primary control; its accessible name leads
    // with the localized "Open …" verb (mock returns the raw template).
    const open = screen.getByRole("button", { name: /open/i });
    fireEvent.click(open);
    expect(onPreview).toHaveBeenCalled();
  });

  it("non-previewable file renders an inert body (no Open button), download-only", () => {
    render(
      <AttachmentCard
        filename="logs.zip"
        contentType="application/zip"
        href="https://cdn.example/logs.zip"
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    expect(screen.queryByRole("button", { name: /open/i })).toBeNull();
    expect(screen.queryByTitle("Preview")).toBeNull();
    expect(screen.getByTitle("Download")).toBeTruthy();
    expect(screen.getByText("logs.zip")).toBeTruthy();
  });

  it("hides Eye and Download buttons while uploading", () => {
    render(
      <AttachmentCard
        filename="report.html"
        contentType="text/html"
        attachmentId="att-1"
        href="https://cdn.example/report.html"
        uploading
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    expect(screen.queryByTitle("Preview")).toBeNull();
    expect(screen.queryByTitle("Download")).toBeNull();
    // The mock `t()` returns the i18n template as-is; the production t-fn
    // interpolates {{filename}} → "report.html". Asserting the template
    // proves the uploading branch was selected without depending on the
    // interpolation behavior of the mock.
    expect(screen.getByText("Uploading {{filename}}")).toBeTruthy();
  });
});

describe("AttachmentCard — meta, truncation, keyboard order", () => {
  it("shows the size · type meta line and picks the type glyph by extension", () => {
    render(
      <AttachmentCard
        filename="2026-Q3-report.pdf"
        contentType="application/pdf"
        sizeBytes={1468006}
        href="https://cdn.example/r.pdf"
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    // "{human size} · {type}" — and the type resolves to the PDF bucket.
    expect(screen.getByText("1.4 MB · PDF")).toBeTruthy();
    // react-file-icon renders an <svg> glyph inside the card.
    expect(document.querySelector("svg")).toBeTruthy();
  });

  it.each([
    ["logs-2026.zip", "application/zip", "240 KB · Archive", 245760],
    ["server.go", "", "18 B · Code", 18],
  ])("labels %s as %s", (filename, contentType, expected, sizeBytes) => {
    render(
      <AttachmentCard
        filename={filename}
        contentType={contentType}
        sizeBytes={sizeBytes}
        href={`https://cdn.example/${filename}`}
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    expect(screen.getByText(expected)).toBeTruthy();
  });

  it("truncates a long filename but keeps the full name in a title tooltip", () => {
    const long =
      "this-is-an-intentionally-very-long-attachment-filename-to-verify-truncation-2026-07-08.txt";
    render(
      <AttachmentCard
        filename={long}
        contentType="text/plain"
        href="https://cdn.example/x.txt"
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    const nameEl = screen.getByTitle(long);
    expect(nameEl.className).toContain("truncate");
  });

  it("tabs in reading order: primary Open → Download → Delete", async () => {
    const user = userEvent.setup();
    render(
      <AttachmentCard
        filename="manual.pdf"
        contentType="application/pdf"
        href="https://cdn.example/manual.pdf"
        onPreview={() => {}}
        onDownload={() => {}}
        onDelete={() => {}}
      />,
    );
    await user.tab();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: /open/i }),
    );
    await user.tab();
    expect(document.activeElement).toBe(screen.getByTitle("Download"));
    await user.tab();
    expect(document.activeElement).toBe(screen.getByTitle("Remove attachment"));
  });
});
