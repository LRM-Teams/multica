"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import {
  deriveUpdateStatus,
  isTerminalUpdateStatus,
  runtimeCanStartSelfUpdate,
  runtimeCurrentVersion,
  runtimeTargetVersion,
} from "@multica/core/runtimes";
import { runtimeKeys, runtimeListOptions } from "@multica/core/runtimes/queries";
import type { AgentRuntime, RuntimeUpdateStatus } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n/use-t";
import {
  parseDismissedPromptKeys,
  runtimeUpdatePrompts,
  serializeDismissedPromptKeys,
} from "./runtime-update-prompt";

interface RuntimeUpdateDialogProps {
  wsId: string | undefined;
}

interface PromptUpdateState {
  promptKey: string | null;
  status: RuntimeUpdateStatus | null;
  error: string;
  output: string;
  starting: boolean;
}

const DISMISSED_PROMPT_KEYS_EVENT = "multica:runtime-update-prompts-dismissed";
const EMPTY_RUNTIMES: AgentRuntime[] = [];
const EMPTY_PROMPT_UPDATE_STATE: PromptUpdateState = {
  promptKey: null,
  status: null,
  error: "",
  output: "",
  starting: false,
};

function dismissedPromptStorageSnapshot(storageKey: string | null): string {
  if (!storageKey || typeof window === "undefined") return "";
  return window.localStorage.getItem(storageKey) ?? "";
}

function subscribeDismissedPromptStorage(
  storageKey: string | null,
  onStoreChange: () => void,
): () => void {
  if (!storageKey || typeof window === "undefined") return () => {};

  const onStorage = (event: StorageEvent) => {
    if (event.key === storageKey) onStoreChange();
  };
  window.addEventListener("storage", onStorage);
  window.addEventListener(DISMISSED_PROMPT_KEYS_EVENT, onStoreChange);
  return () => {
    window.removeEventListener("storage", onStorage);
    window.removeEventListener(DISMISSED_PROMPT_KEYS_EVENT, onStoreChange);
  };
}

