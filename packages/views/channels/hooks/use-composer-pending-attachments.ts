"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createSafeId } from "@multica/core/utils";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import type { MessagePart } from "@multica/core/types";

export type PendingAttachmentStatus = "uploading" | "ready" | "error";

export type PendingAttachment = {
  localId: string;
  status: PendingAttachmentStatus;
  /** Kept for retry after a failed upload. */
  file?: File;
  attachmentId?: string;
  filename: string;
  contentType: string;
  sizeBytes: number;
  /** Blob or remote URL for tray preview only — never sent on the wire. */
  previewUrl?: string;
  errorMessage?: string;
};

export type AttachmentMessagePart = Extract<MessagePart, { type: "attachment" }>;

export type UseComposerPendingAttachmentsOptions = {
  /** Upload one file; return null on soft failure (e.g. toast already shown). */
  upload: (file: File) => Promise<UploadResult | null>;
  /**
   * When this identity changes (channel id, thread root, …), pending tray
   * state is cleared so files never leak across conversations.
   */
  resetKey?: string | null;
};

export type UseComposerPendingAttachmentsResult = {
  pending: PendingAttachment[];
  addFiles: (files: File[]) => void;
  remove: (localId: string) => void;
  retry: (localId: string) => void;
  clear: () => void;
  hasUploading: boolean;
  /** Ready attachment parts in add order for send assembly. */
  readyAttachmentParts: AttachmentMessagePart[];
};

function isImageContentType(contentType: string): boolean {
  return contentType.startsWith("image/");
}

function previewUrlForFile(file: File): string | undefined {
  if (!isImageContentType(file.type)) return undefined;
  try {
    return URL.createObjectURL(file);
  } catch {
    return undefined;
  }
}

function revokePreviewUrl(url: string | undefined): void {
  if (!url || !url.startsWith("blob:")) return;
  try {
    URL.revokeObjectURL(url);
  } catch {
    // ignore
  }
}

function toAttachmentPart(item: PendingAttachment): AttachmentMessagePart | null {
  if (item.status !== "ready" || !item.attachmentId) return null;
  return {
    type: "attachment",
    attachment_id: item.attachmentId,
    filename: item.filename || undefined,
    content_type: item.contentType || undefined,
    size_bytes: item.sizeBytes || undefined,
  };
}

/**
 * Slack-style composer tray state: files upload into a pending list (not the
 * editor document). Send consumers take `readyAttachmentParts` in add order.
 */
export function useComposerPendingAttachments(
  opts: UseComposerPendingAttachmentsOptions,
): UseComposerPendingAttachmentsResult {
  const uploadRef = useRef(opts.upload);
  uploadRef.current = opts.upload;

  const [pending, setPending] = useState<PendingAttachment[]>([]);
  // Track which localIds are still live so late upload results for removed
  // items are ignored (and their blob URLs already revoked on remove).
  const liveIdsRef = useRef(new Set<string>());

  const runUpload = useCallback(async (localId: string, file: File) => {
    try {
      const result = await uploadRef.current(file);
      if (!liveIdsRef.current.has(localId)) return;
      if (!result?.id) {
        setPending((prev) =>
          prev.map((item) =>
            item.localId === localId
              ? {
                  ...item,
                  status: "error" as const,
                  errorMessage: "Upload failed",
                  attachmentId: undefined,
                }
              : item,
          ),
        );
        return;
      }
      setPending((prev) =>
        prev.map((item) => {
          if (item.localId !== localId) return item;
          const nextContentType = result.content_type || item.contentType;
          const preferRemote =
            isImageContentType(nextContentType) && !!result.link;
          const nextPreview = preferRemote ? result.link : item.previewUrl;
          if (preferRemote && item.previewUrl && item.previewUrl !== nextPreview) {
            revokePreviewUrl(item.previewUrl);
          }
          return {
            ...item,
            status: "ready" as const,
            attachmentId: result.id,
            filename: result.filename || item.filename,
            contentType: nextContentType,
            sizeBytes: result.size_bytes || item.sizeBytes,
            // Prefer remote URL for ready images when available.
            previewUrl: nextPreview,
            errorMessage: undefined,
          };
        }),
      );
    } catch (err) {
      if (!liveIdsRef.current.has(localId)) return;
      const message = err instanceof Error ? err.message : "Upload failed";
      setPending((prev) =>
        prev.map((item) =>
          item.localId === localId
            ? {
                ...item,
                status: "error" as const,
                errorMessage: message,
                attachmentId: undefined,
              }
            : item,
        ),
      );
    }
  }, []);

  const addFiles = useCallback(
    (files: File[]) => {
      if (files.length === 0) return;
      const nextItems: PendingAttachment[] = files.map((file) => {
        const localId = createSafeId();
        liveIdsRef.current.add(localId);
        return {
          localId,
          status: "uploading" as const,
          file,
          filename: file.name || "file",
          contentType: file.type || "application/octet-stream",
          sizeBytes: file.size,
          previewUrl: previewUrlForFile(file),
        };
      });
      setPending((prev) => [...prev, ...nextItems]);
      for (const item of nextItems) {
        if (item.file) void runUpload(item.localId, item.file);
      }
    },
    [runUpload],
  );

  const remove = useCallback((localId: string) => {
    liveIdsRef.current.delete(localId);
    setPending((prev) => {
      const target = prev.find((item) => item.localId === localId);
      revokePreviewUrl(target?.previewUrl);
      return prev.filter((item) => item.localId !== localId);
    });
  }, []);

  const retry = useCallback(
    (localId: string) => {
      setPending((prev) => {
        const target = prev.find((item) => item.localId === localId);
        if (!target?.file || target.status !== "error") return prev;
        liveIdsRef.current.add(localId);
        void runUpload(localId, target.file);
        return prev.map((item) =>
          item.localId === localId
            ? {
                ...item,
                status: "uploading" as const,
                errorMessage: undefined,
                attachmentId: undefined,
              }
            : item,
        );
      });
    },
    [runUpload],
  );

  const clear = useCallback(() => {
    setPending((prev) => {
      for (const item of prev) {
        liveIdsRef.current.delete(item.localId);
        revokePreviewUrl(item.previewUrl);
      }
      return [];
    });
  }, []);

  // Own the conversation-switch reset inside the hook so parents do not
  // bounce clear() through effects (react-doctor no-pass-data-to-parent).
  useEffect(() => {
    clear();
  }, [opts.resetKey, clear]);

  const hasUploading = useMemo(
    () => pending.some((item) => item.status === "uploading"),
    [pending],
  );

  const readyAttachmentParts = useMemo(() => {
    const parts: AttachmentMessagePart[] = [];
    for (const item of pending) {
      const part = toAttachmentPart(item);
      if (part) parts.push(part);
    }
    return parts;
  }, [pending]);

  return {
    pending,
    addFiles,
    remove,
    retry,
    clear,
    hasUploading,
    readyAttachmentParts,
  };
}

/**
 * Assemble chat send `parts` from body text + ready tray attachment parts.
 * Attachment-only (empty text) is valid.
 */
export function buildChatMessageParts(
  text: string,
  readyAttachmentParts: readonly AttachmentMessagePart[],
): MessagePart[] {
  const parts: MessagePart[] = [];
  const trimmed = text.trim();
  if (trimmed) {
    parts.push({ type: "text", text: trimmed });
  }
  for (const part of readyAttachmentParts) {
    if (!part.attachment_id) continue;
    parts.push(part);
  }
  return parts;
}
