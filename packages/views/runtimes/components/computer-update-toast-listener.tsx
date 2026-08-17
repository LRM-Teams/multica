"use client";

import { useCallback, useEffect, useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@multica/core/api";
import { useWSEvent } from "@multica/core/realtime";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  clearComputerUpdateDismiss,
  computerUpdateMachineKey,
  computerUpdateCandidatesFingerprint,
  computerUpdateToastContentKey,
  computerUpdateToastId,
  dismissComputerUpdate,
  isNewerCliVersion,
  isComputerUpdateDismissed,
  listComputerUpdateCandidates,
  runtimeCurrentVersion,
  type ComputerUpdateCandidate,
} from "@multica/core/runtimes";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import { createSafeId } from "@multica/core/utils";
import type {
  ComputerUpgradeDonePayload,
  ComputerUpgradeProgressPayload,
} from "@multica/core/types";
import { useT } from "../../i18n";
import {
  ComputerUpdateToast,
  type ComputerUpdateToastPhase,
} from "./computer-update-toast";
import {
  computerUpdateSuccessToastOptions,
  computerUpdateToastOptions,
} from "./computer-update-toast-options";

type LocalPhase = {
  phase: Exclude<ComputerUpdateToastPhase, "prompt">;
  progress?: string | null;
  error?: string | null;
  targetVersion: string;
  machineTitle: string;
  daemonId: string;
  runtimeId: string;
};

type ToastCopy = {
  update: string;
  later: string;
  retry: string;
  dismiss: string;
  versionUnknown: string;
  progressPending: string;
  progressRunning: string;
  progressDownloading: string;
  progressVerifying: string;
  progressApplying: string;
  progressRestarting: string;
  failedGeneric: string;
  pinned: string;
  promptTitle: (name: string) => string;
  versionLine: (current: string, target: string) => string;
  updatingTitle: (name: string) => string;
  failedTitle: (name: string) => string;
  successTitle: (name: string) => string;
  successBody: (version: string) => string;
  rolledBack: (version: string) => string;
};

function browserStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function getOrCreateMap<K, V>(
  ref: { current: Map<K, V> | null },
): Map<K, V> {
  if (!ref.current) ref.current = new Map();
  return ref.current;
}

/**
 * Watches owned local runtimes and shows one sticky toast per machine that
 * can self-update. Mount once under the dashboard shell (web + desktop).
 *
 * Performance notes:
 * - In-flight phase lives in refs (no React state churn).
 * - Candidate-list identity churn is ignored via fingerprint.
 * - toast.custom is skipped when the content key for a toast id is unchanged
 *   (runtime list refresh while a sticky prompt is already correct).
 * - Upgrade poll only re-syncs when the polled status actually changes.
 */
