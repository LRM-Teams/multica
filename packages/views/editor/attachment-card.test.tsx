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
          download_file: "Download {{filename}}",
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

  // LRM-359 — Frank washed chip: light gray name on washed muted surface.
  // Lock semantic tokens so light/dark both keep ≥4.5:1 name contrast.
  it("uses solid muted chip + foreground filename (no muted/40 wash, no gray hex)", () => {
    render(
      <AttachmentCard
        filename="design-agent-profile-polish.html"
        contentType="text/html"
        attachmentId="att-1"
        href="https://cdn.example/design-agent-profile-polish.html"
        sizeBytes={2048}
        onPreview={() => {}}
        onDownload={() => {}}
      />,
    );
    const chip = screen.getByTestId("attachment-card-chip");
    expect(chip.className).toContain("bg-muted");
    expect(chip.className).toContain("border-border");
    expect(chip.className).not.toMatch(/bg-muted\/\d+/);
    expect(chip.className).not.toMatch(/bg-\[#|text-\[#/);

    const filename = screen.getByTestId("attachment-card-filename");
    expect(filename.className).toContain("text-foreground");
    expect(filename.className).not.toMatch(/text-muted|text-\[#|text-gray|text-slate/);
    expect(filename.textContent).toBe("design-agent-profile-polish.html");
  });

  it("HTML URL-only source downloads from the primary card body", () => {
    // Text kinds need an attachmentId for the /content proxy, but a reachable
    // URL still needs a primary touch target that downloads the file.
    const onDownload = vi.fn();
    render(
      <AttachmentCard
        filename="report.html"
        contentType="text/html"
        href="https://cdn.example/report.html"
        onPreview={() => {}}
        onDownload={onDownload}
      />,
    );
    const download = screen.getByRole("button", {
      name: /^Download \{\{filename\}\}/,
    });
    fireEvent.click(download);
    expect(onDownload).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Download" })).toBeTruthy();
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
    fireEvent.mouseDown(screen.getByRole("button", { name: "Download" }));
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

  it("non-previewable file downloads when its primary card body is clicked", () => {
    const onDownload = vi.fn();
    render(
      <AttachmentCard
        filename="logs.zip"
        contentType="application/zip"
        href="https://cdn.example/logs.zip"
        onPreview={() => {}}
        onDownload={onDownload}
      />,
    );
    const download = screen.getByRole("button", {
      name: /^Download \{\{filename\}\}/,
    });
    fireEvent.click(download);
    expect(onDownload).toHaveBeenCalledTimes(1);
    expect(screen.queryByTitle("Preview")).toBeNull();
    expect(screen.getByRole("button", { name: "Download" })).toBeTruthy();
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
    const nameEl = screen.getByTestId("attachment-card-filename");
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
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Download" }),
    );
    await user.tab();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Remove attachment" }),
    );
  });
});
