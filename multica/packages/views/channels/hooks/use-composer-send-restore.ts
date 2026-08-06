import { useCallback, useRef, useState } from "react";
import type { ComposerSendErrorState } from "../components/composer-send-error-bar";

/**
 * #772 — one composer's send-failure → restore state, extracted from the large
 * conversation surfaces so they don't accumulate a pile of related `useState`
 * calls (react-doctor `prefer-useReducer`).
 *
 * On a failed send the attempted text is put back into the composer — unless the
 * composer already holds DIFFERENT new text, in which case the failed text is
 * kept back and offered via `restorePrevious` (Iris edge case: never auto-cover
 * the user's new input). An inline error bar is shown either way. `nonce` is
 * bumped into the editor `key` to force a remount that re-reads the restored
 * text, because `ContentEditor` reads `defaultValue` only on mount.
 *
 * Pass `persist` for surfaces backed by a persistent draft store (the main
 * composer restores by writing the draft the remounted editor reads). Omit it
 * for surfaces with no persistent draft (thread composers): the restored text is
 * held in `restoreText` and fed to the editor's `defaultValue` directly.
 */
export function useComposerSendRestore(persist?: (text: string) => void) {
  const [error, setError] = useState<ComposerSendErrorState | null>(null);
  const [nonce, setNonce] = useState(0);
  const [restoreText, setRestoreText] = useState("");
  const failedContentRef = useRef("");
  // Held in a ref so the returned callbacks stay stable across renders even when
  // the caller passes a fresh `persist` closure each render.
  const persistRef = useRef(persist);
  persistRef.current = persist;

  const putBack = useCallback((text: string) => {
    if (persistRef.current) persistRef.current(text);
    else setRestoreText(text);
    setNonce((n) => n + 1);
  }, []);

  /**
   * Call from the send's `onVisibleError`. `attempted` is the text that failed
   * to send; `current` is the composer's live text at failure time.
   */
  const onFailed = useCallback(
    (attempted: string, current: string, tooLong = false) => {
      const conflicted = current.length > 0 && current !== attempted;
      failedContentRef.current = attempted;
      if (!conflicted) putBack(attempted);
      // #1276 413 fast-follow: a too-large payload surfaces a shorten-and-retry
      // message with no plain Retry (a raw retry just 413s again).
      setError({ conflicted, tooLong });
    },
    [putBack],
  );

  /** Bring the kept-back failed text into the composer (conflicted case). */
  const restorePrevious = useCallback(() => {
    putBack(failedContentRef.current);
    setError(null);
  }, [putBack]);

  /** Clear the error bar (a send committed). */
  const clear = useCallback(() => setError(null), []);

  /** Clear the error bar and any pending restore text (a new send dispatched). */
  const reset = useCallback(() => {
    setError(null);
    setRestoreText("");
  }, []);

  return { error, nonce, restoreText, onFailed, restorePrevious, clear, reset };
}
