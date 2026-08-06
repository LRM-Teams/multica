import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render as rtlRender, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactElement } from "react";
import type { Attachment } from "@multica/core/types";

const openExternalMock = vi.hoisted(() => vi.fn());

vi.mock("../platform", () => ({
  openExternal: openExternalMock,
}));

// vi.hoisted: factories run before module evaluation, letting us name mocks
// referenced from inside vi.mock factories below. The Error classes must be
// hoisted too because vi.mock is itself hoisted above the top-level `class`
// declarations.
const {
  getAttachmentTextContentMock,
  downloadMock,
  getBaseUrlMock,
  FakePreviewTooLargeError,
  FakePreviewUnsupportedError,
} = vi.hoisted(() => {
  class FakePreviewTooLargeError extends Error {
    constructor() {
      super("too large");
      this.name = "PreviewTooLargeError";
    }
  }
  class FakePreviewUnsupportedError extends Error {
    constructor() {
      super("unsupported");
      this.name = "PreviewUnsupportedError";
    }
  }
  return {
    getAttachmentTextContentMock: vi.fn(),
    downloadMock: vi.fn(),
    // Default to the web shape (empty base, same-origin). Tests covering
    // the desktop-renderer / standalone-shell case override per-test.
    getBaseUrlMock: vi.fn(() => ""),
    FakePreviewTooLargeError,
    FakePreviewUnsupportedError,
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    getAttachmentTextContent: getAttachmentTextContentMock,
    getBaseUrl: getBaseUrlMock,
  },
  PreviewTooLargeError: FakePreviewTooLargeError,
  PreviewUnsupportedError: FakePreviewUnsupportedError,
}));

vi.mock("./use-download-attachment", () => ({
  useDownloadAttachment: () => downloadMock,
}));

// Module-level flags toggled per-test: simulate desktop (openInNewTab
// adapter present) vs web (omitted), and the no-slug case where the
// modal sits outside a workspace route.
const { openInNewTabMock, getShareableUrlMock, navState, slugState } =
  vi.hoisted(() => ({
    openInNewTabMock: vi.fn(),
    getShareableUrlMock: vi.fn((p: string) => `https://app.example${p}`),
    navState: { hasOpenInNewTab: true },
    slugState: { value: "acme" as string | null },
  }));

vi.mock("../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/issues",
    searchParams: new URLSearchParams(),
    ...(navState.hasOpenInNewTab ? { openInNewTab: openInNewTabMock } : {}),
    getShareableUrl: getShareableUrlMock,
  }),
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspaceSlug: () => slugState.value,
  };
});

// ReadonlyContent has a heavy import surface (lowlight + KaTeX + Mermaid).
// Stub it so the markdown dispatch test only verifies wiring.
vi.mock("./readonly-content", () => ({
  ReadonlyContent: ({ content }: { content: string }) => (
    <div data-testid="readonly-content">{content}</div>
  ),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (sel: (s: Record<string, Record<string, string>>) => string) =>
      sel({
        image: { download: "Download" },
        attachment: {
          preview: "Preview",
          preview_loading: "Loading preview…",
          preview_failed: "Couldn't load preview",
          preview_too_large: "File is too large to preview. Please download.",
          preview_unsupported: "This file type can't be previewed.",
          close: "Close",
          download_failed: "",
          open_in_new_tab: "Open in new tab",
        },
      }),
  }),
}));

import {
  AttachmentPreviewModal,
  useAttachmentPreview,
} from "./attachment-preview-modal";
import { rendersFromUrlAlone } from "./utils/preview";
import { renderHook, act as hookAct } from "@testing-library/react";

// A real UUID literal — `attachmentIdFromDownloadURL` validates the shape, so
// the "recovered from the URL" cases must use one that actually parses.
const ATTACHMENT_ID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301";

