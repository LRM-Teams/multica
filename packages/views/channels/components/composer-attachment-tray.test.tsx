import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
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
