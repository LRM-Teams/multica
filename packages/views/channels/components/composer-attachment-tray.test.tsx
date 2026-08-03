import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PendingAttachment } from "../hooks/use-composer-pending-attachments";
import { ComposerAttachmentTray } from "./composer-attachment-tray";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, string>,
    ) => {
      const dict = {
        composer: {
          tray_remove_aria: `Remove ${vars?.filename ?? ""}`,
          tray_retry_aria: `Retry upload of ${vars?.filename ?? ""}`,
          tray_preview_aria: `Preview ${vars?.filename ?? ""}`,
          tray_uploading: "Uploading",
          tray_upload_failed: "Upload failed",
          tray_reselect: "Needs re-selection",
        },
      };
      const value = selector(dict);
      return typeof value === "string" ? value : String(value);
    },
  }),
}));

// LRM-1180 — the tray owns a real `useAttachmentPreview()` handle. The stand-in
// keeps the *lifecycle* that matters to the tray (open() records the source,
// `modal` flips null → node → null on close) without dragging the whole editor
// barrel + modal dependency tree into this component test. The modal itself is
// covered by attachment-preview-modal.test.tsx.
const previewOpenSpy = vi.fn();
vi.mock("../../editor", async () => {
  const React = await import("react");
  return {
    useAttachmentPreview: () => {
      const [open, setOpen] = React.useState(false);
      return {
        tryOpen: vi.fn(),
        open: (source: unknown) => {
          previewOpenSpy(source);
          setOpen(true);
        },
        modal: open
          ? React.createElement(
              "button",
              {
                type: "button",
                "data-testid": "fake-preview-close",
                onClick: () => setOpen(false),
              },
              "close",
            )
          : null,
      };
    },
  };
});

function item(overrides: Partial<PendingAttachment> & Pick<PendingAttachment, "localId" | "status" | "filename">): PendingAttachment {
  return {
    contentType: "image/png",
    sizeBytes: 100,
    previewUrl: "blob:preview",
    ...overrides,
  };
}