// Fresh QueryClient per render — no retries (preview errors are typed,
// not transient) and no caching across tests so each scenario is hermetic.
function render(ui: ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return rtlRender(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

function makeAttachment(overrides: Partial<Attachment> = {}): Attachment {
  return {
    id: "att-1",
    workspace_id: "ws-1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "member",
    uploader_id: "u-1",
    filename: "test.bin",
    url: "https://cdn.example.test/att-1.bin",
    download_url: "https://cdn.example.test/att-1.bin?Signature=s",
    markdown_url: "https://cdn.example.test/api/attachments/att-1/download",
    content_type: "application/octet-stream",
    size_bytes: 0,
    created_at: "2026-05-13T00:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  navState.hasOpenInNewTab = true;
  slugState.value = "acme";
  // Default to web's same-origin empty base so existing absolute-URL tests
  // remain unaffected by the relative-URL resolution added in normalize().
  getBaseUrlMock.mockReturnValue("");
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("AttachmentPreviewModal — dispatch", () => {
  it("renders an <img> centered in the modal for image content types", () => {
    const att = makeAttachment({ filename: "shot.png", content_type: "image/png" });
    render(<AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={() => {}} />);
    const img = document.querySelector("img");
    expect(img).toBeTruthy();
    expect(img?.getAttribute("src")).toBe(att.download_url);
    expect(img?.getAttribute("alt")).toBe(att.filename);
  });

  it("renders an <img> from a URL-only source for image filenames", () => {
    const url = "https://cdn.example.test/orphan.png?Signature=s";
    render(
      <AttachmentPreviewModal
        source={{ kind: "url", url, filename: "orphan.png" }}
        open
        onClose={() => {}}
      />,
    );
    const img = document.querySelector("img");
    expect(img?.getAttribute("src")).toBe(url);
  });

  it("renders nothing for a directly-mounted PDF source, never an iframe (#591/#799)", () => {
    // Real usage never reaches this: both open() and tryOpen() dispatch pdf
    // sources to openExternal and never mount this modal (tryOpen-gate
    // suite below). A direct mount is unreachable through any current
    // caller, but must still render no fallback dialog and definitely no
    // iframe — the app's global CSP `frame-ancestors 'none'` refuses ANY
    // iframe embed of the download URL (same-origin included), so an
    // inline PDF preview here always dead-ends.
    const att = makeAttachment({ filename: "manual.pdf", content_type: "application/pdf" });
    render(
      <AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={() => {}} />,
    );
    // The modal portals to document.body, so the content region lives
    // outside RTL's `container` — query the document directly.
    expect(document.querySelector("iframe")).toBeNull();
    expect(document.querySelector(".min-h-0.flex-1")?.textContent).toBe("");
  });

  it("renders a <video> for video/* content types", () => {
    const att = makeAttachment({ filename: "clip.mp4", content_type: "video/mp4" });
    render(<AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={() => {}} />);
    const video = document.querySelector("video");
    expect(video).toBeTruthy();
    expect(video?.getAttribute("src")).toBe(att.download_url);
  });

  it("renders an <audio> for audio/* content types", () => {
    const att = makeAttachment({ filename: "note.mp3", content_type: "audio/mpeg" });
    render(<AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={() => {}} />);
    const audio = document.querySelector("audio");
    expect(audio).toBeTruthy();
  });




  it("shows unsupported fallback when no PreviewKind matches", () => {
    const att = makeAttachment({ filename: "blob.zip", content_type: "application/zip" });
    render(<AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={() => {}} />);
    expect(screen.getByText("This file type can't be previewed.")).toBeTruthy();
  });
});

describe("AttachmentPreviewModal — server-relative download_url resolution (MUL-2976)", () => {
  // The unified `/api/attachments/{id}/download` endpoint returns a
  // server-relative path on non-CloudFront deployments. The web app keeps
  // working same-origin because `apiBaseUrl=""`, but the desktop renderer
  // is loaded from `app://` / file: / dev-server origin and needs the
  // absolute URL — otherwise `<img src>`, `<iframe src>`, `<video src>`
  // hit the shell origin and fail.
  it("prefixes the configured API base for image previews when download_url is server-relative", () => {
    getBaseUrlMock.mockReturnValue("https://api.example.test");
    const att = makeAttachment({
      filename: "shot.png",
      content_type: "image/png",
      download_url: "/api/attachments/att-1/download",
    });
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={() => {}}
      />,
    );
    const img = document.querySelector("img");
    expect(img?.getAttribute("src")).toBe(
      "https://api.example.test/api/attachments/att-1/download",
    );
  });

  it("opens the API-base-prefixed URL for PDFs when download_url is server-relative", () => {
    // #591/#799: PDFs never mount the modal (tryOpen hands off directly),
    // so this exercises the same resolution through the real entry point
    // instead of a header button that no longer exists for this kind.
    getBaseUrlMock.mockReturnValue("https://api.example.test");
    const att = makeAttachment({
      filename: "manual.pdf",
      content_type: "application/pdf",
      download_url: "/api/attachments/att-1/download",
    });
    const { result } = renderHook(() => useAttachmentPreview());
    hookAct(() => {
      result.current.tryOpen({ kind: "full", attachment: att });
    });
    expect(openExternalMock).toHaveBeenCalledWith(
      "https://api.example.test/api/attachments/att-1/download",
    );
  });

  it("keeps a same-origin relative URL untouched when the configured base is empty (web)", () => {
    // Default web shape — empty base. Browser resolves the relative path
    // against the document origin, no prefix needed.
    const att = makeAttachment({
      filename: "shot.png",
      content_type: "image/png",
      download_url: "/api/attachments/att-1/download",
    });
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={() => {}}
      />,
    );
    const img = document.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/api/attachments/att-1/download");
  });

  it("trims a trailing slash on the configured base when joining a relative URL", () => {
    getBaseUrlMock.mockReturnValue("https://api.example.test/");
    const att = makeAttachment({
      filename: "shot.png",
      content_type: "image/png",
      download_url: "/api/attachments/att-1/download",
    });
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={() => {}}
      />,
    );
    const img = document.querySelector("img");
    expect(img?.getAttribute("src")).toBe(
      "https://api.example.test/api/attachments/att-1/download",
    );
  });

  it("passes an already-absolute CloudFront/presigned download_url through unchanged", () => {
    getBaseUrlMock.mockReturnValue("https://api.example.test");
    const att = makeAttachment({
      filename: "shot.png",
      content_type: "image/png",
      download_url: "https://cdn.example.test/att-1.png?Signature=s",
    });
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={() => {}}
      />,
    );
    const img = document.querySelector("img");
    expect(img?.getAttribute("src")).toBe(
      "https://cdn.example.test/att-1.png?Signature=s",
    );
  });
});


