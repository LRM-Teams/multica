"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { ApiError, api } from "@multica/core/api";
import {
  agentDetailKeys,
  agentDetailOptions,
  memberProfileOptions,
  validateAgentUsername,
  agentFleetRankOptions,
} from "@multica/core/agents";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import {
  canChangeAgentWorkspaceRole,
  useAgentPermissions,
  useCurrentMember,
} from "@multica/core/permissions";
import { useAuthStore } from "@multica/core/auth";
import type { Agent } from "@multica/core/types";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { ActorAvatar } from "../../common/actor-avatar";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { MessageSquare, Pencil } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { AppLink } from "../../navigation/app-link";
import { useOpenDM } from "../../common/use-open-dm";
import { PropRow } from "../../common/prop-row";
import { deriveRuntimeHealth, deriveRuntimeHealthPresentation } from "@multica/core/runtimes";
import { useRuntimeHealthStateLabel } from "../../runtimes/components/shared";
import { ComputerInfoRow } from "./inspector/computer-info-row";
import { AgentWorkspaceRolePicker } from "./inspector/agent-workspace-role-picker";
import { RuntimePicker } from "./inspector/runtime-picker";
import { ModelPicker } from "./inspector/model-picker";
import { ThinkingPropRow } from "./inspector/thinking-prop-row";
import { InlineEditPopover } from "./inline-edit-popover";
import { useUpdateAgent } from "../hooks/use-update-agent";
import { useT } from "../../i18n/use-t";
import { ActorProfileContentLoaded } from "../../common/actor-profile-popover";
import { MemoryGrowthField } from "./memory-growth-field";
import { FleetRankBadge } from "@multica/ui/components/fleet/fleet-class-badge";
import { AgentLifecycleStatusLine } from "./agent-lifecycle-status-line";

interface AgentProfileCardProps {
  agentId: string;
}

