"use client";

import { useCallback, useEffect, useMemo, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { useWSEvent } from "@multica/core/realtime";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  clearComputerUpdateDismiss,
  computerUpdateCandidatesFingerprint,
  computerUpdateToastContentKey,
  computerUpdateToastId,
  dismissComputerUpdate,
  isComputerUpdateDismissed,
  listComputerUpdateCandidates,
  useAllComputerUpgrades,
  useComputerUpgradeStore,
  type ComputerUpdateCandidate,
  type ComputerUpgradeRecord,
} from "@multica/core/runtimes";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
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
  progressReady: string;
  failedGeneric: string;
  pinned: string;
  promptTitle: (name: string) => string;
  versionLine: (current: string, target: string) => string;
  updatingTitle: (name: string) => string;
  failedTitle: (name: string) => string;
  successTitle: (name: string) => string;
  successBody: (version: string) => string;
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
 * - Candidate-list identity churn is ignored via fingerprint.
 * - toast.custom is skipped when the content key for a toast id is unchanged
 *   (runtime list refresh while a sticky prompt is already correct).
 * - Upgrades coordinate through shared useComputerUpgradeStore.
 */
export function ComputerUpdateToastListener() {
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id);
  const qc = useQueryClient();
  const { t } = useT("layout");
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  const runtimesRef = useRef(runtimes);
  runtimesRef.current = runtimes;

  const upgrades = useAllComputerUpgrades();

  /** toastId → last published content key (skip identical re-push). */
  const publishedContentRef = useRef<Map<string, string> | null>(null);
  const startUpdateRef = useRef<
    ((candidate: ComputerUpdateCandidate) => void) | null
  >(null);
  const dismissLaterRef = useRef<
    ((
      candidate: Pick<ComputerUpdateCandidate, "machineKey" | "targetVersion"> & {
        daemonId?: string | null;
      },
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
      progressReady: t(($) => $.computer_update.progress_ready),
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
    }),
    [t],
  );
  const copyRef = useRef(copy);
  copyRef.current = copy;

  const syncToasts = useCallback(
    (
      nextCandidates: ComputerUpdateCandidate[],
      currentUpgrades: Record<string, ComputerUpgradeRecord>,
    ) => {
      if (!wsId || !userId) return;
      const storage = browserStorage();
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
          upgrade?: ComputerUpgradeRecord;
        },
      ) => {
        const toastId = computerUpdateToastId(machineKey);
        const busy = phase === "updating";
        const laterTarget =
          opts.candidate?.targetVersion ?? opts.upgrade?.targetVersion ?? null;
        const actionRuntimeId =
          opts.candidate?.runtimeId ?? opts.upgrade?.runtimeId ?? null;
        const actionDaemonId =
          opts.candidate?.daemonId ?? opts.upgrade?.daemonId ?? null;

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
                opts.upgrade
                  ? () => {
                      startUpdateRef.current?.({
                        machineKey,
                        daemonId: opts.upgrade!.daemonId,
                        runtimeId: opts.upgrade!.runtimeId ?? "",
                        machineTitle: opts.upgrade!.machineTitle ?? "",
                        currentVersion: null,
                        targetVersion: opts.upgrade!.targetVersion,
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
                        daemonId: actionDaemonId,
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
                          daemonId: actionDaemonId,
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

      for (const upgrade of Object.values(currentUpgrades)) {
        if (upgrade.phase === "pending" || upgrade.phase === "running") {
          const runtime = runtimesRef.current?.find(
            (r) =>
              (r.daemon_id && r.daemon_id === upgrade.daemonId) ||
              r.id === upgrade.runtimeId ||
              r.name === upgrade.machineKey,
          );
          const reachedTarget =
            runtime &&
            upgrade.targetVersion &&
            (runtime.current_version === upgrade.targetVersion ||
              `v${runtime.current_version}` === upgrade.targetVersion ||
              runtime.current_version === upgrade.targetVersion.replace(/^v/, ""));

          if (reachedTarget) {
            useComputerUpgradeStore.getState().recordDone({
              computer_id: upgrade.daemonId,
              ok: true,
              newVersion: runtime.current_version ?? undefined,
            });
            const machineKey = upgrade.machineKey || upgrade.daemonId;
            upsertToast(machineKey, "success", {
              title: c.successTitle(upgrade.machineTitle || upgrade.daemonId),
              versionLine: c.successBody(upgrade.targetVersion),
            });
            if (storage && wsId && upgrade.targetVersion) {
              dismissComputerUpdate(
                storage,
                wsId,
                machineKey,
                upgrade.targetVersion,
              );
            }
            continue;
          }

          upsertToast(upgrade.machineKey, "updating", {
            title: c.updatingTitle(upgrade.machineTitle || upgrade.daemonId),
            progressLabel:
              upgrade.progress ||
              (upgrade.phase === "pending"
                ? c.progressPending
                : c.progressRunning),
            upgrade,
          });
        } else if (upgrade.phase === "failed") {
          upsertToast(upgrade.machineKey, "failed", {
            title: c.failedTitle(upgrade.machineTitle || upgrade.daemonId),
            errorLabel:
              upgrade.error === "runtime_pinned"
                ? c.pinned
                : (upgrade.error || c.failedGeneric),
            upgrade,
          });
        }
      }

      for (const candidate of nextCandidates) {
        if (currentUpgrades[candidate.daemonId]) continue;
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
    if (candidate.daemonId) {
      useComputerUpgradeStore.getState().dismissUpgrade(candidate.daemonId);
    }
    const toastId = computerUpdateToastId(candidate.machineKey);
    toast.dismiss(toastId);
    publishedContentRef.current?.delete(toastId);
    syncToasts(candidatesRef.current, useComputerUpgradeStore.getState().upgrades);
  };

  startUpdateRef.current = (candidate) => {
    if (!wsId) return;
    const storage = browserStorage();
    if (storage) {
      clearComputerUpdateDismiss(storage, wsId, candidate.machineKey);
    }
    void useComputerUpgradeStore.getState().startUpgrade({
      daemonId: candidate.daemonId,
      targetVersion: candidate.targetVersion,
      machineKey: candidate.machineKey,
      machineTitle: candidate.machineTitle,
      runtimeId: candidate.runtimeId,
    }).catch(() => {
      // Store state handles failed state
    });
  };

  useWSEvent("computer:upgrade:progress", (raw) => {
    const payload = raw as ComputerUpgradeProgressPayload;
    let label: string | null = payload.message ?? null;
    if (!label && payload.phase) {
      const c = copyRef.current;
      switch (payload.phase) {
        case "pending":
          label = c.progressPending;
          break;
        case "downloading":
          label = c.progressDownloading;
          break;
        case "verifying":
          label = c.progressVerifying;
          break;
        case "applying":
          label = c.progressApplying;
          break;
        case "restarting":
          label = c.progressRestarting;
          break;
        case "ready":
          label = c.progressReady;
          break;
        default:
          label = c.progressRunning;
          break;
      }
    }
    useComputerUpgradeStore.getState().recordProgress({
      ...payload,
      message: label ?? undefined,
    });
  });

  useWSEvent("computer:upgrade:done", (raw) => {
    const payload = raw as ComputerUpgradeDonePayload;
    useComputerUpgradeStore.getState().recordDone(payload);
    const upgrade = useComputerUpgradeStore.getState().getUpgrade(payload.computer_id);
    if (payload.ok) {
      const storage = browserStorage();
      if (storage && wsId && upgrade?.machineKey && upgrade?.targetVersion) {
        dismissComputerUpdate(storage, wsId, upgrade.machineKey, upgrade.targetVersion);
      }
      const machineKey = upgrade?.machineKey || payload.computer_id;
      const toastId = computerUpdateToastId(machineKey);
      publishedContentRef.current?.delete(toastId);
      toast.custom(
        (id) => (
          <ComputerUpdateToast
            phase="success"
            title={copyRef.current.successTitle(upgrade?.machineTitle || payload.computer_id)}
            versionLine={copyRef.current.successBody(upgrade?.targetVersion || payload.newVersion || "")}
            updateLabel={copyRef.current.update}
            laterLabel={copyRef.current.later}
            retryLabel={copyRef.current.retry}
            dismissLabel={copyRef.current.dismiss}
            onDismiss={() => toast.dismiss(id)}
          />
        ),
        { ...computerUpdateSuccessToastOptions, id: toastId },
      );
      qc.invalidateQueries({
        predicate: (query) => query.queryKey[0] === "runtimes",
      });
    }
  });

  // Only re-sync when eligibility fingerprint, locale copy, or active upgrades change —
  // not on every runtime-list array identity churn.
  useEffect(() => {
    syncToasts(candidatesRef.current, upgrades);
  }, [candidatesFingerprint, copy, syncToasts, upgrades]);

  return null;
}