describe("AttachmentPreviewModal — controls", () => {
  it("ESC closes the modal", () => {
    const onClose = vi.fn();
    const att = makeAttachment({ filename: "manual.pdf", content_type: "application/pdf" });
    render(<AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={onClose} />);
    act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    expect(onClose).toHaveBeenCalled();
  });

  it("Download button invokes useDownloadAttachment with the attachment id", () => {
    const att = makeAttachment({ filename: "manual.pdf", content_type: "application/pdf" });
    render(<AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={() => {}} />);
    // Two Download CTAs may exist (header + unsupported fallback). The header
    // button is always present, look it up by aria-label/title.
    const buttons = screen.getAllByTitle("Download");
    expect(buttons.length).toBeGreaterThan(0);
    fireEvent.click(buttons[0]!);
    expect(downloadMock).toHaveBeenCalledWith("att-1");
  });

  it("clicking the backdrop closes the modal", () => {
    const onClose = vi.fn();
    const att = makeAttachment({ filename: "manual.pdf", content_type: "application/pdf" });
    render(<AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={onClose} />);
    const dialog = screen.getByRole("dialog");
    fireEvent.click(dialog);
    expect(onClose).toHaveBeenCalled();
  });
});

describe("AttachmentPreviewModal — URL-only source", () => {
  it("renders <video> from the URL when no attachment record is available", () => {
    const url = "https://cdn.example.test/clip.mp4?Signature=s";
    render(
      <AttachmentPreviewModal
        source={{ kind: "url", url, filename: "clip.mp4" }}
        open
        onClose={() => {}}
      />,
    );
    const video = document.querySelector("video");
    expect(video?.getAttribute("src")).toBe(url);
  });

  it("falls back to unsupported when a text kind is forced through a URL source", () => {
    // The tryOpen gate normally prevents this; direct mount tests the
    // defensive branch inside PreviewContent.
    render(
      <AttachmentPreviewModal
        source={{ kind: "url", url: "https://x/y.md", filename: "y.md" }}
        open
        onClose={() => {}}
      />,
    );
    expect(screen.getByText("This file type can't be previewed.")).toBeTruthy();
  });

  it("Download button opens the raw URL externally when no attachment id is available", () => {
    const url = "https://cdn.example.test/orphan.pdf?Signature=s";
    render(
      <AttachmentPreviewModal
        source={{ kind: "url", url, filename: "orphan.pdf" }}
        open
        onClose={() => {}}
      />,
    );
    const button = screen.getAllByTitle("Download")[0]!;
    fireEvent.click(button);
    expect(openExternalMock).toHaveBeenCalledWith(url);
    expect(downloadMock).not.toHaveBeenCalled();
  });
});

