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
// LRM-1264 R3 — tray imports attachment-preview-modal leaf (not the TipTap barrel).
vi.mock("../../editor/attachment-preview-modal", async () => {
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

  // LRM-1228 reverses the "file chips are untouched" half of this design: the
  // corner treatment is now the single remove rule for every chip kind. What
  // survives from LRM-1180 is that a file chip is not a zoom entry point.
  it("non-image chip is not a zoom entry point", () => {
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

// ---------------------------------------------------------------------------
// LRM-1228 — the other half of Frank's "手机端 button 太大": LRM-1180/#2047 only
// shrank the remove button on *image* thumbs, so non-image / stale chips were
// still carrying a 36px (`size-9`) in-chip button on mobile web. This block
// freezes the corner treatment as the single remove rule for every chip kind:
// 20px visual + `after:-inset-0.5` → 24px pointer target (SC 2.5.8), parked in
// the `-right-2 -top-2` overflow corner, with the chip reserving `pr-3` (12px)
// so the outdented button never lands on the filename.
// ---------------------------------------------------------------------------
describe("ComposerAttachmentTray — LRM-1228 file/stale chip remove button", () => {
  function fileItem(
    overrides: Partial<PendingAttachment> = {},
  ): PendingAttachment {
    return item({
      localId: "file-1",
      status: "ready",
      filename: "notes.pdf",
      contentType: "application/pdf",
      previewUrl: undefined,
      attachmentId: "a2",
      ...overrides,
    });
  }

  for (const isMobile of [false, true]) {
    const label = isMobile ? "mobile web" : "desktop";

    it(`${label}: file chip remove is 20px in the overflow corner with a 24px hit area`, () => {
      render(
        <ComposerAttachmentTray
          isMobile={isMobile}
          pending={[fileItem()]}
          onRemove={vi.fn()}
          onRetry={vi.fn()}
        />,
      );
      const remove = screen.getByRole("button", { name: "Remove notes.pdf" });
      expect(remove.className).toMatch(/\bsize-5\b/); // 20px visual
      // The two live sizes this slice retires.
      expect(remove.className).not.toMatch(/\bsize-9\b/); // 36px mobile
      expect(remove.className).not.toMatch(/\bsize-6\b/); // 24px desktop
      expect(remove.className).toMatch(/\brounded-full\b/);
      // after:-inset-0.5 lifts the 20px visual back to a 24px pointer target.
      expect(remove.className).toMatch(/\brelative\b/);
      expect(remove.className).toMatch(/after:-inset-0\.5\b/);

      const corner = remove.parentElement as HTMLElement;
      expect(corner.className).toMatch(/-right-2\b/);
      expect(corner.className).toMatch(/-top-2\b/);
      // Sitting half outside the chip, it needs its own surface to read.
      expect(remove.className).toMatch(/border-border/);
      expect(remove.className).toMatch(/bg-background/);
    });
  }

  it("file chip reserves pr-3 so the outdented button never covers the filename", () => {
    render(
      <ComposerAttachmentTray
        pending={[fileItem()]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("composer-tray-item-file-1");
    // The button's inner half is 12px wide; pr-3 == 12px clears the text.
    expect(chip.className).toMatch(/\bpr-3\b/);
    expect(chip.className).toMatch(/\bpl-2\b/);
    // px-2 (8px right) would let `truncate` text run under the corner button.
    expect(chip.className).not.toMatch(/\bpx-2\b/);
  });

  it("remove stays visible on a file chip without hover (no image to protect)", () => {
    render(
      <ComposerAttachmentTray
        isMobile
        pending={[fileItem({ filename: "phone.pdf" })]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const remove = screen.getByRole("button", { name: "Remove phone.pdf" });
    expect(remove.className).toMatch(/\bopacity-100\b/);
    expect(remove.className).not.toMatch(/\bopacity-0\b/);
  });

  it("stale draft chip gets the same 20px corner remove and still no retry", async () => {
    const user = userEvent.setup();
    const onRemove = vi.fn();
    render(
      <ComposerAttachmentTray
        isMobile
        pending={[
          fileItem({
            localId: "stale-1",
            status: "stale",
            filename: "gone.png",
            contentType: "image/png",
            previewUrl: undefined,
            attachmentId: undefined,
          }),
        ]}
        onRemove={onRemove}
        onRetry={vi.fn()}
      />,
    );
    const remove = screen.getByRole("button", { name: "Remove gone.png" });
    expect(remove.className).toMatch(/\bsize-5\b/);
    expect(remove.className).not.toMatch(/\bsize-9\b/);
    expect((remove.parentElement as HTMLElement).className).toMatch(/-right-2\b/);
    expect(screen.queryByRole("button", { name: /Retry upload/ })).toBeNull();
    await user.click(remove);
    expect(onRemove).toHaveBeenCalledWith("stale-1");
  });

  it("error file chip keeps retry inline and moves only remove to the corner", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    render(
      <ComposerAttachmentTray
        isMobile
        pending={[
          fileItem({
            localId: "err-f",
            status: "error",
            filename: "broken.pdf",
            errorMessage: "network",
          }),
        ]}
        onRemove={vi.fn()}
        onRetry={onRetry}
      />,
    );
    const retry = screen.getByRole("button", {
      name: "Retry upload of broken.pdf",
    });
    // Out of scope for this slice (AC covers the remove button only): retry is
    // the primary recovery action and covers no image, so it stays inline.
    expect(retry.className).toMatch(/\bsize-9\b/);
    expect((retry.parentElement as HTMLElement).className).not.toMatch(
      /-right-2\b/,
    );

    const remove = screen.getByRole("button", { name: "Remove broken.pdf" });
    expect(remove.className).toMatch(/\bsize-5\b/);
    expect((remove.parentElement as HTMLElement).className).toMatch(
      /-right-2\b/,
    );
    await user.click(retry);
    expect(onRetry).toHaveBeenCalledWith("err-f");
  });

  it("does not regress the #2047 image thumb surface", () => {
    render(
      <ComposerAttachmentTray
        isMobile
        pending={[
          item({
            localId: "img-1",
            status: "ready",
            filename: "shot.png",
            previewUrl: "blob:shot",
            attachmentId: "a1",
          }),
        ]}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const remove = screen.getByRole("button", { name: "Remove shot.png" });
    expect(remove.className).toMatch(/\bsize-5\b/);
    expect((remove.parentElement as HTMLElement).className).toMatch(
      /-right-2\b/,
    );
    // Thumb keeps p-0 / max-w-none — the chip padding change must not leak here.
    const chip = screen.getByTestId("composer-tray-item-img-1");
    expect(chip.className).toMatch(/\bp-0\b/);
    expect(chip.className).not.toMatch(/\bpr-3\b/);
    expect(screen.getByRole("button", { name: "Preview shot.png" })).toBeInTheDocument();
  });
});