describe("ComposerAttachmentTray", () => {
  it("renders nothing when the tray is empty", () => {
    const { container } = render(
      <ComposerAttachmentTray pending={[]} onRemove={vi.fn()} onRetry={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders image thumbs and file chips with remove controls", async () => {
    const user = userEvent.setup();
    const onRemove = vi.fn();
    const pending: PendingAttachment[] = [
      item({
        localId: "img-1",
        status: "ready",
        filename: "photo.png",
        attachmentId: "a1",
        previewUrl: "https://cdn.example/photo.png",
      }),
      item({
        localId: "file-1",
        status: "ready",
        filename: "notes.pdf",
        contentType: "application/pdf",
        attachmentId: "a2",
        previewUrl: undefined,
      }),
    ];

    render(
      <ComposerAttachmentTray pending={pending} onRemove={onRemove} onRetry={vi.fn()} />,
    );

    expect(screen.getByTestId("composer-attachment-tray")).toBeInTheDocument();
    expect(screen.getByAltText("photo.png")).toHaveAttribute(
      "src",
      "https://cdn.example/photo.png",
    );
    expect(screen.getByText("notes.pdf")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Remove photo.png" }));
    expect(onRemove).toHaveBeenCalledWith("img-1");
  });

  it("shows error state and retry for failed uploads", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    const pending: PendingAttachment[] = [
      item({
        localId: "err-1",
        status: "error",
        filename: "broken.png",
        errorMessage: "network",
      }),
    ];

    render(
      <ComposerAttachmentTray pending={pending} onRemove={vi.fn()} onRetry={onRetry} />,
    );

    expect(screen.getByTestId("composer-tray-item-err-1")).toHaveAttribute(
      "data-status",
      "error",
    );
    await user.click(screen.getByRole("button", { name: "Retry upload of broken.png" }));
    expect(onRetry).toHaveBeenCalledWith("err-1");
  });

  it("LRM-801: stale draft placeholder shows 需重新选择 + remove only (no retry)", async () => {
    const user = userEvent.setup();
    const onRemove = vi.fn();
    const onRetry = vi.fn();
    const pending: PendingAttachment[] = [
      item({
        localId: "stale-1",
        status: "stale",
        filename: "gone.png",
        previewUrl: undefined,
        attachmentId: undefined,
      }),
    ];

    render(
      <ComposerAttachmentTray pending={pending} onRemove={onRemove} onRetry={onRetry} />,
    );

    const chip = screen.getByTestId("composer-tray-item-stale-1");
    expect(chip).toHaveAttribute("data-status", "stale");
    expect(chip.textContent).toContain("Needs re-selection");
    expect(
      screen.queryByRole("button", { name: /Retry upload/ }),
    ).toBeNull();
    await user.click(screen.getByRole("button", { name: "Remove gone.png" }));
    expect(onRemove).toHaveBeenCalledWith("stale-1");
  });

  it("marks uploading items", () => {
    render(
      <ComposerAttachmentTray
        pending={[
          item({
            localId: "up-1",
            status: "uploading",
            filename: "wait.png",
          }),
        ]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    expect(screen.getByTestId("composer-tray-item-up-1")).toHaveAttribute(
      "data-status",
      "uploading",
    );
  });

  it("is a single horizontal strip (no column stack, no wrap)", () => {
    const pending: PendingAttachment[] = [
      item({
        localId: "f1",
        status: "ready",
        filename: "cleanup-grok-xy-bridge.sh",
        contentType: "application/x-sh",
        attachmentId: "a1",
        previewUrl: undefined,
      }),
      item({
        localId: "f2",
        status: "ready",
        filename: "cleanup-grok-xy-bridge-2.sh",
        contentType: "text/x-shellscript",
        attachmentId: "a2",
        previewUrl: undefined,
      }),
      item({
        localId: "img-1",
        status: "ready",
        filename: "shot.png",
        contentType: "image/png",
        attachmentId: "a3",
        previewUrl: "https://cdn.example/shot.png",
      }),
    ];

    render(
      <ComposerAttachmentTray pending={pending} onRemove={vi.fn()} onRetry={vi.fn()} />,
    );

    const tray = screen.getByTestId("composer-attachment-tray");
    expect(tray.className).toMatch(/\bflex-row\b/);
    expect(tray.className).toMatch(/\bflex-nowrap\b/);
    expect(tray.className).toMatch(/\boverflow-x-auto\b/);
    // Must not use column layout or wrap (both produce vertical stacks).
    expect(tray.className).not.toMatch(/\bflex-col\b/);
    expect(tray.className).not.toMatch(/\bflex-wrap\b/);

    for (const id of ["f1", "f2", "img-1"]) {
      const el = screen.getByTestId(`composer-tray-item-${id}`);
      expect(el.className).toMatch(/\bshrink-0\b/);
      expect(el.className).not.toMatch(/\bw-full\b/);
    }

    expect(screen.getByTestId("composer-tray-item-f1")).toHaveAttribute(
      "data-kind",
      "file",
    );
    expect(screen.getByTestId("composer-tray-item-img-1")).toHaveAttribute(
      "data-kind",
      "image",
    );
  });

  it("on mobile web keeps remove visible without hover (touch)", () => {
    render(
      <ComposerAttachmentTray
        isMobile
        pending={[
          item({
            localId: "img-m",
            status: "ready",
            filename: "phone.png",
            attachmentId: "am",
            previewUrl: "https://cdn.example/phone.png",
          }),
        ]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    const tray = screen.getByTestId("composer-attachment-tray");
    expect(tray).toHaveAttribute("data-mobile", "true");
    const remove = screen.getByRole("button", { name: "Remove phone.png" });
    // Must not rely on group-hover opacity-0 for the only remove control.
    expect(remove.className).toMatch(/\bopacity-100\b/);
    expect(remove.className).not.toMatch(/\bopacity-0\b/);
  });

  // LRM-353 — tray chrome stays on background / muted / border tokens (no light hex).
  it("LRM-353: tray chips use semantic surface tokens only", () => {
    const src = readFileSync(
      resolve(dirname(fileURLToPath(import.meta.url)), "composer-attachment-tray.tsx"),
      "utf8",
    );
    expect(src).not.toMatch(/#f4f4f4/);
    expect(src).not.toMatch(/hover:bg-\[#/);
    expect(src).toMatch(/border-border/);
    expect(src).toMatch(/bg-muted/);
    expect(src).toMatch(/bg-background/);

    render(
      <ComposerAttachmentTray
        pending={[
          item({
            localId: "file-tok",
            status: "uploading",
            filename: "draft.pdf",
            contentType: "application/pdf",
            previewUrl: undefined,
          }),
        ]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("composer-tray-item-file-tok");
    expect(chip.className).toMatch(/border-border/);
    expect(chip.className).toMatch(/bg-muted/);
  });
});

// ---------------------------------------------------------------------------
// LRM-1180 — v2 frozen design (parent LRM-1150): zoomable thumb + 20px
// overflow-corner remove button. Numbers below are the frozen spec, not taste:
// 20px visual + after:-inset-0.5 → 24px hit target (SC 2.5.8 exactly), and the
// button sits 8px outside the thumb so occlusion drops 41.3% → 4.6% on mobile.
// ---------------------------------------------------------------------------
describe("ComposerAttachmentTray — LRM-1180 v2 frozen design", () => {
  beforeEach(() => {
    previewOpenSpy.mockClear();
  });

  function imageItem(
    overrides: Partial<PendingAttachment> = {},
  ): PendingAttachment {
    return item({
      localId: "img-1",
      status: "ready",
      filename: "shot.png",
      attachmentId: "a1",
      previewUrl: "blob:shot",
      ...overrides,
    });
  }

  it("tray reserves the overflow corner: pt-2 pr-2 -mt-2 + gap-3 (overflow-y-hidden clips the padding box)", () => {
    render(
      <ComposerAttachmentTray
        pending={[imageItem()]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const tray = screen.getByTestId("composer-attachment-tray");
    // Without pt-2/pr-2 the 8px-outdented button is clipped by overflow-y-hidden.
    expect(tray.className).toMatch(/\bpt-2\b/);
    expect(tray.className).toMatch(/\bpr-2\b/);
    // -mt-2 cancels the reserved top padding so the composer doesn't grow.
    expect(tray.className).toMatch(/-mt-2\b/);
    // gap-2 (8px) exactly equals the outdent → button would touch the next item.
    expect(tray.className).toMatch(/\bgap-3\b/);
    expect(tray.className).not.toMatch(/\bgap-2\b/);
    // Horizontal-strip contract from LRM-353/LRM-801 must survive.
    expect(tray.className).toMatch(/\boverflow-y-hidden\b/);
    expect(tray.className).toMatch(/\bflex-nowrap\b/);
  });

  it("image remove button is 20px in the overflow corner with a 24px hit area", () => {
    render(
      <ComposerAttachmentTray
        pending={[imageItem()]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const remove = screen.getByRole("button", { name: "Remove shot.png" });
    expect(remove.className).toMatch(/\bsize-5\b/); // 20px visual
    expect(remove.className).not.toMatch(/\bsize-9\b/); // not the 36px live button
    expect(remove.className).toMatch(/\brounded-full\b/);
    const corner = remove.parentElement as HTMLElement;
    expect(corner.className).toMatch(/-right-2\b/);
    expect(corner.className).toMatch(/-top-2\b/);
    // after:-inset-0.5 lifts the 20px visual to a 24px pointer target.
    expect(remove.className).toMatch(/after:-inset-0\.5\b/);
    expect(remove.className).toMatch(/\brelative\b/);
  });

  it("mobile image remove button is the same 20px overflow corner (not size-9) and stays visible", () => {
    render(
      <ComposerAttachmentTray
        isMobile
        pending={[imageItem({ filename: "phone.png" })]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const remove = screen.getByRole("button", { name: "Remove phone.png" });
    expect(remove.className).toMatch(/\bsize-5\b/);
    expect(remove.className).not.toMatch(/\bsize-9\b/);
    expect(remove.className).toMatch(/\bopacity-100\b/);
  });

  it("non-image chip keeps its in-chip remove button (no overflow corner)", () => {
    render(
      <ComposerAttachmentTray
        pending={[
          item({
            localId: "file-1",
            status: "ready",
            filename: "notes.pdf",
            contentType: "application/pdf",
            previewUrl: undefined,
          }),
        ]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const remove = screen.getByRole("button", { name: "Remove notes.pdf" });
    const holder = remove.parentElement as HTMLElement;
    expect(holder.className).not.toMatch(/-right-2\b/);
    expect(remove.className).not.toMatch(/\bsize-5\b/);
    // A file chip is not a zoom entry point.
    expect(screen.queryByRole("button", { name: /^Preview/ })).toBeNull();
  });

  it("clicking the thumb opens the shared preview with the real MIME type", async () => {
    const user = userEvent.setup();
    render(
      <ComposerAttachmentTray
        pending={[imageItem({ contentType: "image/png" })]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    const zoom = screen.getByRole("button", { name: "Preview shot.png" });
    expect(zoom.className).toMatch(/\bcursor-zoom-in\b/);
    await user.click(zoom);

    // contentType must be forwarded: a pasted screenshot often has no
    // extension, and getPreviewKind falls back to the filename without it.
    expect(previewOpenSpy).toHaveBeenCalledWith({
      kind: "url",
      url: "blob:shot",
      filename: "shot.png",
      contentType: "image/png",
      attachmentId: "a1",
    });
    expect(screen.getByTestId("fake-preview-close")).toBeInTheDocument();
  });

  it("returns focus to the thumb after the preview closes", async () => {
    const user = userEvent.setup();
    render(
      <ComposerAttachmentTray
        pending={[imageItem()]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    const zoom = screen.getByRole("button", { name: "Preview shot.png" });
    await user.click(zoom);
    await user.click(screen.getByTestId("fake-preview-close"));

    expect(screen.getByRole("button", { name: "Preview shot.png" })).toHaveFocus();
  });

  it("uploading keeps zoom available (blob previewUrl) but hides the centered zoom glyph", () => {
    render(
      <ComposerAttachmentTray
        pending={[imageItem({ status: "uploading" })]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Preview shot.png" }),
    ).toBeEnabled();
    // Two centered elements (spinner + zoom glyph) would collide.
    expect(screen.queryByTestId("composer-tray-zoom-hint-img-1")).toBeNull();
  });

  it("error state centers retry, keeps remove in the corner, and drops the zoom entry point", () => {
    render(
      <ComposerAttachmentTray
        pending={[imageItem({ status: "error", errorMessage: "network" })]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    const retry = screen.getByRole("button", {
      name: "Retry upload of shot.png",
    });
    expect(retry.className).toMatch(/-translate-x-1\/2/);
    expect(retry.className).toMatch(/-translate-y-1\/2/);
    expect(retry.className).toMatch(/\bsize-6\b/); // 24px primary recovery action

    const remove = screen.getByRole("button", { name: "Remove shot.png" });
    expect((remove.parentElement as HTMLElement).className).toMatch(/-right-2\b/);

    // The centered slot is taken by retry, and a failed item means retry-or-drop.
    expect(screen.queryByRole("button", { name: /^Preview/ })).toBeNull();
  });

  it("stale draft placeholder gets no zoom entry point (no previewUrl to show)", () => {
    render(
      <ComposerAttachmentTray
        pending={[
          item({
            localId: "stale-1",
            status: "stale",
            filename: "gone.png",
            previewUrl: undefined,
            attachmentId: undefined,
          }),
        ]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button", { name: /^Preview/ })).toBeNull();
  });
});
