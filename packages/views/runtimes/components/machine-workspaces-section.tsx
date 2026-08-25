"use client";

import { useState, type ReactNode } from "react";
import { Folder, Loader2, RefreshCw, Trash2 } from "lucide-react";
import type {
  RuntimeAgentWorkspace,
  RuntimeAgentWorkspacesResponse,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n/use-t";
import {
  DeleteAgentWorkspaceDialog,
  type DeleteAgentWorkspaceTarget,
} from "./delete-agent-workspace-dialog";
import {
  workspaceDisplayName,
  workspaceDisplayPath,
  workspaceRowStatus,
  type WorkspaceRowStatus,
} from "./machine-workspaces";

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h2 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
      {children}
    </h2>
  );
}

function statusBadgeClass(status: WorkspaceRowStatus): string {
  switch (status) {
    case "active":
      return "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400";
    case "archived":
      return "border-border bg-muted text-muted-foreground";
    case "orphaned":
      return "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-400";
  }
}

function statusLabelKey(status: WorkspaceRowStatus) {
  switch (status) {
    case "active":
      return "workspace_status_active" as const;
    case "archived":
      return "workspace_status_archived" as const;
    case "orphaned":
      return "workspace_status_orphaned" as const;
  }
}

export type MachineWorkspacesSectionProps = {
  machineOnline: boolean;
  primaryRuntimeId: string | null;
  canUpdate: boolean;
  scanned: boolean;
  loading: boolean;
  data: RuntimeAgentWorkspacesResponse | undefined;
  deletePending: boolean;
  onScan: () => void;
  onDelete: (dirName: string) => void;
};

/**
 * LRM-1148 / LRM-1095 knife 1 — Agent Workspaces list on Computer detail.
 * Renders name + status badge + path only (no metrics placeholders).
 */
export function MachineWorkspacesSection({
  machineOnline,
  primaryRuntimeId,
  canUpdate,
  scanned,
  loading,
  data,
  deletePending,
  onScan,
  onDelete,
}: MachineWorkspacesSectionProps) {
  const { t } = useT("runtimes");
  const [confirmTarget, setConfirmTarget] =
    useState<DeleteAgentWorkspaceTarget | null>(null);

  const scanDisabled =
    !primaryRuntimeId || !machineOnline || loading;
  const scanDisabledReason = !primaryRuntimeId
    ? t(($) => $.machine.scan_workspaces_offline)
    : !machineOnline
      ? t(($) => $.machine.scan_workspaces_offline)
      : null;

  const status = data?.status;
  const items = data?.items ?? [];
  const truncated = !!data?.truncated;

  let body: ReactNode;
  if (!scanned) {
    body = (
      <p
        className="px-1 text-sm text-muted-foreground"
        data-testid="machine-workspaces-idle"
      >
        {t(($) => $.machine.scan_workspaces_idle)}
      </p>
    );
  } else if (loading) {
    body = (
      <div
        className="flex items-center gap-2 px-1 py-3 text-sm text-muted-foreground"
        data-testid="machine-workspaces-loading"
      >
        <Loader2 className="h-4 w-4 animate-spin" />
        {t(($) => $.machine.scan_workspaces)}
      </div>
    );
  } else if (status === "offline") {
    body = (
      <p
        className="px-1 text-sm text-muted-foreground"
        data-testid="machine-workspaces-offline"
      >
        {t(($) => $.machine.scan_workspaces_offline)}
      </p>
    );
  } else if (status === "error" || status === "missing") {
    body = (
      <p
        className="px-1 text-sm text-muted-foreground"
        data-testid="machine-workspaces-error"
      >
        {t(($) => $.machine.scan_workspaces_error)}
      </p>
    );
  } else if (items.length === 0) {
    body = (
      <p
        className="px-1 text-sm text-muted-foreground"
        data-testid="machine-workspaces-empty"
      >
        {t(($) => $.machine.scan_workspaces_empty)}
      </p>
    );
  } else {
    body = (
      <div className="overflow-hidden rounded-xl border bg-card">
        {items.map((ws, idx) => (
          <WorkspaceRow
            key={ws.dir_name}
            ws={ws}
            showBorder={idx < items.length - 1}
            canUpdate={canUpdate}
            deletePending={deletePending}
            onRequestDelete={(target) => setConfirmTarget(target)}
          />
        ))}
      </div>
    );
  }

  return (
    <section data-testid="machine-workspaces-section">
      <div className="mb-2 flex items-center justify-between gap-3 px-1">
        <SectionTitle>{t(($) => $.machine.workspaces_section)}</SectionTitle>
        <div className="flex min-w-0 items-center gap-2">
          {scanDisabled && scanDisabledReason && !loading ? (
            <span className="hidden truncate text-[11px] text-muted-foreground sm:inline">
              {scanDisabledReason}
            </span>
          ) : null}
          <Button
            type="button"
            variant="outline"
            size="xs"
            className="h-6 shrink-0 gap-1 px-2 text-[11px]"
            onClick={onScan}
            disabled={scanDisabled}
            title={scanDisabledReason ?? undefined}
            data-testid="machine-scan-workspaces"
          >
            {loading ? (
              <Loader2 className="h-3 w-3 animate-spin" />
            ) : (
              <RefreshCw className="h-3 w-3" />
            )}
            {t(($) => $.machine.scan_workspaces)}
          </Button>
        </div>
      </div>

      {body}

      {scanned && !loading && truncated ? (
        <p
          className="mt-2 px-1 text-[11px] text-muted-foreground"
          data-testid="machine-workspaces-truncated"
        >
          {t(($) => $.machine.workspaces_truncated)}
        </p>
      ) : null}

      <DeleteAgentWorkspaceDialog
        target={confirmTarget}
        pending={deletePending}
        onOpenChange={(open) => {
          if (!open) setConfirmTarget(null);
        }}
        onConfirm={(dirName) => {
          onDelete(dirName);
          setConfirmTarget(null);
        }}
      />
    </section>
  );
}

