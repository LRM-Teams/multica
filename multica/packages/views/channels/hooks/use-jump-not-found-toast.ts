"use client";

import { useEffect, useRef } from "react";
import { showErrorToast } from "@multica/ui/lib/error-toast";

/**
 * LRM-736 AC — when a deep-link / jump target is gone (deleted or never in
 * history), announce with `showErrorToast`. Call sites keep their durable
 * inline notice (#835: toast is the announcement, not the only record).
 *
 * Toasts at most once per `targetId` so remounts / re-renders do not spam.
 */
export function useJumpNotFoundToast(params: {
  missing: boolean;
  targetId: string | null | undefined;
  message: string;
}): void {
  const { missing, targetId, message } = params;
  const toastedForRef = useRef<string | null>(null);

  useEffect(() => {
    if (!targetId) {
      toastedForRef.current = null;
      return;
    }
    if (!missing) return;
    if (toastedForRef.current === targetId) return;
    toastedForRef.current = targetId;
    showErrorToast(message);
  }, [missing, targetId, message]);
}
