"use client";

import { useState } from "react";
import { Box, Camera, Loader2, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useDeleteSandboxSnapshotMutation } from "@multica/core/sandboxes/mutations";
import { sandboxNodeSnapshotsOptions } from "@multica/core/sandboxes/queries";
import type { SandboxSnapshot } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
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
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../../i18n/use-t";

export function NodeSnapshotsPanel({
  nodeId,
  onCreateSandbox,
}: {
  nodeId: string;
  onCreateSandbox: (snapshot: SandboxSnapshot) => void;
}) {
  const { t } = useT("layout");
  const wsId = useWorkspaceId();
  const { data, isLoading, error, refetch } = useQuery(sandboxNodeSnapshotsOptions(nodeId));
  const del = useDeleteSandboxSnapshotMutation(wsId, nodeId);
  const [pendingDelete, setPendingDelete] = useState<SandboxSnapshot | null>(null);
  const snapshots = data ?? [];

  const handleDelete = async () => {
    if (!pendingDelete) return;
    try {
      await del.mutateAsync(pendingDelete.id);
      toast.success(t(($) => $.sandboxes_page.snapshot_delete_success));
      setPendingDelete(null);
    } catch (e) {
      showErrorToast(
        e instanceof Error ? e.message : t(($) => $.sandboxes_page.snapshot_delete_failed),
      );
    }
  };

  if (isLoading) {
    return (
      <div className="space-y-3 p-5">
        <Skeleton className="h-4 w-48" />
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-16 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
        <p className="text-sm text-destructive">
          {error instanceof Error
            ? error.message
            : t(($) => $.sandboxes_page.snapshots_load_failed)}
        </p>
        <Button type="button" size="sm" variant="outline" onClick={() => void refetch()}>
          {t(($) => $.sandboxes_page.templates_retry)}
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4 p-5">
      <div>
        <h3 className="text-sm font-semibold">{t(($) => $.sandboxes_page.snapshots_title)}</h3>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(($) => $.sandboxes_page.snapshots_description)}
        </p>
      </div>

      {snapshots.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-lg border border-dashed px-6 py-14 text-center">
          <Camera className="mb-3 size-8 text-muted-foreground/40" />
          <p className="text-sm font-medium">{t(($) => $.sandboxes_page.snapshots_empty_title)}</p>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            {t(($) => $.sandboxes_page.snapshots_empty_description)}
          </p>
        </div>
      ) : (
        <div className="divide-y rounded-lg border">
          {snapshots.map((snapshot) => (
            <SnapshotRow
              key={snapshot.id}
              snapshot={snapshot}
              deleting={del.isPending && del.variables === snapshot.id}
              onCreateSandbox={() => onCreateSandbox(snapshot)}
              onDelete={() => setPendingDelete(snapshot)}
            />
          ))}
        </div>
      )}

      <AlertDialog
        open={!!pendingDelete}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.sandboxes_page.snapshot_delete_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.sandboxes_page.snapshot_delete_dialog.description, {
                name: pendingDelete?.name ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.sandboxes_page.snapshot_delete_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => void handleDelete()}>
              {t(($) => $.sandboxes_page.snapshot_delete_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function SnapshotRow({
  snapshot,
  deleting,
  onCreateSandbox,
  onDelete,
}: {
  snapshot: SandboxSnapshot;
  deleting: boolean;
  onCreateSandbox: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("layout");
  const busy = snapshot.status === "creating" || snapshot.status === "deleting";
  const canDelete = !busy && snapshot.status !== "deleting";
  const canCreate =
    snapshot.status === "ready" && snapshot.cube_snapshot_id.trim().length > 0;

  return (
    <div className="flex items-start justify-between gap-4 px-4 py-3">
      <div className="min-w-0">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate text-sm font-medium">{snapshot.name}</span>
          <Badge variant="outline">{snapshot.status}</Badge>
        </div>
        {snapshot.description ? (
          <p className="mt-1 text-sm text-muted-foreground whitespace-pre-wrap break-words">
            {snapshot.description}
          </p>
        ) : null}
        <div className="mt-1 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
          {snapshot.cube_snapshot_id ? (
            <span className="truncate font-mono">{snapshot.cube_snapshot_id}</span>
          ) : (
            <span>{t(($) => $.sandboxes_page.snapshot_pending_cube_id)}</span>
          )}
          {snapshot.created_at ? <span>{formatTime(snapshot.created_at)}</span> : null}
        </div>
        {snapshot.error ? <p className="mt-1 text-xs text-destructive">{snapshot.error}</p> : null}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={!canCreate}
          onClick={onCreateSandbox}
        >
          <Box className="mr-2 size-3.5" />
          {t(($) => $.sandboxes_page.create_from_snapshot_action)}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={!canDelete || deleting}
          onClick={onDelete}
        >
          {deleting || snapshot.status === "deleting" ? (
            <Loader2 className="mr-2 size-3.5 animate-spin" />
          ) : (
            <Trash2 className="mr-2 size-3.5" />
          )}
          {t(($) => $.sandboxes_page.delete_action)}
        </Button>
      </div>
    </div>
  );
}

function formatTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
