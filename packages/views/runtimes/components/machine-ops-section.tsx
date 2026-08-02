"use client";

import { useMemo } from "react";
import {
  isSandboxRuntime,
  runtimeCurrentVersion,
  runtimeLaunchedBy,
  runtimeTargetVersion,
} from "@multica/core/runtimes";
import { useAuthStore } from "@multica/core/auth";
import { useQuery } from "@tanstack/react-query";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { Trash2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { UpdateSection } from "./update-section";
import { RestartSection } from "./restart-section";
import { MachineDeleteControl } from "./delete-computer-dialog";
import {
  machinePrimaryRuntimeId,
  type RuntimeMachine,
} from "./runtime-machines";
import { useT } from "../../i18n/use-t";

/**
 * Machine-level ops zone (Iris / Frank 2026-08-02): upgrade · restart ·
 * delete computer — one set per machine, not per runtime row.
 */
export function MachineOpsSection({
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

  const restartBlockedReason = useMemo(() => {
    if (!isLocal) return t(($) => $.machine.ops.restart_not_local);
    if (launchedBy === "desktop") {
      return t(($) => $.machine.ops.restart_managed_desktop);
    }
    if (isSandbox) return t(($) => $.machine.ops.restart_sandbox);
    if (!canManage) return t(($) => $.machine.ops.restart_no_permission);
    if (!isOnline) return t(($) => $.machine.ops.restart_offline);
    return null;
  }, [isLocal, launchedBy, isSandbox, canManage, isOnline, t]);

  const canRestart =
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

  if (!machine.runtimes.length) return null;

  return (
    <section data-testid="machine-ops-section">
      <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        {t(($) => $.machine.ops.section)}
      </h3>
      <div className="space-y-3 rounded-xl border bg-card px-4 py-3">
        {isLocal && primaryId && primary ? (
          <div className="space-y-2">
            <UpdateSection
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
              compact
            />
            <div className="flex flex-wrap items-center gap-2">
              <RestartSection
                runtimeId={primaryId}
                isOnline={isOnline}
                canRestart={canRestart}
              />
              {restartBlockedReason && !canRestart && (
                <span
                  className="text-xs text-muted-foreground"
                  data-testid="machine-ops-restart-reason"
                >
                  {restartBlockedReason}
                </span>
              )}
            </div>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.machine.ops.local_only)}
          </p>
        )}

        <div className="flex flex-wrap items-center gap-2 border-t pt-3">
          {canDeleteComputer ? (
            <MachineDeleteControl
              machine={machine}
              wsId={wsId}
              onDeleted={onDeleted}
              layout="button"
            />
          ) : (
            <>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled
                className="gap-1.5"
                data-testid="delete-computer-button-disabled"
              >
                <Trash2 className="h-3.5 w-3.5" aria-hidden />
                {t(($) => $.machine.delete_computer.button)}
              </Button>
              <span
                className="text-xs text-muted-foreground"
                data-testid="machine-ops-delete-reason"
              >
                {t(($) => $.machine.ops.delete_owner_only)}
              </span>
            </>
          )}
        </div>
      </div>
    </section>
  );
}
