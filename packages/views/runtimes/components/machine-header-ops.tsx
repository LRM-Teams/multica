"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  isSandboxRuntime,
  runtimeCurrentVersion,
  runtimeLaunchedBy,
  runtimeTargetVersion,
  deriveUpdateStatus,
} from "@multica/core/runtimes";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { api, ApiError } from "@multica/core/api";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ArrowUpCircle, Loader2, MoreHorizontal } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import type { RuntimeRestartStatus, RuntimeUpdateStatus } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { formatRuntimeUpdateError } from "./update-error";
import { DeleteComputerDialog } from "./delete-computer-dialog";
import {
  machinePrimaryRuntimeId,
  type RuntimeMachine,
} from "./runtime-machines";

/**
 * LRM-1036 / LRM-1031 v2 — detail-header right slot:
 * primary Upgrade (only when needed) + ⋯ overflow (Restart… / Delete…).
 */
export function MachineHeaderOps({
  machine,
  now,
  onDeleted,
}: {
  machine: RuntimeMachine;
  now: number;
  onDeleted?: () => void;
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const primaryId = machinePrimaryRuntimeId(machine, now);
  const primary = machine.runtimes.find((r) => r.id === primaryId) ?? null;

  const currentMember = user
    ? members.find((m) => m.user_id === user.id)
    : null;
  const isAdmin = currentMember
    ? currentMember.role === "owner" || currentMember.role === "admin"
    : false;
  const isRuntimeOwner = !!user && !!primary && primary.owner_id === user.id;
  const canManage = isAdmin || isRuntimeOwner;

  const launchedBy = primary ? runtimeLaunchedBy(primary) : null;
  const isSandbox = primary ? isSandboxRuntime(primary) : false;
  const isOnline = machine.health === "online";
  const isLocal = machine.mode === "local";
  const isUpdatingHealth =
    primary?.runtime_health === "updating" ||
    primary?.update_state === "pending" ||
    primary?.update_state === "running";

  const restartBlockedReason = useMemo(() => {
    if (isUpdatingHealth) return t(($) => $.machine.ops.restart_updating);
    if (!isLocal) return t(($) => $.machine.ops.restart_not_local);
    if (launchedBy === "desktop") {
      return t(($) => $.machine.ops.restart_managed_desktop);
    }
    if (isSandbox) return t(($) => $.machine.ops.restart_sandbox);
    if (!canManage) return t(($) => $.machine.ops.restart_no_permission);
    if (!isOnline) return t(($) => $.machine.ops.restart_offline);
    return null;
  }, [
    isUpdatingHealth,
    isLocal,
    launchedBy,
    isSandbox,
    canManage,
    isOnline,
    t,
  ]);

  const canRestart =
    !isUpdatingHealth &&
    canManage &&
    isLocal &&
    isOnline &&
    launchedBy !== "desktop" &&
    !isSandbox &&
    !!primaryId;

  const canDeleteComputer =
    machine.runtimes.length > 0 &&
    !!user &&
    machine.runtimes.every((r) => r.owner_id === user.id);

  const deleteBlockedReason = canDeleteComputer
    ? null
    : t(($) => $.machine.ops.delete_owner_only);

  const [restartConfirmOpen, setRestartConfirmOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [restartStatus, setRestartStatus] = useState<RuntimeRestartStatus | null>(
    null,
  );
  const [restarting, setRestarting] = useState(false);
  const restartPollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const cleanupRestart = useCallback(() => {
    if (restartPollRef.current) {
      clearInterval(restartPollRef.current);
      restartPollRef.current = null;
    }
  }, []);

  useEffect(() => () => cleanupRestart(), [cleanupRestart]);

  const handleRestart = async () => {
    if (!primaryId) return;
    cleanupRestart();
    setRestartConfirmOpen(false);
    setRestarting(true);
    setRestartStatus("pending");
    try {
      const restart = await api.initiateRestart(primaryId);
      restartPollRef.current = setInterval(async () => {
        try {
          const result = await api.getRestart(primaryId, restart.id);
          setRestartStatus(result.status);
          if (result.status === "delivered" || result.status === "timeout") {
            setRestarting(false);
            cleanupRestart();
          }
        } catch {
          // ignore poll errors
        }
      }, 2000);
    } catch {
      setRestartStatus("timeout");
      setRestarting(false);
    }
  };

  if (!machine.runtimes.length) return null;

  return (
    <div
      className="flex shrink-0 items-center gap-2"
      data-testid="machine-header-ops"
    >
      {isLocal && primaryId && primary ? (
        <HeaderUpgradeControl
          runtimeId={primaryId}
          currentVersion={
            machine.cliVersion ?? runtimeCurrentVersion(primary)
          }
          targetVersion={
            machine.updateTargetVersion ?? runtimeTargetVersion(primary)
          }
          updateState={primary.update_state}
          runtimeHealth={primary.runtime_health}
          updateError={machine.updateError ?? primary.update_error}
          isOnline={isOnline}
          launchedBy={launchedBy}
          canUpdate={canManage}
          isSandbox={isSandbox}
          pinnedVersion={primary.pinned_version}
        />
      ) : null}

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              variant="outline"
              size="icon-sm"
              className="h-[30px] w-[30px]"
              aria-label={t(($) => $.machine.ops.more_aria)}
              data-testid="machine-actions-menu-trigger"
            />
          }
        >
          <MoreHorizontal className="h-4 w-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-[200px]">
          <DropdownMenuItem
            disabled={!canRestart || restarting}
            onClick={() => {
              if (canRestart && !restarting) setRestartConfirmOpen(true);
            }}
            data-testid="machine-actions-restart"
          >
            <span className="min-w-0 flex-1">
              {t(($) => $.machine.ops.restart_menu)}
            </span>
            {restartBlockedReason ? (
              <span
                className="ml-3 shrink-0 text-[11px] text-muted-foreground"
                data-testid="machine-ops-restart-reason"
              >
                {restartBlockedReason}
              </span>
            ) : null}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            variant="destructive"
            disabled={!canDeleteComputer}
            onClick={() => {
              if (canDeleteComputer) setDeleteOpen(true);
            }}
            data-testid="machine-actions-delete"
          >
            <span className="min-w-0 flex-1">
              {t(($) => $.machine.ops.delete_menu)}
            </span>
            {deleteBlockedReason ? (
              <span
                className="ml-3 shrink-0 text-[11px] text-muted-foreground"
                data-testid="machine-ops-delete-reason"
              >
                {deleteBlockedReason}
              </span>
            ) : null}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <AlertDialog open={restartConfirmOpen} onOpenChange={setRestartConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.restart.confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.restart.confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>
              {t(($) => $.restart.confirm_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={handleRestart}>
              {t(($) => $.restart.confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {canDeleteComputer && deleteOpen ? (
        <DeleteComputerDialog
          open={deleteOpen}
          onOpenChange={setDeleteOpen}
          machine={machine}
          wsId={wsId}
          canDelete={canDeleteComputer}
          onDeleted={() => {
            setDeleteOpen(false);
            onDeleted?.();
          }}
        />
      ) : null}

      {restartStatus === "timeout" ? (
        <span className="sr-only">{t(($) => $.restart.status.timeout)}</span>
      ) : null}
    </div>
  );
}

function HeaderUpgradeControl({
  runtimeId,
  currentVersion,
  targetVersion,
  updateState,
  runtimeHealth = "ok",
  updateError,
  isOnline,
  launchedBy,
  canUpdate = true,
  isSandbox = false,
  pinnedVersion,
}: {
  runtimeId: string;
  currentVersion: string | null;
  targetVersion: string | null;
  updateState?: import("@multica/core/types").RuntimeUpdateState;
  runtimeHealth?: import("@multica/core/types").RuntimeHealthState;
  updateError?: string | null;
  isOnline: boolean;
  launchedBy?: string | null;
  canUpdate?: boolean;
  isSandbox?: boolean;
  pinnedVersion?: string | null;
}) {
  const { t } = useT("runtimes");
  const qc = useQueryClient();
  const isManaged = launchedBy === "desktop";
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
      const update = await api.initiateUpdate(runtimeId, targetVersion);
      pollRef.current = setInterval(async () => {
        try {
          const result = await api.getUpdateResult(runtimeId, update.id);
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
  const isFailed =
    derivedStatus === "failed" || derivedStatus === "timeout";
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

  // LRM-1036: no primary when up-to-date / offline / managed / sandbox / no update.
  if (isManaged || isSandbox) return null;
  if (!isOnline && !isFailed && !isActive) return null;
  if (!hasUpdate && !isActive && !isFailed) return null;

  if (isActive) {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-[30px] gap-1.5 px-3 text-xs"
        disabled
        data-testid="machine-header-upgrade"
      >
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        {t(($) => $.machine.ops.updating)}
      </Button>
    );
  }

  if (isFailed) {
    return (
      <div className="flex items-center gap-2">
        <button
          type="button"
          className="inline-flex h-[30px] items-center gap-1 rounded-[7px] bg-destructive/10 px-2.5 text-[11.5px] text-destructive"
          onClick={() => {
            if (canRetry) void handleUpdate();
          }}
          data-testid="machine-header-upgrade-fail"
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
        <Button
          type="button"
          size="sm"
          className="h-[30px] gap-1.5 px-3 text-xs"
          onClick={() => void handleUpdate()}
          disabled={!canRetry}
          data-testid="machine-header-upgrade"
        >
          <ArrowUpCircle className="h-3.5 w-3.5" />
          {t(($) => $.machine.ops.upgrade)}
        </Button>
      </div>
    );
  }

  return (
    <Button
      type="button"
      size="sm"
      className="h-[30px] gap-1.5 px-3 text-xs"
      onClick={() => void handleUpdate()}
      disabled={!canStartUpdate}
      data-testid="machine-header-upgrade"
    >
      <ArrowUpCircle className="h-3.5 w-3.5" />
      {t(($) => $.machine.ops.upgrade_to, {
        version: targetVersion ?? "",
      })}
    </Button>
  );
}
