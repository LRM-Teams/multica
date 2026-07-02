"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpCircle, CheckCircle2, Loader2, XCircle } from "lucide-react";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { runtimeKeys, runtimeListOptions, latestCliVersionOptions } from "@multica/core/runtimes/queries";
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
import { isVersionNewer } from "../utils";

const statusIcon: Record<RuntimeUpdateStatus, typeof Loader2> = {
  pending: Loader2,
  running: Loader2,
  completed: CheckCircle2,
  failed: XCircle,
  timeout: XCircle,
};

function runtimeCliVersion(runtime: AgentRuntime): string | null {
  const version = runtime.metadata?.cli_version;
  return typeof version === "string" && version ? version : null;
}

function runtimeLaunchedBy(runtime: AgentRuntime): string | null {
  const launchedBy = runtime.metadata?.launched_by;
  return typeof launchedBy === "string" && launchedBy ? launchedBy : null;
}

function isUpdatableRuntime(
  runtime: AgentRuntime,
  latestVersion: string,
  userId: string,
): boolean {
  if (runtime.runtime_mode !== "local") return false;
  if (runtime.status !== "online") return false;
  if (runtime.owner_id !== userId) return false;
  if (runtimeLaunchedBy(runtime) === "desktop") return false;
  const cliVersion = runtimeCliVersion(runtime);
  return !!cliVersion && isVersionNewer(latestVersion, cliVersion);
}

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
  const { data: latestVersion } = useQuery(latestCliVersionOptions());
  const [dismissedKey, setDismissedKey] = useState<string | null>(null);
  const [dismissedHydrated, setDismissedHydrated] = useState(false);
  const [activeRuntimeId, setActiveRuntimeId] = useState<string | null>(null);
  const [status, setStatus] = useState<RuntimeUpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [output, setOutput] = useState("");
  const [starting, setStarting] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const updatableRuntimes = useMemo(() => {
    if (!latestVersion || !userId) return [];
    return runtimes.filter((runtime) =>
      isUpdatableRuntime(runtime, latestVersion, userId),
    );
  }, [latestVersion, runtimes, userId]);

  const promptKey = useMemo(() => {
    if (!latestVersion || updatableRuntimes.length === 0) return null;
    return `${latestVersion}:${updatableRuntimes.map((r) => r.id).sort().join(",")}`;
  }, [latestVersion, updatableRuntimes]);
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
  const StatusIcon = status ? statusIcon[status] : ArrowUpCircle;

  const cleanup = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

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

  const pollUpdate = useCallback(
    (runtimeId: string, nextUpdateId: string) => {
      cleanup();
      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtimeId, nextUpdateId);
          setStatus(result.status);
          if (result.status === "completed") {
            setOutput(result.output ?? t(($) => $.update_prompt.status.completed));
            cleanup();
            setStarting(false);
            setTimeout(() => {
              qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId ?? "") });
            }, 3000);
          } else if (result.status === "failed" || result.status === "timeout") {
            setError(result.error ?? t(($) => $.update.unknown_error));
            cleanup();
            setStarting(false);
          }
        } catch {
          // Keep polling through transient network or restart gaps.
        }
      }, 2000);
    },
    [cleanup, qc, t, wsId],
  );

  const startUpdate = async () => {
    if (!activeRuntime || !latestVersion) return;
    cleanup();
    setStarting(true);
    setStatus("pending");
    setError("");
    setOutput("");
    try {
      const update = await api.initiateUpdate(activeRuntime.id, latestVersion);
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

  if (!activeRuntime || !latestVersion || !promptKey) return null;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !isActive && dismiss()}>
      <DialogContent className="sm:max-w-lg" showCloseButton={!isActive}>
        <div className="flex items-start gap-3">
          <div className="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-info/10 text-info">
            <StatusIcon
              className={`h-5 w-5 ${status === "pending" || status === "running" || starting ? "animate-spin" : ""}`}
            />
          </div>
          <div className="min-w-0 flex-1">
            <DialogTitle>{t(($) => $.update_prompt.title)}</DialogTitle>
            <DialogDescription className="mt-1 leading-relaxed">
              {t(($) => $.update_prompt.description, {
                current: runtimeCliVersion(activeRuntime) ?? t(($) => $.update.version_unknown),
                latest: latestVersion,
              })}
            </DialogDescription>
          </div>
        </div>

        <div className="rounded-lg border bg-muted/30 p-3 text-sm">
          <div className="flex items-center justify-between gap-3">
            <span className="min-w-0 truncate font-medium">{activeRuntime.name}</span>
            <span className="shrink-0 rounded bg-background px-2 py-1 font-mono text-xs">
              {runtimeCliVersion(activeRuntime) ?? "?"} → {latestVersion}
            </span>
          </div>
          {updatableRuntimes.length > 1 && (
            <p className="mt-2 text-xs text-muted-foreground">
              {t(($) => $.update_prompt.more_runtimes, {
                count: updatableRuntimes.length - 1,
              })}
            </p>
          )}
        </div>

        {status && (
          <div className="rounded-lg border px-3 py-2 text-sm">
            <p className="font-medium">{t(($) => $.update.status[status])}</p>
            {output && <p className="mt-1 text-xs text-success">{output}</p>}
            {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
          </div>
        )}

        <p className="text-xs leading-relaxed text-muted-foreground">
          {t(($) => $.update_prompt.note)}
        </p>

        <DialogFooter>
          <Button variant="ghost" onClick={dismiss} disabled={isActive}>
            {t(($) => $.update_prompt.later)}
          </Button>
          <Button onClick={startUpdate} disabled={isActive || status === "completed"}>
            {isActive && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {status === "failed" || status === "timeout"
              ? t(($) => $.update.retry)
              : t(($) => $.update_prompt.update_now)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
