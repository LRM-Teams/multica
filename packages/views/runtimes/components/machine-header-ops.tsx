"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  isSandboxRuntime,
  runtimeLaunchedBy,
} from "@multica/core/runtimes";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { Loader2, RotateCcw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
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
import type { RuntimeRestartStatus } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import {
  machinePrimaryRuntimeId,
  type RuntimeMachine,
} from "./runtime-machines";

/**
 * LRM-1071 / LRM-1085 — detail-header right slot (S1):
 * Restart outline only. Empty/disabled ⋯ removed (Frank: 点不了就删).
 * Upgrade lives on the Daemon Basics row; Delete lives in Danger Zone only.
 * Blocked Restart reason stays on the Restart button `title`.
 */
export function MachineHeaderOps({
  machine,
  now,
}: {
  machine: RuntimeMachine;
  now: number;
  /** @deprecated Delete moved to Danger Zone (LRM-1071). */
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

  const [restartConfirmOpen, setRestartConfirmOpen] = useState(false);
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
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-[30px] gap-1.5 px-3 text-xs"
        disabled={!canRestart || restarting}
        title={restartBlockedReason ?? undefined}
        onClick={() => {
          if (canRestart && !restarting) setRestartConfirmOpen(true);
        }}
        data-testid="machine-header-restart"
      >
        {restarting ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        ) : (
          <RotateCcw className="h-3.5 w-3.5" />
        )}
        <span className="hidden sm:inline">
          {t(($) => $.machine.ops.restart)}
        </span>
      </Button>

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

      {restartStatus === "timeout" ? (
        <span className="sr-only">{t(($) => $.restart.status.timeout)}</span>
      ) : null}
    </div>
  );
}
