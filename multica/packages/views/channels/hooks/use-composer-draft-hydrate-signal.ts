"use client";

import { useEffect, useState } from "react";
import { useComposerDraftStore } from "@multica/core/channels";

/**
 * LRM-801 — signal for tray restore after zustand persist rehydration.
 *
 * - `undefined` when there is no draft key (caller should omit persistence)
 * - `""` while the draft store is still rehydrating (tray must not save yet)
 * - attachment signature once ready (empty string signature `""` is reserved
 *   for "not ready"; ready-with-no-attachments uses `"0"` so the tray can
 *   unblock saves after a confirmed empty draft)
 */
export function useComposerDraftHydrateSignal(
  draftKey: string | null | undefined,
): string | undefined {
  const [ready, setReady] = useState(() => useComposerDraftStore.persist.hasHydrated());
  const attachments = useComposerDraftStore((s) =>
    draftKey ? s.drafts[draftKey]?.attachments : undefined,
  );

  useEffect(() => {
    if (ready) return;
    const unsub = useComposerDraftStore.persist.onFinishHydration(() => {
      setReady(true);
    });
    if (useComposerDraftStore.persist.hasHydrated()) setReady(true);
    return unsub;
  }, [ready]);

  if (!draftKey) return undefined;
  if (!ready) return "";
  if (!attachments || attachments.length === 0) return "0";
  return attachments
    .map((a) => a.attachmentId ?? `f:${a.filename}:${a.sizeBytes}`)
    .join("|");
}
