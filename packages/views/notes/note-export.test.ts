import { describe, expect, it } from "vitest";
import { DEFAULT_NOTE_FORMAT } from "@multica/core/notes/format";
import type { NotePage } from "@multica/core/types";
import {
  buildNoteExportHtml,
  collectKatexExportCss,
  collectKatexExportCssFromDocument,
  noteExportBaseHref,
  renderNoteMarkdown,
  waitForNoteExportAssets,
} from "./note-export";

function page(content: string): NotePage {
  return {
    id: "page-1",
    workspace_id: "ws-1",
    parent_id: null,
    owner_user_id: "user-1",
    title: "Formula note",
    content,
    sort_key: "a",
    share_user_ids: [],
    can_manage_shares: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    deleted_at: null,
  };
}

describe("note export formulas", () => {
  it("renders inline $math$ with KaTeX instead of leaving the source dollars", () => {
    const html = renderNoteMarkdown("Energy is $E=mc^2$ in the note.");
    expect(html).toContain("katex");
    expect(html).not.toContain("$E=mc^2$");
  });

  it("renders a $$ block formula as display math", () => {
    const html = renderNoteMarkdown("Intro\n\n$$\nE = mc^2\n$$\n\nAfter");
    expect(html).toContain("katex-display");
    expect(html).not.toMatch(/\$\$\s*E = mc\^2\s*\$\$/);
  });

  it("includes KaTeX CSS so a downloaded HTML file can keep the formula look", () => {
    const html = buildNoteExportHtml(page("$x^2$"), DEFAULT_NOTE_FORMAT);
    expect(html).toContain("katex.min.css");
    expect(html).toContain("katex");
  });

  it("leaves dollars inside inline code as code", () => {
    const html = renderNoteMarkdown("Use `$E=mc^2$` in markdown.");
    expect(html).toContain("<code>$E=mc^2$</code>");
    expect(html).not.toContain("katex");
  });

  it("collects only KaTeX rules so PDF print can reuse the app fonts", () => {
    const css = collectKatexExportCss([
      {
        cssRules: [
          { cssText: "@font-face { font-family: KaTeX_Main; src: url(/_next/static/media/KaTeX.woff2); }" },
          { cssText: ".katex { font-family: KaTeX_Main; }" },
          { cssText: ".btn { display: flex; }" },
        ],
      },
    ]);
    expect(css).toContain("KaTeX_Main");
    expect(css).toContain(".katex");
    expect(css).not.toContain(".btn");
  });

  it("falls back to style tags when stylesheets do not expose cssRules", () => {
    const doc = document.implementation.createHTMLDocument("export");
    const style = doc.createElement("style");
    style.textContent = ".katex { font-family: KaTeX_Main; }";
    doc.head.appendChild(style);
    expect(collectKatexExportCssFromDocument(doc)).toContain(".katex { font-family: KaTeX_Main; }");
  });

  it("inlines collected KaTeX CSS instead of the jsDelivr stylesheet", () => {
    const html = buildNoteExportHtml(page("$x^2$"), DEFAULT_NOTE_FORMAT, {
      extraHead: noteExportBaseHref("https://app.test"),
      katexCss: ".katex { font-family: KaTeX_Main; }",
    });
    expect(html).toContain('<base href="https://app.test/" />');
    expect(html).toContain(".katex { font-family: KaTeX_Main; }");
    expect(html).not.toContain("cdn.jsdelivr.net");
  });

  it("resolves immediately when the print document has no pending stylesheets", async () => {
    const doc = document.implementation.createHTMLDocument("export");
    await expect(waitForNoteExportAssets(doc, 50)).resolves.toBeUndefined();
  });
});
