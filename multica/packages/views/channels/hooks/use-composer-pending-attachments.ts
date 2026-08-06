"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createSafeId } from "@multica/core/utils";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import type { ComposerDraftAttachment } from "@multica/core/channels";
import type { MessagePart } from "@multica/core/types";

/** `stale` = restored draft placeholder whose file never finished uploading. */
export type PendingAttachmentStatus = "uploading" | "ready" | "error" | "stale";

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
  /**
   * Upload one file. Prefer throwing on failure so the tray chip can show the
   * real API/error message (LRM-426). Returning null is treated as a soft
   * failure with no detail — avoid it for channel/DM composers.
   */
  upload: (file: File) => Promise<UploadResult | null>;
  /**
   * When this identity changes (channel id, thread root, …), pending tray
   * state is cleared so files never leak across conversations.
   */
  resetKey?: string | null;
  /**
   * LRM-801 — draft persistence for the tray, same lifecycle as the text
   * draft. `load` is read on mount and at every `resetKey` change (must read
   * fresh, e.g. via `store.getState()`); `save` receives the serialized tray
   * on every change (`[]` clears the attachment half of the draft).
   *
   * Optional `hydrateSignal` must change when the backing store finishes
   * async rehydration (e.g. a signature of `drafts[key]?.attachments`). The
   * tray owns local `useState`, so without this signal a cold mount races
   * zustand persist: `load()` returns `[]`, then `save([])` wipes the image
   * half while the text draft (selector-driven) still restores — hard-refresh
   * 「字在图丢」.
   */
  persistence?: {
    load: () => ComposerDraftAttachment[] | undefined;
    save: (items: ComposerDraftAttachment[]) => void;
    /** Bumps when persisted attachments become available after rehydrate. */
    hydrateSignal?: string;
  };
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

function serializeForDraft(items: PendingAttachment[]): ComposerDraftAttachment[] {
  return items.map((item) => {
    const restorable = item.status === "ready" && !!item.attachmentId;
    return {
      attachmentId: restorable ? item.attachmentId : undefined,
      filename: item.filename,
      contentType: item.contentType,
      sizeBytes: item.sizeBytes,
      previewUrl:
        restorable && item.previewUrl && !item.previewUrl.startsWith("blob:")
          ? item.previewUrl
          : undefined,
      unrestorable: restorable ? undefined : true,
    };
  });
}

function deserializeFromDraft(items: ComposerDraftAttachment[]): PendingAttachment[] {
  return items.map((item) => {
    const localId = createSafeId();
    const restorable = !item.unrestorable && !!item.attachmentId;
    return {
      localId,
      status: restorable ? ("ready" as const) : ("stale" as const),
      attachmentId: restorable ? item.attachmentId : undefined,
      filename: item.filename,
      contentType: item.contentType,
      sizeBytes: item.sizeBytes,
      previewUrl: restorable ? item.previewUrl : undefined,
    };
  });
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
  const persistenceRef = useRef(opts.persistence);
  persistenceRef.current = opts.persistence;

  const [pending, setPending] = useState<PendingAttachment[]>(() =>
    deserializeFromDraft(persistenceRef.current?.load() ?? []),
  );
  // Track which localIds are still live so late upload results for removed
  // items are ignored (and their blob URLs already revoked on remove).
  const liveIdsRef = useRef(new Set<string>());
  // LRM-801 — the tray starts out already hydrated for the initial resetKey
  // (useState initializer); a later resetKey switch flips this back to
  // "stale" until the rehydrate effect commits, so the save effect never
  // writes conversation A's tray into conversation B's draft.
  const hydratedKeyRef = useRef(opts.resetKey);
  // Until the backing draft store has finished (or timed out) rehydrating,
  // do not persist an empty tray — that would wipe attachments that text
  // already restored via its store selector.
  const persistReadyRef = useRef(!opts.persistence);

  const runUpload = useCallback(async (localId: string, file: File) => {
    try {
      const result = await uploadRef.current(file);
      if (!liveIdsRef.current.has(localId)) return;
      if (!result?.id) {
        // Leave errorMessage unset so ComposerAttachmentTray can show the
        // localized tray_upload_failed string — do not hardcode English
        // "Upload failed" (that was the only signal in LRM-426 screenshots).
        setPending((prev) =>
          prev.map((item) =>
            item.localId === localId
              ? {
                  ...item,
                  status: "error" as const,
                  errorMessage: undefined,
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
      // Prefer the thrown message (API/CSRF/size/schema). Empty → tray i18n.
      const raw = err instanceof Error ? err.message.trim() : "";
      const message = raw.length > 0 ? raw : undefined;
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

  const applyRestored = useCallback((items: ComposerDraftAttachment[]) => {
    setPending((prev) => {
      // User already has tray items (typed add / prior restore) — keep them.
      if (prev.length > 0) return prev;
      const restored = deserializeFromDraft(items);
      for (const item of restored) liveIdsRef.current.add(item.localId);
      return restored;
    });
  }, []);

  // Own the conversation-switch reset inside the hook so parents do not
  // bounce clear() through effects (react-doctor no-pass-data-to-parent).
  // With persistence attached, a switch restores that conversation's draft
  // tray instead of dropping it (LRM-801).
  useEffect(() => {
    if (hydratedKeyRef.current === opts.resetKey && !persistenceRef.current) {
      clear();
      return;
    }
    if (hydratedKeyRef.current === opts.resetKey) return;
    hydratedKeyRef.current = opts.resetKey;
    persistReadyRef.current = !persistenceRef.current;
    setPending((prev) => {
      for (const item of prev) {
        liveIdsRef.current.delete(item.localId);
        revokePreviewUrl(item.previewUrl);
      }
      const restored = deserializeFromDraft(persistenceRef.current?.load() ?? []);
      for (const item of restored) liveIdsRef.current.add(item.localId);
      return restored;
    });
    if (!persistenceRef.current) persistReadyRef.current = true;
  }, [opts.resetKey, clear]);

  // Late restore after zustand persist rehydration. When the parent passes
  // `hydrateSignal`, "" means "store still rehydrating" — hold persistReady
  // so save([]) cannot wipe attachments. Unit tests omit the signal and keep
  // the sync load()/save() behavior.
  useEffect(() => {
    const persistence = persistenceRef.current;
    if (!persistence) {
      persistReadyRef.current = true;
      return;
    }
    if (hydratedKeyRef.current !== opts.resetKey) return;
    const signal = persistence.hydrateSignal;
    if (signal === "") return;
    const items = persistence.load() ?? [];
    if (items.length > 0) applyRestored(items);
    persistReadyRef.current = true;
  }, [opts.resetKey, opts.persistence?.hydrateSignal, applyRestored]);

  // Persist every tray change into the draft (debounced at the storage layer).
  useEffect(() => {
    const persistence = persistenceRef.current;
    if (!persistence) return;
    if (hydratedKeyRef.current !== opts.resetKey) return;
    if (!persistReadyRef.current) return;
    persistence.save(serializeForDraft(pending));
  }, [pending, opts.resetKey]);

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
