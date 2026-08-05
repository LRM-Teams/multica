"use client";

import { useState } from "react";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";
import { DeleteComputerDialog } from "./delete-computer-dialog";
import {
  canDeleteCloudComputerMachine,
  isCloudComputerMachine,
  type RuntimeMachine,
} from "./runtime-machines";

/**
 * LRM-1071 / v5 S4 — Delete computer lives only in the page-bottom Danger Zone.
 */
export function MachineDangerZone({
  machine,
  onDeleted,
}: {
  machine: RuntimeMachine;
  onDeleted?: () => void;
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const isCloud = isCloudComputerMachine(machine);
  if (!machine.runtimes.length && !machine.pendingCloud) return null;

  const canDeleteComputer = isCloud
    ? canDeleteCloudComputerMachine(machine, user?.id)
    : !!user &&
      machine.runtimes.length > 0 &&
      machine.runtimes.every((r) => r.owner_id === user.id);
  const deleteBlockedReason = canDeleteComputer
    ? null
    : t(($) => $.machine.ops.delete_owner_only);

  return (
    <section
      className="rounded-xl border border-destructive/30 bg-destructive/[0.04] p-4"
      data-testid="machine-danger-zone"
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-destructive">
            {t(($) => $.machine.danger_zone.title)}
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {isCloud
              ? t(($) => $.machine.danger_zone.description_cloud)
              : t(($) => $.machine.danger_zone.description)}
          </p>
          {deleteBlockedReason ? (
            <p
              className="mt-1 text-[11px] text-muted-foreground"
              data-testid="machine-ops-delete-reason"
            >
              {deleteBlockedReason}
            </p>
          ) : null}
        </div>
        <Button
          type="button"
          variant="destructive"
          size="sm"
          className="shrink-0"
          disabled={!canDeleteComputer}
          onClick={() => {
            if (canDeleteComputer) setDeleteOpen(true);
          }}
          data-testid="machine-danger-delete"
        >
          {t(($) => $.machine.ops.delete_menu)}
        </Button>
      </div>

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
    </section>
  );
}