export function ComputerUpdateToastListener() {
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id);
  const { t } = useT("layout");
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const runtimesRef = useRef(runtimes);
  runtimesRef.current = runtimes;

  const localByMachineRef = useRef<Map<string, LocalPhase> | null>(null);
  const requestByMachineRef = useRef<Map<string, {
    requestId: string;
    daemonId: string;
    targetVersion: string;
    machineTitle: string;
    runtimeId: string;
  }> | null>(null);
  /** toastId → last published content key (skip identical re-push). */
  const publishedContentRef = useRef<Map<string, string> | null>(null);
  const startUpdateRef = useRef<
    ((candidate: ComputerUpdateCandidate) => void) | null
  >(null);
  const dismissLaterRef = useRef<
    ((
      candidate: Pick<ComputerUpdateCandidate, "machineKey" | "targetVersion">,
    ) => void) | null
  >(null);

  const candidates = useMemo(
    () => listComputerUpdateCandidates(runtimes, userId),
    [runtimes, userId],
  );
  const candidatesFingerprint = useMemo(
    () => computerUpdateCandidatesFingerprint(candidates),
    [candidates],
  );
  const runtimeVersionsFingerprint = useMemo(() => {
    const rows = (runtimes ?? []).map(
      (runtime) =>
        `${computerUpdateMachineKey(runtime)}\0${runtimeCurrentVersion(runtime) ?? ""}`,
    );
    rows.sort();
    return rows.join("\n");
  }, [runtimes]);

  const copy = useMemo<ToastCopy>(
    () => ({
      update: t(($) => $.computer_update.update),
      later: t(($) => $.computer_update.later),
      retry: t(($) => $.computer_update.retry),
      dismiss: t(($) => $.computer_update.dismiss),
      versionUnknown: t(($) => $.computer_update.version_unknown),
      progressPending: t(($) => $.computer_update.progress_pending),
      progressRunning: t(($) => $.computer_update.progress_running),
      progressDownloading: t(($) => $.computer_update.progress_downloading),
      progressVerifying: t(($) => $.computer_update.progress_verifying),
      progressApplying: t(($) => $.computer_update.progress_applying),
      progressRestarting: t(($) => $.computer_update.progress_restarting),
      failedGeneric: t(($) => $.computer_update.failed_generic),
      pinned: t(($) => $.computer_update.pinned),
      promptTitle: (name) =>
        t(($) => $.computer_update.prompt_title, { name }),
      versionLine: (current, target) =>
        t(($) => $.computer_update.version_line, { current, target }),
      updatingTitle: (name) =>
        t(($) => $.computer_update.updating_title, { name }),
      failedTitle: (name) =>
        t(($) => $.computer_update.failed_title, { name }),
      successTitle: (name) =>
        t(($) => $.computer_update.success_title, { name }),
      successBody: (version) =>
        t(($) => $.computer_update.success_body, { version }),
      rolledBack: (version) =>
        t(($) => $.computer_update.rolled_back, { version }),
    }),
    [t],
  );
  const copyRef = useRef(copy);
  copyRef.current = copy;

  const syncToasts = useCallback(
    (nextCandidates: ComputerUpdateCandidate[]) => {
      if (!wsId || !userId) return;
      const storage = browserStorage();
      const localByMachine = getOrCreateMap(localByMachineRef);
      const published = getOrCreateMap(publishedContentRef);
      const nextPublished = new Map<string, string>();
      const c = copyRef.current;

      const upsertToast = (
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
        const busy = phase === "updating";
        const laterTarget =
          opts.candidate?.targetVersion ?? opts.local?.targetVersion ?? null;
        const actionRuntimeId =
          opts.candidate?.runtimeId ?? opts.local?.runtimeId ?? null;
        const actionDaemonId =
          opts.candidate?.daemonId ?? opts.local?.daemonId ?? null;

        const contentKey = computerUpdateToastContentKey({
          phase,
          title: opts.title,
          versionLine: opts.versionLine,
          progressLabel: opts.progressLabel,
          errorLabel: opts.errorLabel,
          updateLabel: c.update,
          laterLabel: c.later,
          retryLabel: c.retry,
          dismissLabel: c.dismiss,
          busy,
          laterTarget,
          actionRuntimeId,
          actionDaemonId,
        });
        nextPublished.set(toastId, contentKey);

        // Same id + same content already on screen — do not re-mount toast.
        if (published.get(toastId) === contentKey) return;

        toast.custom(
          (id) => (
            <ComputerUpdateToast
              phase={phase}
              title={opts.title}
              versionLine={opts.versionLine}
              progressLabel={opts.progressLabel}
              errorLabel={opts.errorLabel}
              updateLabel={c.update}
              laterLabel={c.later}
              retryLabel={c.retry}
              dismissLabel={c.dismiss}
              busy={busy}
              onUpdate={
                opts.candidate
                  ? () => {
                      startUpdateRef.current?.(opts.candidate!);
                    }
                  : undefined
              }
              onRetry={
                opts.local
                  ? () => {
                      startUpdateRef.current?.({
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
                      dismissLaterRef.current?.({
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
                        dismissLaterRef.current?.({
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

      for (const [machineKey, local] of localByMachine.entries()) {
        const versionsCaughtUp =
          local.phase === "updating" &&
          (runtimesRef.current ?? []).some((runtime) => {
            if (computerUpdateMachineKey(runtime) !== machineKey) return false;
            const currentVersion = runtimeCurrentVersion(runtime);
            return (
              !!currentVersion &&
              !isNewerCliVersion(local.targetVersion, currentVersion)
            );
          });
        if (versionsCaughtUp) {
          requestByMachineRef.current?.delete(machineKey);
          localByMachine.delete(machineKey);
          if (storage) {
            dismissComputerUpdate(storage, wsId, machineKey, local.targetVersion);
          }
          upsertToast(machineKey, "success", {
            title: c.successTitle(local.machineTitle),
            versionLine: c.successBody(local.targetVersion),
            local,
          });
          continue;
        }
        if (local.phase === "updating" || local.phase === "failed") {
          upsertToast(machineKey, local.phase, {
            title:
              local.phase === "updating"
                ? c.updatingTitle(local.machineTitle)
                : c.failedTitle(local.machineTitle),
            progressLabel: local.progress,
            errorLabel: local.error,
            local,
          });
        }
      }

      for (const candidate of nextCandidates) {
        if (localByMachine.has(candidate.machineKey)) continue;
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
        const current = candidate.currentVersion ?? c.versionUnknown;
        upsertToast(candidate.machineKey, "prompt", {
          title: c.promptTitle(candidate.machineTitle),
          versionLine: c.versionLine(current, candidate.targetVersion),
          candidate,
        });
      }

      for (const id of published.keys()) {
        if (!nextPublished.has(id)) {
          toast.dismiss(id);
        }
      }
      publishedContentRef.current = nextPublished;
    },
    [userId, wsId],
  );

  const candidatesRef = useRef(candidates);
  candidatesRef.current = candidates;

  dismissLaterRef.current = (candidate) => {
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
    getOrCreateMap(localByMachineRef).delete(candidate.machineKey);
    const toastId = computerUpdateToastId(candidate.machineKey);
    toast.dismiss(toastId);
    publishedContentRef.current?.delete(toastId);
    syncToasts(candidatesRef.current);
  };

  startUpdateRef.current = (candidate) => {
    if (!wsId) return;
    const c = copyRef.current;
    const toastId = computerUpdateToastId(candidate.machineKey);
    const localByMachine = getOrCreateMap(localByMachineRef);
    localByMachine.set(candidate.machineKey, {
      phase: "updating",
      progress: c.progressPending,
      targetVersion: candidate.targetVersion,
      machineTitle: candidate.machineTitle,
      daemonId: candidate.daemonId,
      runtimeId: candidate.runtimeId,
    });
    // Force re-publish even if a prompt was showing for the same id.
    publishedContentRef.current?.delete(toastId);
    syncToasts(candidatesRef.current);

    void (async () => {
      try {
        const requestId = createSafeId();
        getOrCreateMap(requestByMachineRef).set(candidate.machineKey, {
          requestId,
          daemonId: candidate.daemonId,
          targetVersion: candidate.targetVersion,
          machineTitle: candidate.machineTitle,
          runtimeId: candidate.runtimeId,
        });
        await api.initiateMachineUpgrade(
          candidate.daemonId,
          candidate.targetVersion,
          requestId,
        );
        const storage = browserStorage();
        if (storage) {
          clearComputerUpdateDismiss(storage, wsId, candidate.machineKey);
        }
        const current = localByMachine.get(candidate.machineKey);
        if (current?.phase === "updating" && current.progress === c.progressPending) {
          localByMachine.set(candidate.machineKey, {
            ...current,
            progress: c.progressRunning,
          });
        }
        syncToasts(candidatesRef.current);
      } catch (err) {
        let message = c.failedGeneric;
        if (
          err instanceof ApiError &&
          err.status === 409 &&
          err.body &&
          typeof err.body === "object" &&
          (err.body as Record<string, unknown>).code === "runtime_pinned"
        ) {
          message = c.pinned;
        } else if (err instanceof Error && err.message.trim()) {
          message = err.message;
        }
        localByMachine.set(candidate.machineKey, {
          phase: "failed",
          error: message,
          targetVersion: candidate.targetVersion,
          machineTitle: candidate.machineTitle,
          daemonId: candidate.daemonId,
          runtimeId: candidate.runtimeId,
        });
        publishedContentRef.current?.delete(toastId);
        syncToasts(candidatesRef.current);
      }
    })();
  };

  useWSEvent("computer:upgrade:progress", (raw) => {
    const payload = raw as ComputerUpgradeProgressPayload;
    const requests = requestByMachineRef.current;
    if (!requests) return;
    for (const [machineKey, request] of requests) {
      if (request.daemonId !== payload.computer_id || request.requestId !== payload.requestId) continue;
      const c = copyRef.current;
      const progress = (() => {
        switch (payload.phase) {
          case "downloading":
          case "staging":
            return c.progressDownloading;
          case "verifying":
            return c.progressVerifying;
          case "applying":
          case "handoff":
            return c.progressApplying;
          case "restarting":
            return c.progressRestarting;
          default:
            return c.progressRunning;
        }
      })();
      const localByMachine = getOrCreateMap(localByMachineRef);
      localByMachine.set(machineKey, {
        phase: "updating",
        progress,
        targetVersion: request.targetVersion,
        machineTitle: request.machineTitle,
        daemonId: request.daemonId,
        runtimeId: request.runtimeId,
      });
      syncToasts(candidatesRef.current);
      return;
    }
  });
  useWSEvent("computer:upgrade:done", (raw) => {
    const payload = raw as ComputerUpgradeDonePayload;
    const requests = requestByMachineRef.current;
    if (!requests) return;
    for (const [machineKey, request] of requests) {
      if (request.daemonId !== payload.computer_id || request.requestId !== payload.requestId) continue;
      requests.delete(machineKey);
      const localByMachine = getOrCreateMap(localByMachineRef);
      const toastId = computerUpdateToastId(machineKey);
      if (!payload.ok) {
        localByMachine.set(machineKey, {
          phase: "failed",
          error:
            payload.error?.trim() ||
            (payload.rolledBack && payload.newVersion
              ? copyRef.current.rolledBack(payload.newVersion)
              : copyRef.current.failedGeneric),
          targetVersion: request.targetVersion,
          machineTitle: request.machineTitle,
          daemonId: request.daemonId,
          runtimeId: request.runtimeId,
        });
        publishedContentRef.current?.delete(toastId);
        syncToasts(candidatesRef.current);
        return;
      }
      const storage = browserStorage();
      if (storage && wsId) {
        dismissComputerUpdate(storage, wsId, machineKey, request.targetVersion);
      }
      localByMachine.delete(machineKey);
      publishedContentRef.current?.delete(toastId);
      toast.custom(
        (id) => (
          <ComputerUpdateToast
            phase="success"
            title={copyRef.current.successTitle(request.machineTitle)}
            versionLine={copyRef.current.successBody(request.targetVersion)}
            updateLabel={copyRef.current.update}
            laterLabel={copyRef.current.later}
            retryLabel={copyRef.current.retry}
            dismissLabel={copyRef.current.dismiss}
            onDismiss={() => toast.dismiss(id)}
          />
        ),
        { ...computerUpdateSuccessToastOptions, id: toastId },
      );
      return;
    }
  });

  // Only re-sync when eligibility fingerprint or locale copy changes —
  // not on every runtime-list array identity churn. Version changes are also
  // observed because a reconnect can replace the transient upgrade-done event.
  useEffect(() => {
    syncToasts(candidatesRef.current);
  }, [candidatesFingerprint, copy, runtimeVersionsFingerprint, syncToasts]);

  return null;
}
