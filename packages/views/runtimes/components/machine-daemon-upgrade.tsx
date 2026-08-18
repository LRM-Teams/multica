"use client";

import { useCallback, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  deriveUpdateStatus,
  isSandboxRuntime,
  runtimeCurrentVersion,
  runtimeLaunchedBy,
  isNewerCliVersion,
  useComputerUpgrade,
  useComputerUpgradeStore,
} from "@multica/core/runtimes";
import { ApiError } from "@multica/core/api";
import { useWSEvent } from "@multica/core/realtime";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ArrowUpCircle, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type {
  AgentRuntime,
  ComputerUpgradeDonePayload,
  ComputerUpgradeProgressPayload,
} from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { formatRuntimeUpdateError } from "./update-error";

/**
 * Basics Daemon row upgrade (LRM-1071 / task #29).
 * Idle: version + outline CTA. Active: current → target + status line only
 * (no grey disabled button — Frank 2026-08-03). Failed: reason + retry.
 */
export function MachineDaemonUpgrade({
  runtime,
  cliVersion,
  daemonTargetVersion,
  updateError,
  isOnline,
  canUpdate,
}: {
  runtime: AgentRuntime;
  cliVersion: string | null;
  daemonTargetVersion: string | null;
  updateError: string | null;
  isOnline: boolean;
  canUpdate: boolean;
}) {
  const { t } = useT("runtimes");
  const qc = useQueryClient();
  const launchedBy = runtimeLaunchedBy(runtime);
  const isManaged = launchedBy === "desktop";
  const isSandbox = isSandboxRuntime(runtime);
  const currentVersion = cliVersion ?? runtimeCurrentVersion(runtime);
  const targetVersion = daemonTargetVersion ?? null;
  const updateState = runtime.update_state;
  const runtimeHealth = runtime.runtime_health;
  const pinnedVersion =
    "pinned_version" in runtime
      ? (runtime as AgentRuntime & { pinned_version?: string | null }).pinned_version
      : null;

  const computerId = runtime.daemon_id?.trim() ?? "";
  const activeUpgrade = useComputerUpgrade(computerId);

  // Keep the last target we tried so a failed attempt still shows `→ target`
  // even if the server drops target_version after health flips to failed.
  const [lastAttemptTarget, setLastAttemptTarget] = useState<string | null>(null);

  const refreshRuntimes = useCallback(() => {
    qc.invalidateQueries({
      predicate: (query) => query.queryKey[0] === "runtimes",
    });
  }, [qc]);

  useWSEvent("computer:upgrade:progress", (raw) => {
    const payload = raw as ComputerUpgradeProgressPayload;
    if (payload.computer_id !== computerId) return;
    useComputerUpgradeStore.getState().recordProgress(payload);
  });
  useWSEvent("computer:upgrade:done", (raw) => {
    const payload = raw as ComputerUpgradeDonePayload;
    if (payload.computer_id !== computerId) return;
    useComputerUpgradeStore.getState().recordDone(payload);
    refreshRuntimes();
  });

  const handleUpdate = async () => {
    const aim = targetVersion ?? activeUpgrade?.targetVersion ?? lastAttemptTarget;
    if (!aim) return;
    const daemonID = runtime.daemon_id?.trim();
    if (!daemonID) return;
    setLastAttemptTarget(aim);
    try {
      await useComputerUpgradeStore.getState().startUpgrade({
        daemonId: daemonID,
        targetVersion: aim,
        machineKey: runtime.daemon_id?.trim() || `runtime:${runtime.id}`,
        machineTitle: runtime.display_name || runtime.device_name || runtime.name || daemonID,
        runtimeId: runtime.id,
      });
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 409 &&
        err.body &&
        typeof err.body === "object" &&
        (err.body as Record<string, unknown>).code === "runtime_pinned"
      ) {
        showErrorToast(
          t(($) => $.update.pin_blocked_toast, {
            version: pinnedVersion?.trim() || t(($) => $.update.version_unknown),
          }),
        );
        return;
      }
    }
  };

  const status = activeUpgrade ? activeUpgrade.phase : null;
  const updating = activeUpgrade
    ? activeUpgrade.phase === "pending" || activeUpgrade.phase === "running"
    : false;

  const derivedStatus = deriveUpdateStatus({
    pollStatus: status,
    updateState,
    runtimeHealth,
  });
  const hasUpdate =
    runtimeHealth === "update_available" &&
    !!targetVersion &&
    isNewerCliVersion(targetVersion, currentVersion);
  const displayTarget =
    targetVersion ??
    (activeUpgrade?.targetVersion ? activeUpgrade.targetVersion : null) ??
    lastAttemptTarget;
  // Poll leaves local status at "completed" after stop — keep applying chrome
  // only while the live version is still behind the target. Once caught up
  // (incl. `0.4.2` vs `v0.4.2`), drop the spinner (Frank 2026-08-04 stuck UX).
  const versionsCaughtUp =
    !!displayTarget &&
    !!currentVersion &&
    !isNewerCliVersion(displayTarget, currentVersion);

  if (versionsCaughtUp && activeUpgrade?.phase === "completed") {
    useComputerUpgradeStore.getState().clearCompleted(computerId);
  }

  const isApplying =
    derivedStatus === "ready_to_apply" ||
    (derivedStatus === "completed" && !versionsCaughtUp) ||
    (activeUpgrade?.phase === "completed" && !versionsCaughtUp);
  const isActive =
    !versionsCaughtUp &&
    (updating ||
      derivedStatus === "pending" ||
      derivedStatus === "running" ||
      isApplying);
  // Health may flip off `update_available` after a failed attempt — still failed.
  const isFailed =
    !versionsCaughtUp &&
    (derivedStatus === "failed" ||
      derivedStatus === "timeout" ||
      activeUpgrade?.phase === "failed" ||
      (runtimeHealth === "failed" && !!updateError?.trim()));
  const isPinned = !!pinnedVersion?.trim();
  const canStartUpdate =
    hasUpdate &&
    !derivedStatus &&
    isOnline &&
    canUpdate &&
    !isManaged &&
    !isSandbox &&
    !isPinned &&
    !isActive;
  // Parker #29: retry when we still know a target — do NOT require
  // runtime_health === update_available (that hid the button after fail).
  const canRetry =
    !!displayTarget &&
    isOnline &&
    canUpdate &&
    !isManaged &&
    !isSandbox &&
    !isPinned &&
    !isActive &&
    isFailed;

  const showUpgradeChrome =
    !isManaged && !isSandbox && (hasUpdate || isActive || isFailed);

  const versionLabel = currentVersion ?? t(($) => $.update.version_unknown);

  const progressLabel = (() => {
    if (activeUpgrade?.progress) {
      return activeUpgrade.progress;
    }
    if (derivedStatus === "pending" || status === "pending") {
      return t(($) => $.machine.ops.upgrade_progress_pending);
    }
    if (derivedStatus === "running" || status === "running") {
      return t(($) => $.machine.ops.upgrade_progress_running);
    }
    if (isApplying) {
      return t(($) => $.machine.ops.upgrade_progress_applying);
    }
    // Local click before poll settles
    if (updating) {
      return t(($) => $.machine.ops.upgrade_progress_pending);
    }
    return t(($) => $.machine.ops.upgrade_progress_running);
  })();

  if (!showUpgradeChrome) {
    return (
      <span
        className="inline-flex flex-wrap items-center justify-end gap-2"
        data-testid="machine-daemon-upgrade"
      >
        <span
          className="font-mono text-xs"
          data-testid="machine-basics-daemon-version"
        >
          {versionLabel}
        </span>
      </span>
    );
  }

  // Active: current → target + status line only (no grey button).
  if (isActive) {
    return (
      <span
        className="inline-flex max-w-full flex-col items-end gap-1"
        data-testid="machine-daemon-upgrade"
        data-state="active"
      >
        <span className="inline-flex flex-wrap items-center justify-end gap-1.5 font-mono text-xs">
          <span data-testid="machine-basics-daemon-version">{versionLabel}</span>
          {displayTarget ? (
            <>
              <span className="text-muted-foreground" aria-hidden>
                →
              </span>
              <span className="text-brand" data-testid="machine-basics-daemon-target">
                {displayTarget}
              </span>
            </>
          ) : null}
        </span>
        <output
          className="inline-flex items-center gap-1.5 text-[11px] leading-none text-muted-foreground"
          data-testid="machine-daemon-upgrade-progress"
        >
          <Loader2 className="h-3 w-3 shrink-0 animate-spin text-brand" />
          {progressLabel}
        </output>
      </span>
    );
  }

  if (isFailed) {
    const rawReason =
      (activeUpgrade?.error && activeUpgrade.error !== "runtime_pinned"
        ? activeUpgrade.error
        : null) || updateError;
    const reason =
      formatRuntimeUpdateError({
        rawError: rawReason,
        currentVersion,
        targetVersion: displayTarget,
        t,
      }) || t(($) => $.machine.ops.upgrade_failed_unknown);
    return (
      <span
        className="inline-flex max-w-full flex-col items-end gap-1"
        data-testid="machine-daemon-upgrade"
        data-state="failed"
      >
        <span className="inline-flex flex-wrap items-center justify-end gap-1.5 font-mono text-xs">
          <span data-testid="machine-basics-daemon-version">{versionLabel}</span>
          {displayTarget ? (
            <>
              <span className="text-muted-foreground" aria-hidden>
                →
              </span>
              <span className="text-brand" data-testid="machine-basics-daemon-target">
                {displayTarget}
              </span>
            </>
          ) : null}
          <Button
            type="button"
            variant="outline"
            size="xs"
            className="h-6 gap-1 px-2 font-sans text-[11px]"
            onClick={() => {
              if (canRetry) void handleUpdate();
            }}
            disabled={!canRetry}
            data-testid="machine-daemon-upgrade-fail"
          >
            {t(($) => $.update.retry)}
          </Button>
        </span>
        <span
          className="max-w-[min(100%,20rem)] text-right text-[11px] leading-snug text-destructive"
          data-testid="machine-daemon-upgrade-error"
        >
          {t(($) => $.machine.ops.upgrade_failed_prefix)}
          {reason}
        </span>
      </span>
    );
  }

  // Idle · update available — non-owners see version only (Frank: owner-only upgrade).
  if (!canUpdate) {
    return (
      <span
        className="inline-flex max-w-full flex-col items-end gap-1"
        data-testid="machine-daemon-upgrade"
        data-state="owner-only"
      >
        <span
          className="font-mono text-xs"
          data-testid="machine-basics-daemon-version"
        >
          {versionLabel}
        </span>
        {displayTarget ? (
          <span className="text-[11px] text-muted-foreground">
            {t(($) => $.machine.ops.upgrade_owner_only)}
          </span>
        ) : null}
      </span>
    );
  }

  return (
    <span
      className="inline-flex flex-wrap items-center justify-end gap-2"
      data-testid="machine-daemon-upgrade"
      data-state="available"
    >
      <span
        className="font-mono text-xs"
        data-testid="machine-basics-daemon-version"
      >
        {versionLabel}
      </span>
      <Button
        type="button"
        variant="outline"
        size="xs"
        className="h-6 gap-1 px-2 text-[11px]"
        onClick={() => void handleUpdate()}
        disabled={!canStartUpdate}
        data-testid="machine-daemon-upgrade-btn"
      >
        <ArrowUpCircle className="h-3 w-3" />
        {targetVersion
          ? t(($) => $.machine.ops.upgrade_to, { version: targetVersion })
          : t(($) => $.machine.ops.upgrade)}
      </Button>
    </span>
  );
}
