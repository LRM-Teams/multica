"use client";

import { useState } from "react";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useRemoveComputerWorkspaceBinding } from "@multica/core/runtimes/mutations";
import { Button } from "@multica/ui/components/ui/button";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
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
  const [removeBindingOpen, setRemoveBindingOpen] = useState(false);
  const removeBinding = useRemoveComputerWorkspaceBinding(wsId);

  const isCloud = isCloudComputerMachine(machine);
  // An explicit Workspace connection remains manageable even when this
  // Workspace has zero Agent runtime rows. Only the synthetic empty local
  // placeholder lacks both a Computer id and a destructive action.
  if (!machine.runtimes.length && !machine.pendingCloud && !machine.daemonId) {
    return null;
  }

  const canDeleteComputer = isCloud
    ? canDeleteCloudComputerMachine(machine, user?.id)
    : !!user &&
      machine.runtimes.length > 0 &&
      machine.runtimes.every((r) => r.owner_id === user.id);
  const deleteBlockedReason = canDeleteComputer
    ? null
    : t(($) => $.machine.ops.delete_owner_only);
  const canRemoveBinding =
    !isCloud &&
    !!machine.daemonId &&
    !!user &&
    (machine.ownerUserId
      ? machine.ownerUserId === user.id
      : machine.runtimes.length > 0 &&
        machine.runtimes.every((r) => r.owner_id === user.id));

  const handleRemoveBinding = async () => {
    if (!machine.daemonId || !canRemoveBinding) return;
    try {
      const result = await removeBinding.mutateAsync(machine.daemonId);
      if (!result.ok || result.workspace_id !== wsId || !result.kept_local_data) {
        throw new Error(t(($) => $.machine.remove_binding.invalid_response));
      }
      toast.success(t(($) => $.machine.remove_binding.success));
      setRemoveBindingOpen(false);
      onDeleted?.();
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : String(error));
    }
  };

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
        <div className="flex shrink-0 flex-col gap-2 sm:items-end">
          {canRemoveBinding ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={() => setRemoveBindingOpen(true)}
              data-testid="machine-remove-binding"
            >
              {t(($) => $.machine.remove_binding.button)}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="destructive"
            size="sm"
            disabled={!canDeleteComputer}
            onClick={() => {
              if (canDeleteComputer) setDeleteOpen(true);
            }}
            data-testid="machine-danger-delete"
          >
            {t(($) => $.machine.ops.delete_menu)}
          </Button>
        </div>
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

      <AlertDialog open={removeBindingOpen} onOpenChange={setRemoveBindingOpen}>
        <AlertDialogContent>
          <div className="px-5 pb-4 pt-5">
            <AlertDialogTitle>
              {t(($) => $.machine.remove_binding.title)}
            </AlertDialogTitle>
            <AlertDialogDescription className="mt-2">
              {t(($) => $.machine.remove_binding.description, {
                name: machine.title,
              })}
            </AlertDialogDescription>
          </div>
          <div className="flex flex-col-reverse gap-2 border-t bg-muted/25 px-5 py-3 sm:flex-row sm:justify-end">
            <Button
              type="button"
              variant="outline"
              onClick={() => setRemoveBindingOpen(false)}
              disabled={removeBinding.isPending}
            >
              {t(($) => $.machine.remove_binding.cancel)}
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={() => void handleRemoveBinding()}
              disabled={removeBinding.isPending}
              data-testid="machine-remove-binding-confirm"
            >
              {removeBinding.isPending
                ? t(($) => $.machine.remove_binding.removing)
                : t(($) => $.machine.remove_binding.confirm)}
            </Button>
          </div>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
