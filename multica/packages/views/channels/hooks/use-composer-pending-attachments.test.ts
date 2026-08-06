import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import type { ComposerDraftAttachment } from "@multica/core/channels";
import {
  buildChatMessageParts,
  useComposerPendingAttachments,
} from "./use-composer-pending-attachments";

function makeFile(name: string, type = "image/png", size = 12): File {
  return new File([new Uint8Array(size)], name, { type });
}

function makeUploadResult(
  overrides: Partial<UploadResult> & { id: string },
): UploadResult {
  const id = overrides.id;
  return {
    workspace_id: "ws",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "user",
    uploader_id: "u1",
    filename: "photo.png",
    url: `https://cdn.example/${id}`,
    download_url: `https://cdn.example/${id}/dl`,
    markdown_url: `https://cdn.example/${id}/md`,
    content_type: "image/png",
    size_bytes: 12,
    created_at: "2026-01-01T00:00:00Z",
    link: `https://cdn.example/${id}`,
    markdownLink: `https://cdn.example/${id}/md`,
    ...overrides,
  };
}

describe("buildChatMessageParts", () => {
  it("builds text + attachment parts and supports attachment-only", () => {
    expect(
      buildChatMessageParts("hello", [
        { type: "attachment", attachment_id: "a1" },
        { type: "attachment", attachment_id: "a2", filename: "b.pdf" },
      ]),
    ).toEqual([
      { type: "text", text: "hello" },
      { type: "attachment", attachment_id: "a1" },
      { type: "attachment", attachment_id: "a2", filename: "b.pdf" },
    ]);

    expect(
      buildChatMessageParts("  \n", [{ type: "attachment", attachment_id: "a1" }]),
    ).toEqual([{ type: "attachment", attachment_id: "a1" }]);

    expect(buildChatMessageParts("only text", [])).toEqual([
      { type: "text", text: "only text" },
    ]);
    expect(buildChatMessageParts("", [])).toEqual([]);
  });
});