export function RuntimeUpdateDialog({ wsId }: RuntimeUpdateDialogProps) {
  const { t } = useT("runtimes");
  const userId = useAuthStore((s) => s.user?.id);
  const qc = useQueryClient();
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const [updateState, setUpdateState] = useState<PromptUpdateState>(
    EMPTY_PROMPT_UPDATE_STATE,
  );
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const updatableRuntimes = useMemo(() => {
    if (!userId) return [];
    return runtimes.filter((runtime) => runtimeCanStartSelfUpdate(runtime, userId));
  }, [runtimes, userId]);

  const prompts = useMemo(
    () => runtimeUpdatePrompts(updatableRuntimes),
    [updatableRuntimes],
  );
  const dismissStorageKey = useMemo(() => {
    if (!wsId || !userId) return null;
    return `multica_runtime_update_prompt:${wsId}:${userId}`;
  }, [userId, wsId]);
  const dismissedRaw = useSyncExternalStore(
    useCallback(
      (onStoreChange) =>
        subscribeDismissedPromptStorage(dismissStorageKey, onStoreChange),
      [dismissStorageKey],
    ),
    useCallback(
      () => dismissedPromptStorageSnapshot(dismissStorageKey),
      [dismissStorageKey],
    ),
    () => "",
  );
  const dismissedKeys = useMemo(
    () => parseDismissedPromptKeys(dismissedRaw),
    [dismissedRaw],
  );
  const prompt = useMemo(
    () => prompts.find((item) => !dismissedKeys.has(item.key)) ?? null,
    [dismissedKeys, prompts],
  );
  const promptKey = prompt?.key ?? null;
  const promptRuntimes = prompt?.runtimes ?? EMPTY_RUNTIMES;

  const activeRuntime = promptRuntimes[0] ?? null;

  const promptUpdateState =
    updateState.promptKey === promptKey
      ? updateState
      : EMPTY_PROMPT_UPDATE_STATE;
  const pollStatus = promptUpdateState.status;
  const error = promptUpdateState.error;
  const starting = promptUpdateState.starting;
  const open = !!prompt;
  // Single display status: an in-flight poll wins, otherwise derive from the
  // runtime projection so a daemon already downloading/staged reads correctly
  // without waiting for the first poll tick (the "clicked, nothing happened" gap).
  const status = deriveUpdateStatus({
    pollStatus,
    updateState: activeRuntime?.update_state,
    runtimeHealth: activeRuntime?.runtime_health,
  });
  const isActive = status === "pending" || status === "running" || starting;
  const isTerminalSuccess = status === "completed" || status === "ready_to_apply";
  const isTerminalError = status === "failed" || status === "timeout";
  const activeTargetVersion = activeRuntime
    ? runtimeTargetVersion(activeRuntime)
    : null;

  const cleanupUpdatePoll = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => cleanupUpdatePoll, [cleanupUpdatePoll]);

  const rememberDismissedKey = useCallback((key: string) => {
    if (!dismissStorageKey || typeof window === "undefined") return;
    const next = parseDismissedPromptKeys(
      window.localStorage.getItem(dismissStorageKey),
    );
    next.add(key);
    window.localStorage.setItem(
      dismissStorageKey,
      serializeDismissedPromptKeys(next),
    );
    window.dispatchEvent(new Event(DISMISSED_PROMPT_KEYS_EVENT));
  }, [dismissStorageKey]);

  const refreshRuntimes = useCallback(() => {
    if (!wsId) return;
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
  }, [qc, wsId]);

  const dismiss = useCallback(() => {
    // "Later" is the only user action that remembers a dismissed key; it just
    // defers, so there is nothing to refresh. Any in-flight poll is cleaned up.
    if (promptKey) rememberDismissedKey(promptKey);
    cleanupUpdatePoll();
    setUpdateState(EMPTY_PROMPT_UPDATE_STATE);
  }, [promptKey, rememberDismissedKey, cleanupUpdatePoll]);

  const pollUpdate = useCallback(
    (key: string, runtimeId: string, nextUpdateId: string) => {
      cleanupUpdatePoll();
      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtimeId, nextUpdateId);
          const staged =
            result.status === "completed" || result.status === "ready_to_apply";
          const errored =
            result.status === "failed" || result.status === "timeout";
          setUpdateState((current) => ({
            ...current,
            promptKey: key,
            status: result.status,
            starting: false,
            output: staged
              ? result.output ?? t(($) => $.update.status[result.status])
              : current.output,
            error: errored
              ? result.error ?? t(($) => $.update.unknown_error)
              : current.error,
          }));
          // `ready_to_apply` is terminal (staged, applies when idle) — stop here
          // instead of polling forever, and refresh so the global surfaces reflect
          // the final health/update_state. The prompt itself has already handed off.
          if (isTerminalUpdateStatus(result.status)) {
            cleanupUpdatePoll();
            refreshRuntimes();
          }
        } catch {
          // Keep polling through transient network or restart gaps.
        }
      }, 2000);
    },
    [cleanupUpdatePoll, refreshRuntimes, t],
  );

  const startUpdate = async () => {
    if (!activeRuntime || !activeTargetVersion || !promptKey) return;
    cleanupUpdatePoll();
    setUpdateState({
      promptKey,
      status: "pending",
      error: "",
      output: "",
      starting: true,
    });
    try {
      const update = await api.initiateUpdate(activeRuntime.id, activeTargetVersion);
      setUpdateState({
        promptKey,
        status: update.status,
        error: "",
        output: "",
        starting: false,
      });
      // Natural handoff: refresh the projection now so health flips to "updating".
      // That drops the runtime from `runtimeCanStartSelfUpdate` eligibility, so the
      // prompt self-dismisses and the global surfaces (AppShell / sidebar / Runtimes)
      // take over showing progress — we never pin a modal over a multi-hour drain.
      // We deliberately do NOT remember a dismissed key here; only "Later" does.
      refreshRuntimes();
      pollUpdate(promptKey, activeRuntime.id, update.id);
    } catch (err) {
      setUpdateState({
        promptKey,
        status: "failed",
        error:
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.update.initiate_failed),
        output: "",
        starting: false,
      });
    }
  };

  if (!activeRuntime || !activeTargetVersion || !promptKey) return null;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !isActive && dismiss()}>
      <DialogContent className="w-[calc(100vw-2rem)] sm:max-w-md" showCloseButton={!isActive}>
        <div>
          <DialogTitle>{t(($) => $.update_prompt.title)}</DialogTitle>
          <DialogDescription className="mt-1">
            {t(($) => $.update_prompt.description, {
              current: runtimeCurrentVersion(activeRuntime) ?? t(($) => $.update.version_unknown),
              latest: activeTargetVersion,
            })}
          </DialogDescription>
        </div>

        <div className="rounded-md bg-muted/40 px-3 py-2 text-sm">
          <div className="flex items-center justify-between gap-3">
            <span className="min-w-0 truncate font-medium">{activeRuntime.name}</span>
            <span className="shrink-0 font-mono text-xs text-muted-foreground">
              {runtimeCurrentVersion(activeRuntime) ?? "?"} → {activeTargetVersion}
            </span>
          </div>
        </div>

        {promptRuntimes.length > 1 && (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.update_prompt.more_runtimes, {
              count: promptRuntimes.length - 1,
            })}
          </p>
        )}

        {/* Brief, in-place feedback so the click is never a black window. In the
            normal path the projection flips to "updating" and the prompt hands off
            to the global surfaces before any terminal shows. The ready/completed
            branches are a stale-projection fallback: if the poll reaches a terminal
            status while the runtime query still reports "update_available", we show
            the outcome here (existing copy) rather than silently reverting to
            "Update now". "Not now" is the only dismiss — we never pin a modal. */}
        <output aria-live="polite" className="block text-xs leading-relaxed empty:hidden">
          {isActive && (
            <span className="inline-flex items-center gap-1.5 text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              {t(($) => $.update.status[status === "pending" ? "pending" : "running"])}
            </span>
          )}
          {status === "ready_to_apply" && (
            <span className="text-warning">
              {t(($) => $.update.status.ready_to_apply)}
            </span>
          )}
          {status === "completed" && (
            <span className="text-success">
              {t(($) => $.update.status.completed)}
            </span>
          )}
          {isTerminalError && (
            <span className="text-destructive">
              {error || t(($) => $.update.status[status])}
            </span>
          )}
        </output>

        <DialogFooter>
          <Button variant="ghost" onClick={dismiss} disabled={isActive}>
            {t(($) => $.update_prompt.later)}
          </Button>
          {/* No action button at terminal success — the outcome is shown and the
              only thing left to do is dismiss. Never revert to "Update now" (that
              would invite a pointless re-click on an already-staged update). */}
          {!isTerminalSuccess && (
            <Button onClick={startUpdate} disabled={isActive}>
              {isActive && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {isActive
                ? t(($) => $.update.status.running)
                : isTerminalError
                ? t(($) => $.update.retry)
                : t(($) => $.update_prompt.update_now)}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