describe("AttachmentPreviewModal — open-in-new-tab (HTML only)", () => {
  it("renders the open-in-new-tab button in the header for HTML attachments", async () => {
    getAttachmentTextContentMock.mockResolvedValueOnce({
      text: "<p>hi</p>",
      originalContentType: "text/html",
    });
    const att = makeAttachment({
      filename: "report.html",
      content_type: "text/html",
    });
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={() => {}}
      />,
    );
    expect(screen.getByTitle("Open in new tab")).toBeTruthy();
  });

  it("invokes navigation.openInNewTab with the preview path and closes the modal (desktop)", async () => {
    getAttachmentTextContentMock.mockResolvedValueOnce({
      text: "<p>hi</p>",
      originalContentType: "text/html",
    });
    const att = makeAttachment({
      filename: "report.html",
      content_type: "text/html",
    });
    const onClose = vi.fn();
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={onClose}
      />,
    );
    fireEvent.click(screen.getByTitle("Open in new tab"));
    expect(openInNewTabMock).toHaveBeenCalledWith(
      "/acme/attachments/att-1/preview?name=report.html",
      "report.html",
      { activate: true },
    );
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("falls back to window.open against the shareable URL and closes the modal (web)", async () => {
    navState.hasOpenInNewTab = false;
    getAttachmentTextContentMock.mockResolvedValueOnce({
      text: "<p>hi</p>",
      originalContentType: "text/html",
    });
    const windowOpenSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);
    const att = makeAttachment({
      filename: "report.html",
      content_type: "text/html",
    });
    const onClose = vi.fn();
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={onClose}
      />,
    );
    fireEvent.click(screen.getByTitle("Open in new tab"));
    expect(openInNewTabMock).not.toHaveBeenCalled();
    expect(windowOpenSpy).toHaveBeenCalledWith(
      "https://app.example/acme/attachments/att-1/preview?name=report.html",
      "_blank",
      "noopener,noreferrer",
    );
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not render the new-tab button for a kind with no open-in-new-tab path (e.g. image)", () => {
    const att = makeAttachment({
      filename: "shot.png",
      content_type: "image/png",
    });
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={() => {}}
      />,
    );
    expect(screen.queryByTitle("Open in new tab")).toBeNull();
  });

  it("does not render the new-tab button for a directly-mounted PDF (#591/#799)", () => {
    // Real usage never reaches this — tryOpen hands PDFs to openExternal
    // before the modal ever mounts (see the tryOpen-gate suite below) — but
    // a direct mount (the defensive PreviewContent branch) shouldn't offer
    // a second, dead open-in-new-tab affordance either.
    const att = makeAttachment({ filename: "manual.pdf", content_type: "application/pdf" });
    render(
      <AttachmentPreviewModal source={{ kind: "full", attachment: att }} open onClose={() => {}} />,
    );
    expect(screen.queryByTitle("Open in new tab")).toBeNull();
  });

  it("does not render the new-tab button when there is no workspace slug", async () => {
    slugState.value = null;
    getAttachmentTextContentMock.mockResolvedValueOnce({
      text: "<p>hi</p>",
      originalContentType: "text/html",
    });
    const att = makeAttachment({
      filename: "report.html",
      content_type: "text/html",
    });
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: att }}
        open
        onClose={() => {}}
      />,
    );
    expect(screen.queryByTitle("Open in new tab")).toBeNull();
  });
});