describe("useComposerPendingAttachments", () => {
  let createObjectURL: ReturnType<typeof vi.fn>;
  let revokeObjectURL: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    createObjectURL = vi.fn(() => "blob:preview");
    revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", {
      createObjectURL,
      revokeObjectURL,
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("addFiles starts uploading and success → ready with attachmentId", async () => {
    let resolveUpload: (value: UploadResult | null) => void = () => {};
    const upload = vi.fn(
      () =>
        new Promise<UploadResult | null>((resolve) => {
          resolveUpload = resolve;
        }),
    );

    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    const file = makeFile("shot.png");
    act(() => {
      result.current.addFiles([file]);
    });

    expect(result.current.pending).toHaveLength(1);
    expect(result.current.pending[0]?.status).toBe("uploading");
    expect(result.current.pending[0]?.filename).toBe("shot.png");
    expect(result.current.hasUploading).toBe(true);
    expect(result.current.readyAttachmentParts).toEqual([]);
    expect(upload).toHaveBeenCalledWith(file);

    await act(async () => {
      resolveUpload(makeUploadResult({ id: "att-1", filename: "shot.png" }));
      await Promise.resolve();
    });

    expect(result.current.pending[0]?.status).toBe("ready");
    expect(result.current.pending[0]?.attachmentId).toBe("att-1");
    expect(result.current.hasUploading).toBe(false);
    expect(result.current.readyAttachmentParts).toEqual([
      expect.objectContaining({
        type: "attachment",
        attachment_id: "att-1",
        filename: "shot.png",
      }),
    ]);
  });

  it("readyAttachmentParts preserve add order across mixed statuses", async () => {
    const resolvers: Array<(v: UploadResult | null) => void> = [];
    const upload = vi.fn(
      () =>
        new Promise<UploadResult | null>((resolve) => {
          resolvers.push(resolve);
        }),
    );

    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    act(() => {
      result.current.addFiles([makeFile("a.png"), makeFile("b.png"), makeFile("c.pdf", "application/pdf")]);
    });
    expect(result.current.pending.map((p) => p.filename)).toEqual([
      "a.png",
      "b.png",
      "c.pdf",
    ]);

    // Resolve out of order: b, then a, then c fails.
    await act(async () => {
      resolvers[1]?.(makeUploadResult({ id: "id-b", filename: "b.png" }));
      await Promise.resolve();
    });
    await act(async () => {
      resolvers[0]?.(makeUploadResult({ id: "id-a", filename: "a.png" }));
      await Promise.resolve();
    });
    await act(async () => {
      resolvers[2]?.(null);
      await Promise.resolve();
    });

    expect(result.current.readyAttachmentParts.map((p) => p.attachment_id)).toEqual([
      "id-a",
      "id-b",
    ]);
    expect(result.current.pending[2]?.status).toBe("error");
  });

  it("remove drops the item and ignores a late upload result", async () => {
    let resolveUpload: (value: UploadResult | null) => void = () => {};
    const upload = vi.fn(
      () =>
        new Promise<UploadResult | null>((resolve) => {
          resolveUpload = resolve;
        }),
    );

    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    act(() => {
      result.current.addFiles([makeFile("gone.png")]);
    });
    const localId = result.current.pending[0]!.localId;

    act(() => {
      result.current.remove(localId);
    });
    expect(result.current.pending).toEqual([]);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview");

    await act(async () => {
      resolveUpload(makeUploadResult({ id: "late" }));
      await Promise.resolve();
    });
    expect(result.current.pending).toEqual([]);
    expect(result.current.readyAttachmentParts).toEqual([]);
  });

  it("retry re-uploads an errored item", async () => {
    const upload = vi
      .fn()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(makeUploadResult({ id: "recovered" }));

    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    await act(async () => {
      result.current.addFiles([makeFile("retry.png")]);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(result.current.pending[0]?.status).toBe("error");

    await act(async () => {
      result.current.retry(result.current.pending[0]!.localId);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(upload).toHaveBeenCalledTimes(2);
    expect(result.current.pending[0]?.status).toBe("ready");
    expect(result.current.pending[0]?.attachmentId).toBe("recovered");
  });

  it("surfaces thrown API errors on the chip (does not hardcode Upload failed)", async () => {
    const upload = vi.fn().mockRejectedValue(new Error("not a channel member"));

    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    await act(async () => {
      result.current.addFiles([makeFile("doc.pdf", "application/pdf")]);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.pending[0]?.status).toBe("error");
    expect(result.current.pending[0]?.errorMessage).toBe("not a channel member");
  });

  it("soft-null upload leaves errorMessage unset for tray i18n fallback", async () => {
    const upload = vi.fn().mockResolvedValue(null);

    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    await act(async () => {
      result.current.addFiles([makeFile("soft.png")]);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.pending[0]?.status).toBe("error");
    expect(result.current.pending[0]?.errorMessage).toBeUndefined();
  });

  it("clear empties the tray and revokes blob previews still held", async () => {
    // Keep uploads pending so previewUrl stays a blob: URL (ready swaps to remote).
    const upload = vi.fn(() => new Promise<UploadResult | null>(() => {}));
    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    act(() => {
      result.current.addFiles([makeFile("a.png"), makeFile("b.png")]);
    });
    expect(result.current.pending.length).toBe(2);
    expect(result.current.pending.every((p) => p.previewUrl === "blob:preview")).toBe(
      true,
    );

    act(() => {
      result.current.clear();
    });
    expect(result.current.pending).toEqual([]);
    expect(result.current.readyAttachmentParts).toEqual([]);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview");
  });

  it("revokes blob preview when upload succeeds with a remote image URL", async () => {
    const upload = vi.fn().mockResolvedValue(
      makeUploadResult({ id: "x", link: "https://cdn.example/x" }),
    );
    const { result } = renderHook(() =>
      useComposerPendingAttachments({ upload }),
    );

    await act(async () => {
      result.current.addFiles([makeFile("a.png")]);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview");
    expect(result.current.pending[0]?.previewUrl).toBe("https://cdn.example/x");
  });

  it("clears the tray when resetKey changes (conversation switch)", async () => {
    const upload = vi.fn(() => new Promise<UploadResult | null>(() => {}));
    const { result, rerender } = renderHook(
      ({ resetKey }: { resetKey: string }) =>
        useComposerPendingAttachments({ upload, resetKey }),
      { initialProps: { resetKey: "channel-a" } },
    );

    act(() => {
      result.current.addFiles([makeFile("a.png")]);
    });
    expect(result.current.pending).toHaveLength(1);

    rerender({ resetKey: "channel-b" });
    expect(result.current.pending).toEqual([]);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview");
  });

  describe("LRM-801 draft persistence", () => {
    it("saves ready attachments (id + remote preview) and restores them on remount", async () => {
      let saved: ComposerDraftAttachment[] = [];
      const persistence = {
        load: () => saved,
        save: (items: ComposerDraftAttachment[]) => {
          saved = items;
        },
      };
      let resolveUpload: (value: UploadResult | null) => void = () => {};
      const upload = vi.fn(
        () =>
          new Promise<UploadResult | null>((resolve) => {
            resolveUpload = resolve;
          }),
      );

      const first = renderHook(() =>
        useComposerPendingAttachments({ upload, resetKey: "channel-a", persistence }),
      );
      act(() => {
        first.result.current.addFiles([makeFile("shot.png")]);
      });
      await act(async () => {
        resolveUpload(makeUploadResult({ id: "att-1", filename: "shot.png" }));
        await Promise.resolve();
      });
      expect(saved).toEqual([
        expect.objectContaining({
          attachmentId: "att-1",
          filename: "shot.png",
          previewUrl: "https://cdn.example/att-1",
          unrestorable: undefined,
        }),
      ]);
      first.unmount();

      const second = renderHook(() =>
        useComposerPendingAttachments({ upload, resetKey: "channel-a", persistence }),
      );
      expect(second.result.current.pending).toHaveLength(1);
      expect(second.result.current.pending[0]?.status).toBe("ready");
      expect(second.result.current.pending[0]?.attachmentId).toBe("att-1");
      expect(second.result.current.pending[0]?.previewUrl).toBe("https://cdn.example/att-1");
      expect(second.result.current.readyAttachmentParts).toEqual([
        expect.objectContaining({ attachment_id: "att-1" }),
      ]);
    });

    it("restores unfinished uploads as stale placeholders that never block send", async () => {
      let saved: ComposerDraftAttachment[] = [];
      const persistence = {
        load: () => saved,
        save: (items: ComposerDraftAttachment[]) => {
          saved = items;
        },
      };
      // Upload never resolves before the "leave".
      const upload = vi.fn(() => new Promise<UploadResult | null>(() => {}));

      const first = renderHook(() =>
        useComposerPendingAttachments({ upload, resetKey: "channel-a", persistence }),
      );
      act(() => {
        first.result.current.addFiles([makeFile("stuck.png")]);
      });
      expect(saved).toEqual([
        expect.objectContaining({ unrestorable: true, attachmentId: undefined }),
      ]);
      first.unmount();

      const second = renderHook(() =>
        useComposerPendingAttachments({ upload, resetKey: "channel-a", persistence }),
      );
      expect(second.result.current.pending[0]?.status).toBe("stale");
      expect(second.result.current.readyAttachmentParts).toEqual([]);
      expect(second.result.current.hasUploading).toBe(false);

      act(() => {
        second.result.current.remove(second.result.current.pending[0]!.localId);
      });
      expect(second.result.current.pending).toEqual([]);
      expect(saved).toEqual([]);
    });

    it("never persists blob: previews (they die with the tab)", async () => {
      let saved: ComposerDraftAttachment[] = [];
      const persistence = {
        load: () => saved,
        save: (items: ComposerDraftAttachment[]) => {
          saved = items;
        },
      };
      let resolveUpload: (value: UploadResult | null) => void = () => {};
      const upload = vi.fn(
        () =>
          new Promise<UploadResult | null>((resolve) => {
            resolveUpload = resolve;
          }),
      );

      renderHook(() =>
        useComposerPendingAttachments({ upload, resetKey: "channel-a", persistence }),
      ).result.current.addFiles([makeFile("shot.png")]);
      await act(async () => {
        // No remote link → tray keeps the blob preview; draft must not.
        resolveUpload(makeUploadResult({ id: "att-1", link: "" }));
        await Promise.resolve();
      });
      expect(saved[0]?.attachmentId).toBe("att-1");
      expect(saved[0]?.previewUrl).toBeUndefined();
    });

    it("switching conversations restores that draft and never leaks A's tray into B", async () => {
      const drafts: Record<string, ComposerDraftAttachment[]> = {
        "channel-a": [
          {
            attachmentId: "att-a",
            filename: "a.png",
            contentType: "image/png",
            sizeBytes: 1,
            previewUrl: "https://cdn.example/att-a",
          },
        ],
        "channel-b": [
          {
            unrestorable: true,
            filename: "b.png",
            contentType: "image/png",
            sizeBytes: 2,
          },
        ],
      };
      const upload = vi.fn(() => new Promise<UploadResult | null>(() => {}));

      const { result, rerender } = renderHook(
        ({ resetKey }: { resetKey: "channel-a" | "channel-b" }) =>
          useComposerPendingAttachments({
            upload,
            resetKey,
            persistence: {
              load: () => drafts[resetKey],
              save: (items) => {
                drafts[resetKey] = items;
              },
            },
          }),
        { initialProps: { resetKey: "channel-a" as "channel-a" | "channel-b" } },
      );

      expect(result.current.pending[0]?.status).toBe("ready");
      expect(result.current.pending[0]?.attachmentId).toBe("att-a");

      rerender({ resetKey: "channel-b" });
      expect(result.current.pending).toHaveLength(1);
      expect(result.current.pending[0]?.status).toBe("stale");
      expect(result.current.pending[0]?.filename).toBe("b.png");
      // A's draft untouched by the switch.
      expect(drafts["channel-a"]).toEqual([
        expect.objectContaining({ attachmentId: "att-a" }),
      ]);
    });

    // Hard-refresh race: zustand persist rehydrates async. Until hydrateSignal
    // leaves "", an empty tray must not save([]) over draft attachments.
    it("holds empty save until hydrateSignal, then restores late attachments", async () => {
      let saved: ComposerDraftAttachment[] = [
        {
          attachmentId: "att-late",
          filename: "late.png",
          contentType: "image/png",
          sizeBytes: 4,
          previewUrl: "https://cdn.example/att-late",
        },
      ];
      let hydrateSignal = "";
      const upload = vi.fn(() => new Promise<UploadResult | null>(() => {}));

      const { result, rerender } = renderHook(
        ({ signal }: { signal: string }) =>
          useComposerPendingAttachments({
            upload,
            resetKey: "channel-a",
            persistence: {
              // Simulate pre-rehydrate: load sees [] until signal flips.
              load: () => (signal === "" ? [] : saved),
              save: (items) => {
                saved = items;
              },
              hydrateSignal: signal,
            },
          }),
        { initialProps: { signal: hydrateSignal } },
      );

      expect(result.current.pending).toHaveLength(0);
      // Empty save must not run while still rehydrating.
      expect(saved).toHaveLength(1);

      hydrateSignal = "att-late";
      rerender({ signal: hydrateSignal });
      expect(result.current.pending).toHaveLength(1);
      expect(result.current.pending[0]?.attachmentId).toBe("att-late");
      expect(result.current.pending[0]?.previewUrl).toBe("https://cdn.example/att-late");
      expect(saved).toEqual([
        expect.objectContaining({ attachmentId: "att-late" }),
      ]);
    });
  });
});
