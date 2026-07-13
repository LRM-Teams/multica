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

  it("lays out chips in a horizontal flex row (not a vertical stack)", () => {
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
    ];

    render(
      <ComposerAttachmentTray pending={pending} onRemove={vi.fn()} onRetry={vi.fn()} />,
    );

    const tray = screen.getByTestId("composer-attachment-tray");
    expect(tray.className).toMatch(/flex-row/);
    expect(tray.className).not.toMatch(/flex-col(?!-)/);

    const first = screen.getByTestId("composer-tray-item-f1");
    const second = screen.getByTestId("composer-tray-item-f2");
    // Content-sized chips sit side-by-side; full-width stretch forced one-per-row wrap.
    expect(first.className).toMatch(/\bw-fit\b/);
    expect(second.className).toMatch(/\bw-fit\b/);
    expect(first.className).not.toMatch(/\bw-full\b/);
    expect(second.className).not.toMatch(/\bw-full\b/);
  });
});
