"use client";

import { useRef, useState } from "react";
import { ShieldCheck, ShieldOff } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent } from "@multica/core/types";
import type { Decision } from "@multica/core/permissions";
import { api } from "@multica/core/api";
import {
  PickerItem,
  PropertyPicker,
} from "../../issues/components/pickers/property-picker";
import { CHIP_CLASS } from "./inspector/chip";
import { EditPencil, InspectorField } from "./inspector/inspector-field";
import { useT } from "../../i18n/use-t";

/**
 * LRM-1449 — workspace-admin 角色门. Shows an agent's workspace-level role
 * (Member vs Admin) and, for workspace owner/admin viewers, offers a
 * one-step change backed by the human route
 * `PATCH /api/workspaces/{id}/agents/{agentId}/role`.
 *
 * Permission is decided server-side (owner/admin only); the frontend mirrors
 * it via `canChangeAgentWorkspaceRole` so non-owner/admin viewers see the
 * role read-only plus a hint instead of a dead control. Agents can be
 * Member or Admin — never Owner (server rejects "owner").
 *
 * A two-value choice used to cost a button plus a confirm dialog (Frank,
 * 2026-08-21). It is a picker now, like every other single value in the
 * panel: what Admin grants lives in the label's hint, where it can be read
 * *before* choosing rather than in a dialog that interrupts afterwards. The
 * change is one step to undo, so there is nothing here a confirm would save.
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
  const [open, setOpen] = useState(false);
  // A re-entrancy guard, not render state — a ref keeps a redundant render
  // off the path between click and PATCH.
  const pending = useRef(false);

  const roleLabel = (admin: boolean) =>
    admin ? t(($) => $.inspector.role_admin) : t(($) => $.inspector.role_member);

  const select = async (target: "member" | "admin") => {
    setOpen(false);
    if ((target === "admin") === isAdmin || pending.current) return;
    pending.current = true;
    try {
      await api.updateAgentWorkspaceRole(wsId, agent.id, target);
      toast.success(t(($) => $.inspector.workspace_role.role_updated_toast));
      // Parent refreshes the agent list (which carries workspace_role).
      onRoleChanged?.();
    } catch (e) {
      showErrorToast(
        e instanceof Error
          ? e.message
          : t(($) => $.inspector.workspace_role.role_update_failed_toast),
      );
    } finally {
      pending.current = false;
    }
  };

  const RoleIcon = isAdmin ? ShieldCheck : ShieldOff;

  return (
    <div className="flex flex-col border-b px-5 py-4">
      <InspectorField
        label={t(($) => $.inspector.section_workspace_role)}
        hint={
          canChange
            ? t(($) => $.inspector.workspace_role.role_hint)
            : t(($) => $.inspector.workspace_role.role_readonly_hint)
        }
      >
        {canChange ? (
          <PropertyPicker
            open={open}
            onOpenChange={setOpen}
            width="w-auto min-w-[16rem]"
            align="start"
            tooltip={roleLabel(isAdmin)}
            triggerRender={
              <button
                type="button"
                className={CHIP_CLASS}
                aria-label={roleLabel(isAdmin)}
                data-testid="agent-workspace-role-toggle"
              />
            }
            trigger={
              <>
                <RoleIcon className="size-3 shrink-0" aria-hidden />
                <span data-testid="agent-workspace-role-value">
                  {roleLabel(isAdmin)}
                </span>
                <EditPencil />
              </>
            }
          >
            <PickerItem
              selected={!isAdmin}
              onClick={() => void select("member")}
            >
              <ShieldOff className="size-4 shrink-0" aria-hidden />
              <span className="text-sm">{roleLabel(false)}</span>
            </PickerItem>
            <PickerItem
              selected={isAdmin}
              onClick={() => void select("admin")}
            >
              <ShieldCheck className="size-4 shrink-0" aria-hidden />
              <span className="text-sm">{roleLabel(true)}</span>
            </PickerItem>
          </PropertyPicker>
        ) : (
          <span
            className="inline-flex items-center gap-1.5"
            data-testid="agent-workspace-role-value"
          >
            <RoleIcon className="size-3 shrink-0 text-muted-foreground" aria-hidden />
            {roleLabel(isAdmin)}
          </span>
        )}
      </InspectorField>
    </div>
  );
}
