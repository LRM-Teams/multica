"use client";

import { useState } from "react";
import { ShieldCheck, ShieldOff } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent } from "@multica/core/types";
import type { Decision } from "@multica/core/permissions";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
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
import { useT } from "../../i18n/use-t";

/**
 * LRM-1449 — workspace-admin 角色门. Shows an agent's workspace-level role
 * (Member vs Admin) and, for workspace owner/admin viewers, offers a
 * one-step toggle backed by the human route
 * `PATCH /api/workspaces/{id}/agents/{agentId}/role`.
 *
 * Permission is decided server-side (owner/admin only); the frontend mirrors
 * it via `canChangeAgentWorkspaceRole` so non-owner/admin viewers see the
 * role read-only plus a hint instead of a dead control. Agents can be
 * Member or Admin — never Owner (server rejects "owner").
 */
export function AgentWorkspaceRole({
  wsId,
  agent,
  permission,
  onRoleChanged,
}: {
  wsId: string;
  agent: Agent;
  permission: Decision;
  /** Called after a successful role change so the parent can refresh data. */
  onRoleChanged?: () => void;
}) {
  const { t } = useT("agents");
  const isAdmin = agent.workspace_role === "admin";
  const canChange = permission.allowed;
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [pending, setPending] = useState(false);

  const handleConfirm = async () => {
    setPending(true);
    const target: "member" | "admin" = isAdmin ? "member" : "admin";
    try {
      await api.updateAgentWorkspaceRole(wsId, agent.id, target);
      toast.success(t(($) => $.inspector.workspace_role.role_updated_toast));
      onRoleChanged?.();
      // Parent refreshes the agent list (which carries workspace_role).
      setConfirmOpen(false);
    } catch (e) {
      showErrorToast(
        e instanceof Error
          ? e.message
          : t(($) => $.inspector.workspace_role.role_update_failed_toast),
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <div className="flex flex-col border-b px-5 py-4">
      <div className="mb-2 -mx-2 px-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {t(($) => $.inspector.section_workspace_role)}
      </div>
      <div className="flex items-center justify-between gap-2">
        <Badge
          variant={isAdmin ? "default" : "secondary"}
          data-testid="agent-workspace-role-badge"
        >
          {isAdmin ? (
            <ShieldCheck className="h-3 w-3" aria-hidden />
          ) : (
            <ShieldOff className="h-3 w-3" aria-hidden />
          )}
          <span data-testid="agent-workspace-role-value">
            {isAdmin
              ? t(($) => $.inspector.role_admin)
              : t(($) => $.inspector.role_member)}
          </span>
        </Badge>
        {canChange && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            onClick={() => setConfirmOpen(true)}
            data-testid="agent-workspace-role-toggle"
          >
            {isAdmin
              ? t(($) => $.inspector.workspace_role.remove_admin_trigger)
              : t(($) => $.inspector.workspace_role.make_admin_trigger)}
          </Button>
        )}
      </div>
      {!canChange && (
        <p className="mt-2 text-xs text-muted-foreground">
          {t(($) => $.inspector.workspace_role.role_readonly_hint)}
        </p>
      )}

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent data-testid="agent-workspace-role-confirm">
          <AlertDialogHeader>
            <AlertDialogTitle>
              {isAdmin
                ? t(($) => $.inspector.workspace_role.remove_admin_confirm_title)
                : t(($) => $.inspector.workspace_role.make_admin_confirm_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {isAdmin
                ? t(($) => $.inspector.workspace_role.remove_admin_confirm_desc)
                : t(($) => $.inspector.workspace_role.make_admin_confirm_desc)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={pending}>
              {t(($) => $.inspector.workspace_role.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction disabled={pending} onClick={handleConfirm}>
              {t(($) => $.inspector.workspace_role.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