function WorkspaceRow({
  ws,
  showBorder,
  canUpdate,
  deletePending,
  onRequestDelete,
}: {
  ws: RuntimeAgentWorkspace;
  showBorder: boolean;
  canUpdate: boolean;
  deletePending: boolean;
  onRequestDelete: (target: DeleteAgentWorkspaceTarget) => void;
}) {
  const { t } = useT("runtimes");
  const status = workspaceRowStatus(ws);
  const name = workspaceDisplayName(ws);
  const path = workspaceDisplayPath(ws.rel_path);
  const statusLabel = t(($) => $.machine[statusLabelKey(status)]);
  const deleteLabel = t(($) => $.machine.workspace_delete_aria, { name });
  const noPermission = t(($) => $.machine.workspace_delete_no_permission);

  return (
    <div
      className={cn(
        "flex items-start gap-3 px-4 py-3",
        showBorder && "border-b",
      )}
      data-testid={`machine-workspace-row-${ws.dir_name}`}
      aria-label={[name, statusLabel, path].filter(Boolean).join(", ")}
    >
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span
            className={cn(
              "truncate text-sm font-semibold text-foreground",
              status !== "active" && !ws.agent_name && "font-mono text-[12.5px] font-medium text-muted-foreground",
            )}
          >
            {name}
          </span>
          <span
            className={cn(
              "inline-flex shrink-0 items-center rounded-md border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide",
              statusBadgeClass(status),
            )}
            data-testid={`machine-workspace-status-${status}`}
          >
            {statusLabel}
          </span>
        </div>
        <div className="mt-1 flex min-w-0 items-center gap-1.5 text-muted-foreground">
          <Folder className="h-3.5 w-3.5 shrink-0" aria-hidden />
          <Tooltip>
            <TooltipTrigger render={<span className="truncate font-mono text-[11px]" />}>
              {path}
            </TooltipTrigger>
            <TooltipContent side="top">{ws.rel_path}</TooltipContent>
          </Tooltip>
        </div>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        className={cn(
          "mt-0.5 shrink-0 text-muted-foreground",
          canUpdate && "hover:text-destructive focus-visible:text-destructive",
        )}
        disabled={deletePending || !canUpdate}
        title={canUpdate ? deleteLabel : noPermission}
        aria-label={canUpdate ? deleteLabel : noPermission}
        data-testid={`machine-workspace-delete-${ws.dir_name}`}
        onClick={() => {
          if (!canUpdate) return;
          onRequestDelete({
            dirName: ws.dir_name,
            displayName: name,
            status,
            displayPath: path,
          });
        }}
      >
        <Trash2 className="h-4 w-4" />
      </Button>
    </div>
  );
}