describe("useAttachmentPreview — tryOpen gate", () => {
  it("accepts a full attachment for a media kind", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    const att = makeAttachment({ filename: "x.jpg", content_type: "image/jpeg" });
    let opened = false;
    hookAct(() => {
      opened = result.current.tryOpen({ kind: "full", attachment: att });
    });
    expect(opened).toBe(true);
  });

  it("accepts a URL source for a media kind", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    let opened = false;
    hookAct(() => {
      opened = result.current.tryOpen({
        kind: "url",
        url: "https://x/y.png",
        filename: "y.png",
      });
    });
    expect(opened).toBe(true);
  });

  // #591/#799: a PDF click never mounts the modal — it hands off straight
  // to the browser's native viewer, synchronously in the same tryOpen call
  // (still inside the original click's gesture stack, so the tab isn't
  // popup-blocked).
  it("hands a full-attachment PDF source straight to openExternal, no modal", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    const att = makeAttachment({ filename: "manual.pdf", content_type: "application/pdf" });
    let opened = false;
    hookAct(() => {
      opened = result.current.tryOpen({ kind: "full", attachment: att });
    });
    expect(opened).toBe(true);
    expect(openExternalMock).toHaveBeenCalledTimes(1);
    expect(openExternalMock).toHaveBeenCalledWith(att.download_url);
    expect(result.current.modal).toBeNull();
  });

  it("hands a URL-only PDF source straight to openExternal, no modal", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    const url = "https://cdn.example.test/orphan.pdf?Signature=s";
    let opened = false;
    hookAct(() => {
      opened = result.current.tryOpen({ kind: "url", url, filename: "orphan.pdf" });
    });
    expect(opened).toBe(true);
    expect(openExternalMock).toHaveBeenCalledTimes(1);
    expect(openExternalMock).toHaveBeenCalledWith(url);
    expect(result.current.modal).toBeNull();
  });

  it("rejects a URL source for a text kind — /content proxy needs an id", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    let opened = true;
    hookAct(() => {
      opened = result.current.tryOpen({
        kind: "url",
        url: "https://x/y.md",
        filename: "y.md",
      });
    });
    expect(opened).toBe(false);
  });

  it("rejects a source whose filename isn't a previewable type", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    let opened = true;
    hookAct(() => {
      opened = result.current.tryOpen({
        kind: "url",
        url: "https://x/y.zip",
        filename: "y.zip",
      });
    });
    expect(opened).toBe(false);
  });

  // #831 — the reported bug: markdown/txt "can't be previewed in the modal".
  // The modal always supported them; the source degraded to download because
  // the attachment record wasn't in the current entity's `attachments` prop,
  // so no id was passed — even when the URL itself carried one. The invariant
  // is "previewable whenever an id is OBTAINABLE", not "whenever the record
  // was in the prop", so these gate on the id, not on the source shape.
  it.each([
    ["markdown", "notes.md"],
    ["plain text", "notes.txt"],
  ])(
    "accepts a URL source for a %s kind when the id was recovered from the URL (#831)",
    (_label, filename) => {
      const { result } = renderHook(() => useAttachmentPreview());
      let opened = false;
      hookAct(() => {
        opened = result.current.tryOpen({
          kind: "url",
          url: `/api/attachments/${ATTACHMENT_ID}/download`,
          filename,
          attachmentId: ATTACHMENT_ID,
        });
      });
      expect(opened).toBe(true);
    },
  );

  it("still rejects a text kind when no id is obtainable — the /content proxy is unaddressable (#831)", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    let opened = true;
    hookAct(() => {
      opened = result.current.tryOpen({
        kind: "url",
        url: "https://cdn.example.test/pasted.md",
        filename: "pasted.md",
      });
    });
    expect(opened).toBe(false);
  });
});

