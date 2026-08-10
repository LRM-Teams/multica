"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  clearComputerUpdateDismiss,
  computerUpdateToastId,
  dismissComputerUpdate,
  isComputerUpdateDismissed,
  listComputerUpdateCandidates,
  type ComputerUpdateCandidate,
} from "@multica/core/runtimes";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import { createSafeId } from "@multica/core/utils";
import type { MachineUpgradePhase, RuntimeUpdateStatus } from "@multica/core/types";
import { useT } from "../../i18n";
import {
  ComputerUpdateToast,
  computerUpdateSuccessToastOptions,
  computerUpdateToastOptions,
  type ComputerUpdateToastPhase,
} from "./computer-update-toast";

type LocalPhase = {
  phase: Exclude<ComputerUpdateToastPhase, "prompt">;
  progress?: string | null;
  error?: string | null;
  targetVersion: string;
  machineTitle: string;
  daemonId: string;
  runtimeId: string;
};

function browserStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function phaseToUpdateStatus(
  phase: MachineUpgradePhase | string | undefined,
): RuntimeUpdateStatus | null {
  switch (phase) {
    case "queued":
    case "starting":
      return "queued";
    case "staging":
    case "verifying":
    case "handoff":
    case "converging":
    case "rollback_pending":
      return "running";
    case "completed":
      return "completed";
    case "failed":
    case "rolled_back":
    case "cancelled":
      return "failed";
    case "timeout":
      return "timeout";
    default:
      return null;
  }
}

function isTerminalMachineStatus(status: RuntimeUpdateStatus | null): boolean {
  return (
    status === "completed" || status === "failed" || status === "timeout"
  );
}

/**
 * Watches owned local runtimes and shows one sticky toast per machine that
 * can self-update. Mount once under the dashboard shell (web + desktop).
 */