export function AgentProfileCard({ agentId }: AgentProfileCardProps) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const { openDM, isPending: openingDM } = useOpenDM();
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  const handleUpdate = useUpdateAgent(wsId);
  const qc = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);
  const { userId: viewerId, role: viewerRole } = useCurrentMember(wsId);
  const canChangeRole = canChangeAgentWorkspaceRole({
    userId: viewerId,
    role: viewerRole,
  });
  const [roleSaving, setRoleSaving] = useState(false);
  const runtimeHealthLabel = useRuntimeHealthStateLabel();

  // LRM-292: panel/card body always from GetAgent — ListAgents is directory only.
  const {
    data: agent,
    isPending: detailPending,
    isError: detailIsError,
    error: detailError,
  } = useQuery({
    ...agentDetailOptions(wsId, agentId),
    enabled: !!agentId,
  });
  const detailForbidden =
    detailIsError &&
    detailError instanceof ApiError &&
    detailError.status === 403;
  const {
    data: identityProfile,
    isPending: identityPending,
  } = useQuery({
    ...memberProfileOptions(wsId, "agent", agentId),
    enabled: !!agentId && detailForbidden,
  });
  // Same permission gate as the detail inspector — owner/admin only. Called
  // unconditionally (its `agent | null` signature handles the loading case)
  // so the early returns below don't violate the rules of hooks.
  const { canEdit } = useAgentPermissions(agent ?? null, wsId);

  const isLoading =
    detailPending || (detailForbidden && identityPending);

  const { data: fleet } = useQuery({
    ...agentFleetRankOptions(wsId, agentId),
    enabled: !!agentId && !!agent,
  });

  if (isLoading) {
    return (
      <div className="flex items-center gap-3">
        <Skeleton className="h-10 w-10 rounded-full" />
        <div className="flex-1 space-y-1.5">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-3 w-20" />
        </div>
      </div>
    );
  }

  if (!agent) {
    if (identityProfile) {
      return <ActorProfileContentLoaded profile={identityProfile} />;
    }
    return (
      <div className="text-xs text-muted-foreground">{t(($) => $.profile_card.unavailable)}</div>
    );
  }

  const owner = agent.owner_id
    ? members.find((m) => m.user_id === agent.owner_id) ?? null
    : null;
  const runtime = runtimes.find((r) => r.id === agent.runtime_id) ?? null;
  // Derived, staleness-aware health instead of the raw `status` column
  // (#10 — "runtime online status" had two divergent sources across the
  // app). Computed once so both dropdowns below agree.
  const runtimeOnline = !!runtime && deriveRuntimeHealth(runtime, Date.now()) === "online";
  const isArchived = !!agent.archived_at;
  const update = (data: Record<string, unknown>) => handleUpdate(agent.id, data);
  // Client-side handle grammar check (mirrors the server validator); the code
  // → message mapping stays here so the shared core validator is i18n-free.
  const usernameError = (v: string): string | null => {
    switch (validateAgentUsername(v)) {
      case "empty":
        return t(($) => $.profile_card.username_error_empty);
      case "too_long":
        return t(($) => $.profile_card.username_error_too_long);
      case "invalid_chars":
        return t(($) => $.profile_card.username_error_invalid);
      default:
        return null;
    }
  };
  // Runtime "version outdated" is an INDEPENDENT axis from online/offline
  // health (kept per Iris/Parker — it currently explains the billing gap).
  // Cloud runtimes never report an outdated local binary.
  const runtimeUpdateHealth =
    agent.runtime_mode !== "cloud" && runtime
      ? deriveRuntimeHealthPresentation(runtime)
      : "ok";

  return (
    // `group` enables the hover-only Detail link on the top-right —
    // it fades in only when the user is hovering the card chrome,
    // staying out of the way during a quick glance.
    <div className="group flex flex-col gap-3 text-left">
      {/* Header — avatar + name + availability on the left, "Detail →" link
          on the right (hover-only). Card stays minimal: only the 3-state
          availability dot is surfaced here; last-task state lives in the
          agents list and the agent detail page. */}
      <div className="flex items-start gap-3">
        <ActorAvatar
          actorType="agent"
          actorId={agentId}
          size={40}
          avatarUrlHint={agent.avatar_url}
          showStatusDot={!isArchived}
          showXpBurst
          profileLink={false}
          className={isArchived ? "opacity-50 grayscale" : undefined}
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <ActorIdentityRow
              identity={agent}
              primaryClassName={`truncate text-sm font-semibold ${
                isArchived ? "text-muted-foreground" : ""
              }`}
              className="min-w-0 shrink"
            />
          </div>
          {/* LRM-248: archived is muted secondary copy; live presence is avatar badge only.
              Lifecycle denser line uses runtime_display_status (not presence). */}
          {isArchived ? (
            <p className="mt-0.5 text-xs text-muted-foreground">
              {t(($) => $.row.archived)}
            </p>
          ) : (
            <AgentLifecycleStatusLine
              status={agent.runtime_display_status}
              className="mt-0.5"
            />
          )}
        </div>
        {!isArchived && (
          <div className="mr-1 mt-0.5 flex shrink-0 items-center gap-2 opacity-0 transition-opacity group-hover:opacity-100">
            <button
              type="button"
              disabled={openingDM}
              onClick={() => void openDM({ peer_type: "agent", peer_id: agent.id })}
              className="inline-flex items-center gap-1 text-xs font-normal text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
            >
              <MessageSquare className="size-3.5" />
              {t(($) => $.profile_card.send_message)}
            </button>
            <AppLink
              href={p.agentDetail(agent.id)}
              className="text-xs font-normal text-brand"
            >
              {t(($) => $.profile_card.detail_link)}
            </AppLink>
          </div>
        )}
      </div>

      {/* Description */}
      {agent.description && (
        <p className="line-clamp-2 text-xs text-muted-foreground">
          {agent.description}
        </p>
      )}

      {/* LRM-304: Memory growth — profile/card only; omitted when zero writes. */}
      <MemoryGrowthField growth={agent.memory_growth} />
      {fleet ? (
        <FleetRankBadge
          classId={fleet.class_id}
          classLabel={fleet.class_label}
          fleetRank={fleet.fleet_rank}
          frozen={fleet.frozen || isArchived}
        />
      ) : null}

      {/* Iris 08-01: INFO (identity + role + computer + owner) then
          RUNTIME CONFIG (pickers). Role sits under username; Computer stays
          immutable info. */}
      <div className="flex flex-col gap-2 text-xs">
        <div className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">
          <h3 className="col-span-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.profile_card.info_section)}
          </h3>
          {/* Username / @handle — the stable ASCII routing handle (`agent.name`),
              distinct from the human display name. Editable by owners/admins
              via the same InlineEditPopover the detail inspector uses for the
              display name; read-only `@handle` for everyone else. */}
          <PropRow label={t(($) => $.profile_card.username_label)} interactive={false}>
            {canEdit.allowed ? (
              <InlineEditPopover
                value={agent.name}
                kind="input"
                title={t(($) => $.profile_card.username_edit_title)}
                placeholder={t(($) => $.profile_card.username_placeholder)}
                validate={usernameError}
                mapSaveError={(e) =>
                  e instanceof ApiError && e.status === 409
                    ? t(($) => $.profile_card.username_taken)
                    : null
                }
                onSave={(v) => update({ username: v.trim() })}
              >
                {(triggerProps) => (
                  <button
                    type="button"
                    {...triggerProps}
                    className="group -mx-1 inline-flex min-w-0 items-center gap-1 rounded px-1 text-left transition-colors hover:bg-accent/50"
                  >
                    <span className="truncate">@{agent.name}</span>
                    <Pencil className="h-3 w-3 shrink-0 text-muted-foreground/0 transition-colors group-hover:text-muted-foreground" />
                  </button>
                )}
              </InlineEditPopover>
            ) : (
              <span className="min-w-0 truncate" title={agent.name}>
                @{agent.name}
              </span>
            )}
          </PropRow>
          <PropRow label={t(($) => $.profile_card.role_label)} interactive={false}>
            <AgentWorkspaceRolePicker
              value={agent.workspace_role === "admin" ? "admin" : "member"}
              canEdit={canChangeRole.allowed}
              saving={roleSaving}
              onChange={async (role) => {
                setRoleSaving(true);
                try {
                  const res = await api.updateAgentWorkspaceRole(
                    wsId,
                    agent.id,
                    role,
                  );
                  const patch = { workspace_role: res.workspace_role } as Pick<
                    Agent,
                    "workspace_role"
                  >;
                  qc.setQueryData<Agent>(
                    agentDetailKeys.detail(wsId, agent.id),
                    (old) => (old ? { ...old, ...patch } : old),
                  );
                  qc.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (old) =>
                    old?.map((a) =>
                      a.id === agent.id ? { ...a, ...patch } : a,
                    ),
                  );
                  toast.success(t(($) => $.profile_card.role_updated));
                } catch (e) {
                  showErrorToast(
                    e instanceof Error
                      ? e.message
                      : t(($) => $.profile_card.role_update_failed),
                  );
                  throw e;
                } finally {
                  setRoleSaving(false);
                }
              }}
            />
          </PropRow>
          <PropRow label={t(($) => $.inspector.prop_computer)} interactive={false}>
            <ComputerInfoRow runtime={runtime} />
          </PropRow>
          {owner && (
            <PropRow label={t(($) => $.profile_card.owner_label)} interactive={false}>
              <span className="min-w-0 truncate" title={owner.name}>
                {owner.name}
              </span>
            </PropRow>
          )}
          <h3 className="col-span-2 mt-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.profile_card.runtime_config_section)}
          </h3>
          <PropRow label={t(($) => $.inspector.prop_runtime)} interactive={false}>
            <div className="flex min-w-0 items-center gap-1.5">
              <RuntimePicker
                value={agent.runtime_id}
                runtimes={runtimes}
                members={members}
                currentUserId={currentUser?.id ?? null}
                canEdit={canEdit.allowed}
                onChange={(id) => update({ runtime_id: id })}
              />
              {runtimeUpdateHealth !== "ok" && (
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                  {runtimeHealthLabel(runtimeUpdateHealth)}
                </span>
              )}
            </div>
          </PropRow>
          <PropRow label={t(($) => $.inspector.prop_model)} interactive={false}>
            <ModelPicker
              runtimeId={agent.runtime_id}
              runtimeOnline={runtimeOnline}
              value={agent.model ?? ""}
              canEdit={canEdit.allowed}
              onChange={(m) => update({ model: m })}
            />
          </PropRow>
          <ThinkingPropRow
            runtimeId={agent.runtime_id}
            runtimeOnline={runtimeOnline}
            model={agent.model ?? ""}
            value={agent.thinking_level ?? ""}
            canEdit={canEdit.allowed}
            onChange={(v) => update({ thinking_level: v })}
          />
        </div>
        {/* Truthfulness hint (#527, Iris): editing runtime/model/thinking here
            configures the NEXT run — it does not retarget a task already
            executing. Shown only to editors, since only they can change it. */}
        {canEdit.allowed && (
          <p className="text-[10px] leading-tight text-muted-foreground">
            {t(($) => $.execution_config.applies_next_run)}
          </p>
        )}
        {agent.skills.length > 0 && (
          <SkillsRow skills={agent.skills.map((s) => s.name)} />
        )}
      </div>
    </div>
  );
}

function SkillsRow({ skills }: { skills: string[] }) {
  const { t } = useT("agents");
  const visible = skills.slice(0, 3);
  const overflow = skills.length - visible.length;
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-12 shrink-0 text-muted-foreground">{t(($) => $.profile_card.skills_label)}</span>
      <div className="flex min-w-0 flex-wrap gap-1">
        {visible.map((s) => (
          <span
            key={s}
            className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground"
          >
            {s}
          </span>
        ))}
        {overflow > 0 && (
          <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
            +{overflow}
          </span>
        )}
      </div>
    </div>
  );
}
