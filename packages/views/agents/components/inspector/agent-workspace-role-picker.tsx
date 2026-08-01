"use client";

import { useState } from "react";
import { ChevronDown } from "lucide-react";
import type { Agent } from "@multica/core/types";
import { RolesDialog } from "../../../settings/components/roles-dialog";
import { useT } from "../../../i18n";
import { CHIP_CLASS } from "./chip";

type AgentWorkspaceRole = Agent["workspace_role"];

/**
 * Agent workspace role (member | admin) — handbook meanings from settings
 * RolesDialog cards (members-roles docs). Editable only for workspace
 * owner/admin; everyone else sees the label + meaning text.
 */
export function AgentWorkspaceRolePicker({
  value,
  canEdit,
  saving = false,
  onChange,
}: {
  value: AgentWorkspaceRole;
  canEdit: boolean;
  saving?: boolean;
  onChange: (role: AgentWorkspaceRole) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const { t: tSettings } = useT("settings");
  const [open, setOpen] = useState(false);

  const label =
    value === "admin"
      ? tSettings(($) => $.members.roles.admin.label)
      : tSettings(($) => $.members.roles.member.label);
  // Frank B + Iris: handbook team-management meanings (not "click to edit").
  const description =
    value === "admin"
      ? t(($) => $.profile_card.role_admin_description)
      : t(($) => $.profile_card.role_member_description);

  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      {canEdit ? (
        <button
          type="button"
          data-testid="agent-workspace-role-trigger"
          className={CHIP_CLASS}
          onClick={() => setOpen(true)}
          disabled={saving}
        >
          <span className="truncate font-medium">{label}</span>
          <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
        </button>
      ) : (
        <span
          data-testid="agent-workspace-role-readonly"
          className="truncate text-xs font-medium"
        >
          {label}
        </span>
      )}
      <p
        data-testid="agent-workspace-role-description"
        className="text-[10px] leading-tight text-muted-foreground"
      >
        {description}
      </p>

      <RolesDialog
        open={open}
        onOpenChange={setOpen}
        mode="select"
        value={value}
        allowedRoles={["admin", "member"]}
        saving={saving}
        title={t(($) => $.profile_card.role_dialog_title)}
        subtitle={t(($) => $.profile_card.role_dialog_subtitle)}
        onSave={async (role) => {
          if (role !== "member" && role !== "admin") return;
          await onChange(role);
          setOpen(false);
        }}
      />
    </div>
  );
}