// LRM-1180 — a URL-only source could not declare its MIME type, so `normalize`
// hardcoded `contentType: ""` and `getPreviewKind` had only the filename
// extension to go on. A pasted screenshot (composer tray: `blob:…` +
// "image.png"-less filename) therefore resolved to kind `null` and the modal
// rendered "can't be previewed" + Download for an ordinary PNG. The fix is
// additive: the field is optional, so every pre-existing caller keeps the
// extension-only behaviour.
describe("PreviewSource url variant — optional contentType (LRM-1180)", () => {
  it("previews an extension-less image when the caller supplies the real MIME", () => {
    render(
      <AttachmentPreviewModal
        source={{
          kind: "url",
          url: "blob:pasted-screenshot",
          filename: "pasted-screenshot",
          contentType: "image/png",
        }}
        open
        onClose={() => {}}
      />,
    );

    expect(screen.getByAltText("pasted-screenshot")).toHaveAttribute(
      "src",
      "blob:pasted-screenshot",
    );
    expect(
      screen.queryByText("This file type can't be previewed."),
    ).toBeNull();
  });

  it("keeps the extension-only fallback when contentType is omitted (zero regression for existing callers)", () => {
    render(
      <AttachmentPreviewModal
        source={{
          kind: "url",
          url: "https://cdn.example.test/photo.png",
          filename: "photo.png",
        }}
        open
        onClose={() => {}}
      />,
    );
    expect(screen.getByAltText("photo.png")).toBeInTheDocument();
  });

  it("tryOpen resolves the kind from contentType, not just the extension", () => {
    const { result } = renderHook(() => useAttachmentPreview());
    let opened = false;
    hookAct(() => {
      opened = result.current.tryOpen({
        kind: "url",
        url: "blob:pasted",
        filename: "pasted",
        contentType: "image/png",
      });
    });
    expect(opened).toBe(true);

    let withoutMime = true;
    hookAct(() => {
      withoutMime = result.current.tryOpen({
        kind: "url",
        url: "blob:pasted",
        filename: "pasted",
      });
    });
    expect(withoutMime).toBe(false);
  });
});

// #831 — the second defect: AttachmentCard re-listed the URL-previewable kinds
// and omitted `image`, so a URL-only image was rendered with no preview
// affordance even though the modal renders images from a URL fine. The card
// now imports this predicate instead of re-listing, so the affordance and the
// modal can't drift apart. Aimed at the invariant (the two agree), not at
// either one's spelling.
describe("rendersFromUrlAlone — single source of truth for URL-only previewability (#831)", () => {
  it.each(["image", "pdf", "video", "audio"] as const)(
    "%s renders from a URL alone",
    (kind) => {
      expect(rendersFromUrlAlone(kind)).toBe(true);
    },
  );

  it.each(["markdown", "html", "text"] as const)(
    "%s does NOT render from a URL alone — it needs the ID-keyed /content proxy",
    (kind) => {
      expect(rendersFromUrlAlone(kind)).toBe(false);
    },
  );
});

