"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  deriveUpdateStatus,
  isSandboxRuntime,
  runtimeCurrentVersion,
  runtimeLaunchedBy,
  isNewerCliVersion,
} from "@multica/core/runtimes";
import { api, ApiError } from "@multica/core/api";
import { createSafeId } from "@multica/core/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ArrowUpCircle, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { AgentRuntime, RuntimeUpdateStatus } from "@multica/core/types";
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
  const machineUpgrade = runtime.machine_upgrade ?? null;
  const machineTarget =
    machineUpgrade?.resolved_target?.trim() || machineUpgrade?.requested_target?.trim() || null;
  const targetVersion = machineTarget ?? daemonTargetVersion ?? null;
  const updateState = runtime.update_state;
  const runtimeHealth = runtime.runtime_health;
  const pinnedVersion =
    "pinned_version" in runtime
      ? (runtime as AgentRuntime & { pinned_version?: string | null }).pinned_version
      : null;

  const [status, setStatus] = useState<RuntimeUpdateStatus | null>(null);
  const [updating, setUpdating] = useState(false);
  // Keep the last target we tried so a failed attempt still shows `→ target`
  // even if the server drops target_version after health flips to failed.
  const [lastAttemptTarget, setLastAttemptTarget] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const cleanup = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => () => cleanup(), [cleanup]);

  // Do NOT useEffect-clear local poll status when version catches up —
  // react-doctor flags that. isApplying / isActive already gate chrome on
  // versionsCaughtUp below (Frank stuck 「正在重启并切换版本…」).

  const refreshRuntimes = useCallback(() => {
    qc.invalidateQueries({
      predicate: (query) => query.queryKey[0] === "runtimes",
    });
  }, [qc]);

  const handleUpdate = async () => {
    const aim = targetVersion ?? lastAttemptTarget;
    if (!aim) return;
    cleanup();
    setUpdating(true);
    setLastAttemptTarget(aim);
    // Immediate local feedback (≤200ms) before first poll tick.
    setStatus("pending");
    try {
      const daemonID = runtime.daemon_id?.trim();
      if (!daemonID) {
        throw new Error("runtime has no daemon identity");
      }
      const update = await api.initiateMachineUpgrade(daemonID, aim, createSafeId());
      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getMachineUpgrade(daemonID, update.id);
          const nextStatus: RuntimeUpdateStatus = (() => {
            switch (result.phase) {
              case "queued": return "queued";
              case "completed": return "completed";
              case "timeout": return "timeout";
              case "failed": case "rolled_back": case "cancelled": return "failed";
              default: return "running";
            }
          })();
          setStatus(nextStatus);
          if (
            nextStatus === "completed" ||
            nextStatus === "failed" ||
            nextStatus === "timeout"
          ) {
            setUpdating(false);
            cleanup();
            refreshRuntimes();
          }
        } catch {
          // ignore poll errors
        }
      }, 2000);
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
        setUpdating(false);
        setStatus(null);
        return;
      }
      setStatus("failed");
      setUpdating(false);
    }
  };

  const projectedMachineStatus: RuntimeUpdateStatus | null = (() => {
    switch (machineUpgrade?.phase) {
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
  })();
  // A runtime page is a projection: no sibling needs to have initiated the
  // request locally to render the daemon's canonical queued/active operation.
  const effectiveStatus = status ?? projectedMachineStatus;
  const derivedStatus = deriveUpdateStatus({
    pollStatus: effectiveStatus,
    updateState,
    runtimeHealth,
  });
  const hasUpdate =
    runtimeHealth === "update_available" &&
    !!targetVersion &&
    isNewerCliVersion(targetVersion, currentVersion);
  const displayTarget = targetVersion ?? lastAttemptTarget;
  // Poll leaves local status at "completed" after stop — keep applying chrome
  // only while the live version is still behind the target. Once caught up
  // (incl. `0.4.2` vs `v0.4.2`), drop the spinner (Frank 2026-08-04 stuck UX).
  const versionsCaughtUp =
    !!displayTarget &&
    !!currentVersion &&
    !isNewerCliVersion(displayTarget, currentVersion);
  const isApplying =
    derivedStatus === "ready_to_apply" ||
    (derivedStatus === "completed" && !versionsCaughtUp);
  const isActive =
    updating ||
    derivedStatus === "pending" ||
    derivedStatus === "running" ||
    // poll may report "queued" before pending (older type packages omit it)
    effectiveStatus === "queued" ||
    isApplying;
  // Health may flip off `update_available` after a failed attempt — still failed.
  const isFailed =
    derivedStatus === "failed" ||
    derivedStatus === "timeout" ||
    (runtimeHealth === "failed" && !!updateError?.trim());
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
    if (
      derivedStatus === "pending" ||
      effectiveStatus === "queued" ||
      effectiveStatus === "pending"
    ) {
      return t(($) => $.machine.ops.upgrade_progress_pending);
    }
    if (derivedStatus === "running") {
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
    const reason =
      formatRuntimeUpdateError({
        rawError: machineUpgrade?.error_message ?? updateError,
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
