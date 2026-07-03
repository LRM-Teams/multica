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
  const status = promptUpdateState.status;
  const error = promptUpdateState.error;
  const output = promptUpdateState.output;
  const starting = promptUpdateState.starting;
  const open = !!prompt;
  const isActive = status === "pending" || status === "running" || starting;
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

  const dismiss = useCallback(() => {
    if (!promptKey) return;
    rememberDismissedKey(promptKey);
  }, [promptKey, rememberDismissedKey]);

  const refreshRuntimes = useCallback(() => {
    if (!wsId) return;
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
  }, [qc, wsId]);

  const pollUpdate = useCallback(
    (key: string, runtimeId: string, nextUpdateId: string) => {
      cleanupUpdatePoll();
      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtimeId, nextUpdateId);
          setUpdateState((current) => ({
            ...current,
            promptKey: key,
            status: result.status,
          }));
          if (result.status === "completed") {
            setUpdateState((current) => ({
              ...current,
              promptKey: key,
              status: result.status,
              output:
                result.output ?? t(($) => $.update_prompt.status.completed),
              starting: false,
            }));
            cleanupUpdatePoll();
            refreshRuntimes();
          } else if (result.status === "failed" || result.status === "timeout") {
            setUpdateState((current) => ({
              ...current,
              promptKey: key,
              status: result.status,
              error: result.error ?? t(($) => $.update.unknown_error),
              starting: false,
            }));
            cleanupUpdatePoll();
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
      // The operation has started; keep subsequent status in AppShell/Runtimes
      // instead of reopening this blocking prompt for the same daemon+target.
      rememberDismissedKey(promptKey);
      setUpdateState({
        promptKey,
        status: update.status,
        error: "",
        output: "",
        starting: false,
      });
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

        {status === "completed" && (
          <p className="text-xs leading-relaxed text-success">
            {output || t(($) => $.update_prompt.status.completed)}
          </p>
        )}
        {(status === "failed" || status === "timeout") && (
          <p className="text-xs leading-relaxed text-destructive">
            {error || t(($) => $.update.status[status])}
          </p>
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={dismiss} disabled={isActive}>
            {t(($) => $.update_prompt.later)}
          </Button>
          <Button onClick={startUpdate} disabled={isActive || status === "completed"}>
            {isActive && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {isActive
              ? t(($) => $.update.status.running)
              : status === "failed" || status === "timeout"
              ? t(($) => $.update.retry)
              : t(($) => $.update_prompt.update_now)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