// LRM-1298 — the modal declares `role="dialog"` + `aria-modal="true"` from a
// hand-rolled `createPortal` overlay, but shipped with no focus management at
// all: no initial focus, no trap (Tab walked straight out into the page behind
// the overlay), and no restore on close (the whole file had zero `.focus()`
// calls). Escape / backdrop close already worked, so these tests pin only the
// focus contract and must keep the existing dismissal paths untouched.
describe("AttachmentPreviewModal — focus contract (LRM-1298)", () => {
  const imageAttachment = makeAttachment({
    filename: "shot.png",
    content_type: "image/png",
  });

  function getDialog() {
    return screen.getByRole("dialog");
  }

  function focusablesInDialog() {
    return Array.from(
      getDialog().querySelectorAll<HTMLElement>("button, iframe, video, audio"),
    );
  }

  /**
   * Opener + a background control that lives OUTSIDE the portal, so an escaped
   * Tab has somewhere real to land (that is exactly the shipped defect).
   *
   * `unmountOpenerWhileOpen` reproduces the LRM-1177 shape: the trigger is
   * conditionally rendered, so it is detached for the whole time the modal is
   * open and comes back as a *different* DOM node. Node identity alone cannot
   * restore focus there.
   */
  function FocusHarness({
    unmountOpenerWhileOpen = false,
  }: {
    unmountOpenerWhileOpen?: boolean;
  }) {
    const [open, setOpen] = useState(false);
    return (
      <div>
        {(!open || !unmountOpenerWhileOpen) && (
          <button
            type="button"
            id="preview-opener"
            onClick={() => setOpen(true)}
          >
            Open preview
          </button>
        )}
        <button type="button" id="background-control">
          Background control
        </button>
        <AttachmentPreviewModal
          source={{ kind: "full", attachment: imageAttachment }}
          open={open}
          onClose={() => setOpen(false)}
        />
      </div>
    );
  }

  it("moves focus into the dialog on open instead of leaving it on <body>", () => {
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: imageAttachment }}
        open
        onClose={() => {}}
      />,
    );

    const dialog = getDialog();
    expect(document.activeElement).not.toBe(document.body);
    expect(dialog.contains(document.activeElement)).toBe(true);
    // The labelled `role="dialog"` node itself takes focus so screen readers
    // announce the filename label, which means it must be programmatically
    // focusable without joining the Tab order.
    expect(document.activeElement).toBe(dialog);
    expect(dialog).toHaveAttribute("tabindex", "-1");
  });

  it("wraps Tab from the last control back to the first (no escape forward)", () => {
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: imageAttachment }}
        open
        onClose={() => {}}
      />,
    );

    const controls = focusablesInDialog();
    expect(controls.length).toBeGreaterThan(1);
    const first = controls[0]!;
    const last = controls[controls.length - 1]!;

    act(() => last.focus());
    fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(first);
  });

  it("wraps Shift+Tab from the first control to the last (no escape backward)", () => {
    render(
      <AttachmentPreviewModal
        source={{ kind: "full", attachment: imageAttachment }}
        open
        onClose={() => {}}
      />,
    );

    const controls = focusablesInDialog();
    const first = controls[0]!;
    const last = controls[controls.length - 1]!;

    act(() => first.focus());
    fireEvent.keyDown(first, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  it("pulls focus back in when it is already outside the aria-modal dialog", () => {
    render(<FocusHarness />);
    const opener = document.getElementById("preview-opener")!;
    act(() => opener.focus());
    fireEvent.click(opener);

    const background = document.getElementById("background-control")!;
    act(() => background.focus());
    expect(getDialog().contains(document.activeElement)).toBe(false);

    fireEvent.keyDown(background, { key: "Tab" });
    expect(getDialog().contains(document.activeElement)).toBe(true);
  });

  it("restores focus to the trigger after closing", () => {
    render(<FocusHarness />);
    const opener = document.getElementById("preview-opener")!;
    act(() => opener.focus());
    fireEvent.click(opener);
    expect(getDialog().contains(document.activeElement)).toBe(true);

    fireEvent.click(screen.getByLabelText("Close"));

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(
      document.getElementById("preview-opener"),
    );
  });

  it("restores focus after Escape, not only after the Close button", () => {
    render(<FocusHarness />);
    const opener = document.getElementById("preview-opener")!;
    act(() => opener.focus());
    fireEvent.click(opener);

    fireEvent.keyDown(document, { key: "Escape" });

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(
      document.getElementById("preview-opener"),
    );
  });

  it("re-finds a trigger that was unmounted while open and returned as a new node (LRM-1177 shape)", () => {
    render(<FocusHarness unmountOpenerWhileOpen />);
    const opener = document.getElementById("preview-opener")!;
    act(() => opener.focus());
    fireEvent.click(opener);

    // The trigger really is gone while the modal is open — restoring by node
    // identity alone would silently no-op here.
    expect(document.getElementById("preview-opener")).toBeNull();

    fireEvent.click(screen.getByLabelText("Close"));

    const reborn = document.getElementById("preview-opener");
    expect(reborn).not.toBeNull();
    expect(reborn).not.toBe(opener);
    expect(document.activeElement).toBe(reborn);
  });

  it("keeps focus inside the dialog when the trigger cannot be re-located", () => {
    // No id / data-testid to re-find, and the node is detached by close time:
    // focus must not be dumped on an unrelated control or on <body>.
    function AnonymousTriggerHarness() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          {!open && (
            <button type="button" onClick={() => setOpen(true)}>
              Open preview
            </button>
          )}
          <AttachmentPreviewModal
            source={{ kind: "full", attachment: imageAttachment }}
            open={open}
            onClose={() => setOpen(false)}
          />
        </div>
      );
    }

    render(<AnonymousTriggerHarness />);
    const opener = screen.getByText("Open preview");
    act(() => opener.focus());
    fireEvent.click(opener);
    expect(getDialog().contains(document.activeElement)).toBe(true);

    fireEvent.click(screen.getByLabelText("Close"));
    expect(screen.queryByRole("dialog")).toBeNull();
    // Nothing to restore to → leave focus where it is rather than stealing it.
    expect(document.activeElement).not.toBeNull();
  });
});
