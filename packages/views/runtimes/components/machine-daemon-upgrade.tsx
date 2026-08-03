"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  deriveUpdateStatus,
  isSandboxRuntime,
  runtimeCurrentVersion,
  runtimeLaunchedBy,
  runtimeTargetVersion,
} from "@multica/core/runtimes";
import { api, ApiError } from "@multica/core/api";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ArrowUpCircle, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { AgentRuntime, RuntimeUpdateStatus } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { formatRuntimeUpdateError } from "./update-error";

/**
 * LRM-1071 / v5 — Upgrade sits on the Basics Daemon row (S3), outline xs,
 * only when an update is available. Not a header primary.
 */
export function MachineDaemonUpgrade({
  runtime,
  cliVersion,
  updateTargetVersion,
  updateError,
  isOnline,
  canUpdate,
}: {
  runtime: AgentRuntime;
  cliVersion: string | null;
  updateTargetVersion: string | null;
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
  const targetVersion = updateTargetVersion ?? runtimeTargetVersion(runtime);
  const updateState = runtime.update_state;
  const runtimeHealth = runtime.runtime_health;
  const pinnedVersion = runtime.pinned_version;

  const [status, setStatus] = useState<RuntimeUpdateStatus | null>(null);
  const [updating, setUpdating] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const cleanup = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => () => cleanup(), [cleanup]);

  const refreshRuntimes = useCallback(() => {
    qc.invalidateQueries({
      predicate: (query) => query.queryKey[0] === "runtimes",
    });
  }, [qc]);

  const handleUpdate = async () => {
    if (!targetVersion) return;
    cleanup();
    setUpdating(true);
    setStatus("pending");
    try {
      const update = await api.initiateUpdate(runtime.id, targetVersion);
      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtime.id, update.id);
          setStatus(result.status as RuntimeUpdateStatus);
          if (
            result.status === "completed" ||
            result.status === "ready_to_apply" ||
            result.status === "failed" ||
            result.status === "timeout"
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
        return;
      }
      setStatus("failed");
      setUpdating(false);
    }
  };

  const derivedStatus = deriveUpdateStatus({
    pollStatus: status,
    updateState,
    runtimeHealth,
  });
  const hasUpdate = runtimeHealth === "update_available" && !!targetVersion;
  const isActive =
    updating || derivedStatus === "pending" || derivedStatus === "running";
  const isFailed = derivedStatus === "failed" || derivedStatus === "timeout";
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
  const canRetry =
    !!targetVersion &&
    isOnline &&
    canUpdate &&
    !isManaged &&
    !isSandbox &&
    !isPinned &&
    !isActive &&
    isFailed;

  // Basics Daemon cell always shows the version. Upgrade chrome only when
  // an update is available / in-flight / failed (and not desktop-managed /
  // sandbox — those stay version-only).
  const showUpgradeChrome =
    !isManaged &&
    !isSandbox &&
    (hasUpdate || isActive || isFailed) &&
    (isOnline || isFailed || isActive);

  const versionPath = (
    <span className="inline-flex flex-wrap items-center gap-1.5 font-mono text-xs">
      <span data-testid="machine-basics-daemon-version">
        {currentVersion ?? t(($) => $.update.version_unknown)}
      </span>
      {showUpgradeChrome && targetVersion ? (
        <>
          <span className="text-muted-foreground">→</span>
          <span className="text-brand">{targetVersion}</span>
          <span className="font-sans text-muted-foreground">
            {t(($) => $.update.available)}
          </span>
        </>
      ) : null}
    </span>
  );

  if (!showUpgradeChrome) {
    return (
      <span
        className="inline-flex flex-wrap items-center gap-2"
        data-testid="machine-daemon-upgrade"
      >
        {versionPath}
      </span>
    );
  }

  if (isActive) {
    return (
      <span className="inline-flex flex-wrap items-center gap-2" data-testid="machine-daemon-upgrade">
        {versionPath}
        <Button
          type="button"
          variant="outline"
          size="xs"
          className="h-6 gap-1 px-2 text-[11px]"
          disabled
          data-testid="machine-daemon-upgrade-btn"
        >
          <Loader2 className="h-3 w-3 animate-spin" />
          {t(($) => $.machine.ops.updating)}
        </Button>
      </span>
    );
  }

  if (isFailed) {
    return (
      <span className="inline-flex flex-wrap items-center gap-2" data-testid="machine-daemon-upgrade">
        {versionPath}
        <button
          type="button"
          className="inline-flex h-6 items-center rounded-md bg-destructive/10 px-2 text-[11px] text-destructive"
          onClick={() => {
            if (canRetry) void handleUpdate();
          }}
          data-testid="machine-daemon-upgrade-fail"
          title={
            formatRuntimeUpdateError({
              rawError: updateError,
              currentVersion,
              targetVersion,
              t,
            }) || undefined
          }
        >
          {t(($) => $.machine.ops.upgrade_failed_retry)}
        </button>
      </span>
    );
  }

  return (
    <span className="inline-flex flex-wrap items-center gap-2" data-testid="machine-daemon-upgrade">
      {versionPath}
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
        {t(($) => $.machine.ops.upgrade)}
      </Button>
    </span>
  );
}
