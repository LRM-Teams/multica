"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
import type { RuntimeUpdateStatus } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n/use-t";

interface RuntimeUpdateDialogProps {
  wsId: string | undefined;
}

export function RuntimeUpdateDialog({ wsId }: RuntimeUpdateDialogProps) {
  const { t } = useT("runtimes");
  const userId = useAuthStore((s) => s.user?.id);
  const qc = useQueryClient();
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const [dismissedKey, setDismissedKey] = useState<string | null>(null);
  const [dismissedHydrated, setDismissedHydrated] = useState(false);
  const [activeRuntimeId, setActiveRuntimeId] = useState<string | null>(null);
  const [status, setStatus] = useState<RuntimeUpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [output, setOutput] = useState("");
  const [starting, setStarting] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const updatableRuntimes = useMemo(() => {
    if (!userId) return [];
    return runtimes.filter((runtime) => runtimeCanStartSelfUpdate(runtime, userId));
  }, [runtimes, userId]);

  const promptKey = useMemo(() => {
    if (updatableRuntimes.length === 0) return null;
    return updatableRuntimes
      .map((runtime) => `${runtime.id}:${runtimeTargetVersion(runtime) ?? ""}`)
      .sort()
      .join(",");
  }, [updatableRuntimes]);
  const dismissStorageKey = useMemo(() => {
    if (!wsId || !userId) return null;
    return `multica_runtime_update_prompt:${wsId}:${userId}`;
  }, [userId, wsId]);

  const activeRuntime = useMemo(
    () =>
      updatableRuntimes.find((runtime) => runtime.id === activeRuntimeId) ??
      updatableRuntimes[0] ??
      null,
    [activeRuntimeId, updatableRuntimes],
  );

  const open = !!promptKey && dismissedHydrated && dismissedKey !== promptKey;
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

  const cleanup = useCallback(() => {
    cleanupUpdatePoll();
  }, [cleanupUpdatePoll]);

  useEffect(() => cleanup, [cleanup]);

  useEffect(() => {
    setDismissedHydrated(false);
    setDismissedKey(null);
    if (!dismissStorageKey || typeof window === "undefined") {
      setDismissedHydrated(true);
      return;
    }
    setDismissedKey(window.localStorage.getItem(dismissStorageKey));
    setDismissedHydrated(true);
  }, [dismissStorageKey]);

  useEffect(() => {
    if (!open) {
      cleanup();
      setActiveRuntimeId(null);
      setStatus(null);
      setError("");
      setOutput("");
      setStarting(false);
    }
  }, [cleanup, open]);

  useEffect(() => {
    if (activeRuntimeId && updatableRuntimes.some((r) => r.id === activeRuntimeId)) {
      return;
    }
    setActiveRuntimeId(updatableRuntimes[0]?.id ?? null);
  }, [activeRuntimeId, updatableRuntimes]);

  const dismiss = () => {
    if (!promptKey) return;
    setDismissedKey(promptKey);
    if (dismissStorageKey && typeof window !== "undefined") {
      window.localStorage.setItem(dismissStorageKey, promptKey);
    }
  };

  const refreshRuntimes = useCallback(() => {
    if (!wsId) return;
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
  }, [qc, wsId]);

  const pollUpdate = useCallback(
    (runtimeId: string, nextUpdateId: string) => {
      cleanupUpdatePoll();
      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtimeId, nextUpdateId);
          setStatus(result.status);
          if (result.status === "completed") {
            setOutput(result.output ?? t(($) => $.update_prompt.status.completed));
            cleanupUpdatePoll();
            setStarting(false);
            refreshRuntimes();
          } else if (result.status === "failed" || result.status === "timeout") {
            setError(result.error ?? t(($) => $.update.unknown_error));
            cleanupUpdatePoll();
            setStarting(false);
          }
        } catch {
          // Keep polling through transient network or restart gaps.
        }
      }, 2000);
    },
    [cleanupUpdatePoll, refreshRuntimes, t],
  );

  const startUpdate = async () => {
    if (!activeRuntime || !activeTargetVersion) return;
    cleanup();
    setStarting(true);
    setStatus("pending");
    setError("");
    setOutput("");
    try {
      const update = await api.initiateUpdate(activeRuntime.id, activeTargetVersion);
      setStatus(update.status);
      pollUpdate(activeRuntime.id, update.id);
    } catch (err) {
      setStatus("failed");
      setError(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.update.initiate_failed),
      );
    } finally {
      setStarting(false);
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

        {updatableRuntimes.length > 1 && (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.update_prompt.more_runtimes, {
              count: updatableRuntimes.length - 1,
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