export function ComputerUpdateToastListener() {
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id);
  const { t } = useT("layout");
  const qc = useQueryClient();
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  const [localByMachine, setLocalByMachine] = useState<
    Record<string, LocalPhase>
  >({});
  const [busyMachines, setBusyMachines] = useState<Record<string, boolean>>(
    {},
  );
  const pollTimers = useRef<Map<string, ReturnType<typeof setInterval>>>(
    new Map(),
  );
  const shownIds = useRef<Set<string>>(new Set());

  const candidates = useMemo(
    () => listComputerUpdateCandidates(runtimes, userId),
    [runtimes, userId],
  );

  const clearPoll = useCallback((machineKey: string) => {
    const timer = pollTimers.current.get(machineKey);
    if (timer) {
      clearInterval(timer);
      pollTimers.current.delete(machineKey);
    }
  }, []);

  useEffect(() => {
    return () => {
      for (const timer of pollTimers.current.values()) clearInterval(timer);
      pollTimers.current.clear();
    };
  }, []);

  const refreshRuntimes = useCallback(() => {
    qc.invalidateQueries({
      predicate: (query) => query.queryKey[0] === "runtimes",
    });
  }, [qc]);

  const dismissLater = useCallback(
    (candidate: Pick<
      ComputerUpdateCandidate,
      "machineKey" | "targetVersion"
    >) => {
      if (!wsId) return;
      const storage = browserStorage();
      if (storage) {
        dismissComputerUpdate(
          storage,
          wsId,
          candidate.machineKey,
          candidate.targetVersion,
        );
      }
      clearPoll(candidate.machineKey);
      setLocalByMachine((prev) => {
        if (!(candidate.machineKey in prev)) return prev;
        const next = { ...prev };
        delete next[candidate.machineKey];
        return next;
      });
      toast.dismiss(computerUpdateToastId(candidate.machineKey));
      shownIds.current.delete(computerUpdateToastId(candidate.machineKey));
    },
    [clearPoll, wsId],
  );

  const startUpdate = useCallback(
    async (candidate: ComputerUpdateCandidate) => {
      if (!wsId) return;
      const toastId = computerUpdateToastId(candidate.machineKey);
      clearPoll(candidate.machineKey);
      setBusyMachines((prev) => ({ ...prev, [candidate.machineKey]: true }));
      setLocalByMachine((prev) => ({
        ...prev,
        [candidate.machineKey]: {
          phase: "updating",
          progress: t(($) => $.computer_update.progress_pending),
          targetVersion: candidate.targetVersion,
          machineTitle: candidate.machineTitle,
          daemonId: candidate.daemonId,
          runtimeId: candidate.runtimeId,
        },
      }));

      try {
        const update = await api.initiateMachineUpgrade(
          candidate.daemonId,
          candidate.targetVersion,
          createSafeId(),
        );
        if (browserStorage()) {
          clearComputerUpdateDismiss(
            browserStorage()!,
            wsId,
            candidate.machineKey,
          );
        }

        const timer = setInterval(async () => {
          try {
            const result = await api.getMachineUpgrade(
              candidate.daemonId,
              update.id,
            );
            const status = phaseToUpdateStatus(result.phase);
            if (status === "queued") {
              setLocalByMachine((prev) => ({
                ...prev,
                [candidate.machineKey]: {
                  phase: "updating",
                  progress: t(($) => $.computer_update.progress_pending),
                  targetVersion: candidate.targetVersion,
                  machineTitle: candidate.machineTitle,
                  daemonId: candidate.daemonId,
                  runtimeId: candidate.runtimeId,
                },
              }));
              return;
            }
            if (status === "running") {
              setLocalByMachine((prev) => ({
                ...prev,
                [candidate.machineKey]: {
                  phase: "updating",
                  progress: t(($) => $.computer_update.progress_running),
                  targetVersion: candidate.targetVersion,
                  machineTitle: candidate.machineTitle,
                  daemonId: candidate.daemonId,
                  runtimeId: candidate.runtimeId,
                },
              }));
              return;
            }
            if (!isTerminalMachineStatus(status)) return;

            clearPoll(candidate.machineKey);
            setBusyMachines((prev) => {
              const next = { ...prev };
              delete next[candidate.machineKey];
              return next;
            });
            refreshRuntimes();

            if (status === "failed" || status === "timeout") {
              setLocalByMachine((prev) => ({
                ...prev,
                [candidate.machineKey]: {
                  phase: "failed",
                  error:
                    result.error_message?.trim() ||
                    t(($) => $.computer_update.failed_generic),
                  targetVersion: candidate.targetVersion,
                  machineTitle: candidate.machineTitle,
                  daemonId: candidate.daemonId,
                  runtimeId: candidate.runtimeId,
                },
              }));
              return;
            }

            // completed — silence re-prompt for this target until a newer one
            const storage = browserStorage();
            if (storage && wsId) {
              dismissComputerUpdate(
                storage,
                wsId,
                candidate.machineKey,
                candidate.targetVersion,
              );
            }
            setLocalByMachine((prev) => {
              const next = { ...prev };
              delete next[candidate.machineKey];
              return next;
            });
            toast.custom(
              (id) => (
                <ComputerUpdateToast
                  phase="success"
                  title={t(($) => $.computer_update.success_title, {
                    name: candidate.machineTitle,
                  })}
                  versionLine={t(($) => $.computer_update.success_body, {
                    version: candidate.targetVersion,
                  })}
                  updateLabel={t(($) => $.computer_update.update)}
                  laterLabel={t(($) => $.computer_update.later)}
                  retryLabel={t(($) => $.computer_update.retry)}
                  dismissLabel={t(($) => $.computer_update.dismiss)}
                  onDismiss={() => toast.dismiss(id)}
                />
              ),
              {
                ...computerUpdateSuccessToastOptions,
                id: toastId,
              },
            );
            shownIds.current.delete(toastId);
          } catch {
            // ignore poll errors; next tick retries
          }
        }, 2000);
        pollTimers.current.set(candidate.machineKey, timer);
      } catch (err) {
        setBusyMachines((prev) => {
          const next = { ...prev };
          delete next[candidate.machineKey];
          return next;
        });
        let message = t(($) => $.computer_update.failed_generic);
        if (
          err instanceof ApiError &&
          err.status === 409 &&
          err.body &&
          typeof err.body === "object" &&
          (err.body as Record<string, unknown>).code === "runtime_pinned"
        ) {
          message = t(($) => $.computer_update.pinned);
        } else if (err instanceof Error && err.message.trim()) {
          message = err.message;
        }
        setLocalByMachine((prev) => ({
          ...prev,
          [candidate.machineKey]: {
            phase: "failed",
            error: message,
            targetVersion: candidate.targetVersion,
            machineTitle: candidate.machineTitle,
            daemonId: candidate.daemonId,
            runtimeId: candidate.runtimeId,
          },
        }));
      }
    },
    [clearPoll, refreshRuntimes, t, wsId],
  );

  // Sync sticky toasts with eligible candidates + local phases.
  useEffect(() => {
    if (!wsId || !userId) return;
    const storage = browserStorage();
    const nextShown = new Set<string>();

    const renderToast = (
      machineKey: string,
      phase: ComputerUpdateToastPhase,
      opts: {
        title: string;
        versionLine?: string | null;
        progressLabel?: string | null;
        errorLabel?: string | null;
        candidate?: ComputerUpdateCandidate;
        local?: LocalPhase;
      },
    ) => {
      const toastId = computerUpdateToastId(machineKey);
      nextShown.add(toastId);
      const busy = !!busyMachines[machineKey];
      const laterTarget =
        opts.candidate?.targetVersion ?? opts.local?.targetVersion;

      toast.custom(
        (id) => (
          <ComputerUpdateToast
            phase={phase}
            title={opts.title}
            versionLine={opts.versionLine}
            progressLabel={opts.progressLabel}
            errorLabel={opts.errorLabel}
            updateLabel={t(($) => $.computer_update.update)}
            laterLabel={t(($) => $.computer_update.later)}
            retryLabel={t(($) => $.computer_update.retry)}
            dismissLabel={t(($) => $.computer_update.dismiss)}
            busy={busy}
            onUpdate={
              opts.candidate
                ? () => {
                    void startUpdate(opts.candidate!);
                  }
                : undefined
            }
            onRetry={
              opts.local
                ? () => {
                    void startUpdate({
                      machineKey,
                      daemonId: opts.local!.daemonId,
                      runtimeId: opts.local!.runtimeId,
                      machineTitle: opts.local!.machineTitle,
                      currentVersion: null,
                      targetVersion: opts.local!.targetVersion,
                    });
                  }
                : undefined
            }
            onLater={
              laterTarget
                ? () => {
                    dismissLater({
                      machineKey,
                      targetVersion: laterTarget,
                    });
                  }
                : undefined
            }
            onDismiss={
              phase === "prompt" || phase === "failed"
                ? () => {
                    if (laterTarget) {
                      dismissLater({
                        machineKey,
                        targetVersion: laterTarget,
                      });
                    } else {
                      toast.dismiss(id);
                    }
                  }
                : phase === "success"
                  ? () => toast.dismiss(id)
                  : undefined
            }
          />
        ),
        {
          ...computerUpdateToastOptions,
          id: toastId,
          duration:
            phase === "success"
              ? computerUpdateSuccessToastOptions.duration
              : Infinity,
        },
      );
    };

    for (const [machineKey, local] of Object.entries(localByMachine)) {
      if (local.phase === "updating" || local.phase === "failed") {
        renderToast(machineKey, local.phase, {
          title:
            local.phase === "updating"
              ? t(($) => $.computer_update.updating_title, {
                  name: local.machineTitle,
                })
              : t(($) => $.computer_update.failed_title, {
                  name: local.machineTitle,
                }),
          progressLabel: local.progress,
          errorLabel: local.error,
          local,
        });
      }
    }

    for (const candidate of candidates) {
      if (localByMachine[candidate.machineKey]) continue;
      if (
        storage &&
        isComputerUpdateDismissed(
          storage,
          wsId,
          candidate.machineKey,
          candidate.targetVersion,
        )
      ) {
        continue;
      }
      const current =
        candidate.currentVersion ??
        t(($) => $.computer_update.version_unknown);
      renderToast(candidate.machineKey, "prompt", {
        title: t(($) => $.computer_update.prompt_title, {
          name: candidate.machineTitle,
        }),
        versionLine: t(($) => $.computer_update.version_line, {
          current,
          target: candidate.targetVersion,
        }),
        candidate,
      });
    }

    for (const id of shownIds.current) {
      if (!nextShown.has(id)) {
        toast.dismiss(id);
      }
    }
    shownIds.current = nextShown;
  }, [
    busyMachines,
    candidates,
    dismissLater,
    localByMachine,
    startUpdate,
    t,
    userId,
    wsId,
  ]);

  return null;
}
