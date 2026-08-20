"use client";

import {
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Camera, Loader2, Pencil, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Button } from "@multica/ui/components/ui/button";
import type {
  Agent,
  AgentRuntime,
  MemberWithUser,
} from "@multica/core/types";
import {
  AGENT_DESCRIPTION_MAX_LENGTH,
  type AgentPresence,
} from "@multica/core/agents";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import type { Decision } from "@multica/core/permissions";
import { useTimeAgo } from "../../i18n";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
} from "@multica/core/identity";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { PropRow } from "../../common/prop-row";
import { InlineFieldEditor } from "./inline-field-editor";
import { useT } from "../../i18n";
import { ComputerInfoRow } from "./inspector/computer-info-row";
import { ConcurrencyPicker } from "./inspector/concurrency-picker";
import { ModelPicker } from "./inspector/model-picker";
import { RuntimePicker } from "./inspector/runtime-picker";
import { SkillAttach } from "./inspector/skill-attach";
import { ThinkingPropRow } from "./inspector/thinking-prop-row";
import { RuntimeConfigDialog } from "./runtime-config-dialog";
import { LarkAgentBindButton } from "../../settings/components/lark-tab";
import { AgentWorkspaceRole } from "./agent-workspace-role";
import { AgentActivityStatus } from "./agent-activity-list-item";

interface InspectorProps {
  agent: Agent;
  runtime: AgentRuntime | null;
  owner: MemberWithUser | null;
  presence: AgentPresence | null | undefined;
  // Below: needed for inline edit. The inspector now owns the editing surface
  // (no Settings tab anymore), so the parent has to pass through everything
  // a write needs.
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  /**
   * Computed by the parent via `useAgentPermissions(agent).canEdit.allowed`.
   * When false the inspector renders all editable surfaces as static
   * read-only displays — pickers become text/badges, name/description lose
   * their pencil affordance, the avatar is no longer clickable, and the
   * "Attach skill" trigger is hidden. Mirrors the backend gate at
   * `server/internal/handler/agent.go:519-535`.
   */
  canEdit: boolean;
  /** LRM-1449 — workspace-admin role change gate (owner/admin only). */
  canChangeRole: Decision;
  wsId: string;
  /** LRM-1449 — called after a role change so the page can refresh the agent. */
  onRoleChanged?: () => void;
  onUpdate: (id: string, data: Record<string, unknown>) => Promise<void>;
  /**
   * Focus the overview pane's Integrations tab. The inspector's Lark status
   * row is read-only and deep-links here; Manage / Disconnect live in the
   * tab so the destructive action exists in exactly one place.
   */
  onShowIntegrations: () => void;
}

/**
 * Left 320px column of the agent detail page. Holds the agent's identity card
 * (avatar / name / description / status), inline-editable properties, and
 * skills.
 *
 * **All editing happens here** — there is no separate Settings tab. The
 * trade-off is that the inspector carries some weight (4 inline pickers plus
 * 3 popovers for name/description/avatar), but it eliminates the "see vs
 * edit" mode split that the previous Settings tab created. Users no longer
 * have to switch tabs and hunt for the field they were already looking at.
 */
export function AgentDetailInspector({
  agent,
  runtime,
  owner,
  presence,
  runtimes,
  members,
  currentUserId,
  canEdit,
  canChangeRole,
  wsId,
  onRoleChanged,
  onUpdate,
  onShowIntegrations,
}: InspectorProps) {
  const { t } = useT("agents");
  const timeAgo = useTimeAgo();
  const update = (data: Record<string, unknown>) => onUpdate(agent.id, data);
  const [runtimeDialogOpen, setRuntimeDialogOpen] = useState(false);

  return (
    <aside className="flex w-full flex-col rounded-lg border bg-background md:h-full md:min-h-0 md:overflow-y-auto">
      {/* Identity */}
      <div className="flex flex-col gap-3 border-b px-5 pb-5 pt-5">
        <AvatarEditor
          agent={agent}
          presence={presence ?? "loading"}
          canEdit={canEdit}
          onUpdate={update}
        />
        <NameAndDescription
          agent={agent}
          canEdit={canEdit}
          onUpdate={update}
        />
        {/* Live status stays on the avatar. Runtime work and failures belong
            to the Activity timeline rather than a second persistent status. */}
        {agent.archived_at ? (
          <span className="text-xs text-muted-foreground">
            {t(($) => $.row.archived)}
          </span>
        ) : (
          <AgentActivityStatus
            agentId={agent.id}
            presence={presence ?? "loading"}
            className="max-w-none"
            testId="agent-inspector-current-status"
          />
        )}
        {agent.start_intent_status === "failed" ? (
          <div
            className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive"
            data-testid="agent-start-intent-failure"
          >
            <div className="flex items-start gap-2">
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
              <p className="min-w-0 flex-1">
                {t(($) => $.inspector.start_intent_failure, {
                  code: agent.start_intent_failure_code || "unknown",
                })}
              </p>
              {canEdit ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-7 shrink-0 border-destructive/30 bg-background px-2 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive"
                  onClick={() => setRuntimeDialogOpen(true)}
                >
                  {t(($) => $.inspector.start_intent_review)}
                </Button>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>

      {/* Properties — editable when canEdit. When the current user lacks
          permission, each picker self-renders a static read-only display so
          the value is visible but not interactive. */}
      <Section label={t(($) => $.inspector.section_properties)}>
        {/* LRM-1351: runtime/model/thinking open one Dialog; summary shows
            effective values only. Frank pencil lock: trailing pencil only —
            summary chips are not a row-wide click target. */}
        <PropRow label={t(($) => $.inspector.prop_computer)} interactive={false}>
          <ComputerInfoRow runtime={runtime} />
        </PropRow>
        <PropRow label={t(($) => $.inspector.prop_runtime)} interactive={false}>
          <span className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
            <RuntimePicker
              value={agent.runtime_id}
              runtimes={runtimes}
              members={members}
              currentUserId={currentUserId}
              canEdit={false}
              onChange={() => {}}
            />
            <ModelPicker
              runtimeId={agent.runtime_id}
              value={agent.model ?? ""}
              canEdit={false}
              onChange={() => {}}
            />
          </span>
          {canEdit ? (
            <button
              type="button"
              className="ml-auto inline-flex shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onClick={() => setRuntimeDialogOpen(true)}
              aria-label={t(($) => $.runtime_config.edit_trigger_aria)}
              data-testid="agent-inspector-runtime-config-edit"
            >
              <Pencil className="h-3.5 w-3.5" aria-hidden />
            </button>
          ) : null}
        </PropRow>
        <ThinkingPropRow
          runtimeId={agent.runtime_id}
          model={agent.model ?? ""}
          value={agent.thinking_level ?? ""}
          canEdit={false}
          onChange={() => {}}
        />
        <PropRow label={t(($) => $.inspector.prop_concurrency)} interactive={false}>
          <ConcurrencyPicker
            value={agent.max_concurrent_tasks}
            canEdit={canEdit}
            onChange={(n) => update({ max_concurrent_tasks: n })}
          />
        </PropRow>
        {canEdit ? (
          <RuntimeConfigDialog
            agent={agent}
            open={runtimeDialogOpen}
            onOpenChange={setRuntimeDialogOpen}
            runtimes={runtimes}
            members={members}
            currentUserId={currentUserId}
            onSave={update}
          />
        ) : null}
      </Section>

      {/* Details — read-only (no hover, no chip styling — these aren't clickable) */}
      <Section label={t(($) => $.inspector.section_details)}>
        {owner && (
          <PropRow label={t(($) => $.inspector.prop_owner)} interactive={false}>
            <span className="flex min-w-0 items-center gap-1.5">
              <ActorAvatar
                actorType="member"
                actorId={owner.user_id}
                size={14}
              />
              <span className="truncate">{owner.name}</span>
            </span>
          </PropRow>
        )}
        <PropRow label={t(($) => $.inspector.prop_created)} interactive={false}>
          <span className="text-muted-foreground">
            {timeAgo(agent.created_at)}
          </span>
        </PropRow>
        <PropRow label={t(($) => $.inspector.prop_updated)} interactive={false}>
          <span className="text-muted-foreground">
            {timeAgo(agent.updated_at)}
          </span>
        </PropRow>
      </Section>

      {/* LRM-1449 — workspace-admin role (Member/Admin). Standalone block so
          the owner/admin-only toggle is a full-width control, not a cramped
          PropRow value. */}
      <AgentWorkspaceRole
        wsId={wsId}
        agent={agent}
        permission={canChangeRole}
        onRoleChanged={onRoleChanged}
      />

      {/* Skills */}
      <div className="flex flex-col border-b px-5 py-4">
        <div className="mb-2 flex items-center gap-2">
          <span className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
            {t(($) => $.inspector.section_skills)}
          </span>
          <span className="font-mono text-[10px] tabular-nums text-muted-foreground/70">
            {agent.skills.length}
          </span>
        </div>
        <div className="flex flex-wrap gap-1">
          {agent.skills.map((s) => (
            <span
              key={s.id}
              className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-[10px] font-medium text-muted-foreground"
            >
              {s.name}
            </span>
          ))}
          <SkillAttach agent={agent} canEdit={canEdit} />
        </div>
      </div>

      {/* Integrations — surfaces external-channel bind entry points
          (Lark Bot today; Slack / Discord in the future). The bind
          button self-hides when the server-side device-flow install
          capability gate is closed, so this section may render empty
          on deployments without a configured Lark app — that's
          intentional and matches the "don't surface a flow that will
          fail" guarantee. We only mount it for editors: viewers
          shouldn't see a CTA they can't action. */}
      {canEdit && (
        <div className="flex flex-col px-5 py-4">
          <div className="mb-2 flex items-center gap-2">
            <span className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
              {t(($) => $.inspector.section_integrations)}
            </span>
          </div>
          <div className="flex flex-wrap gap-2">
            <LarkAgentBindButton
              agentId={agent.id}
              agentName={agent.name}
              onShowConnectedDetails={onShowIntegrations}
            />
          </div>
        </div>
      )}
    </aside>
  );
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

function Section({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="border-b px-5 py-4">
      <div className="mb-1 -mx-2 px-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">
        {children}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Identity — avatar / name / description editors
// ---------------------------------------------------------------------------

function AvatarEditor({
  agent,
  presence,
  canEdit,
  onUpdate,
}: {
  agent: Agent;
  presence: AgentPresence | "loading";
  canEdit: boolean;
  onUpdate: (data: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { upload, uploading } = useFileUpload(api);

  if (!canEdit) {
    return (
      <div className="h-14 w-14 shrink-0 overflow-hidden rounded-lg">
        <ActorAvatar
          actorType="agent"
          actorId={agent.id}
          size={56}
          className="rounded-none"
          showStatusDot={!agent.archived_at}
          agentPresence={presence}
        />
      </div>
    );
  }

  const handleFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = "";
    try {
      const result = await upload(file);
      if (!result) return;
      await onUpdate({ avatar_selection: { kind: "uploaded", attachment_id: result.id } });
      toast.success(t(($) => $.inspector.avatar_updated_toast));
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : t(($) => $.inspector.avatar_upload_failed_toast));
    }
  };

  return (
    <>
      <button
        type="button"
        // rounded-lg matches the standard agent avatar treatment used in
        // list rows. Avoid rounded-full — circles are reserved for humans.
        className="group relative h-14 w-14 shrink-0 overflow-hidden rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => fileInputRef.current?.click()}
        disabled={uploading}
        aria-label={t(($) => $.inspector.change_avatar_aria)}
      >
        <ActorAvatar
          actorType="agent"
          actorId={agent.id}
          size={56}
          className="rounded-none"
          showStatusDot={!agent.archived_at}
          agentPresence={presence}
        />
        <div className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
          {uploading ? (
            <Loader2 className="h-4 w-4 animate-spin text-white" />
          ) : (
            <Camera className="h-4 w-4 text-white" />
          )}
        </div>
      </button>
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={handleFile}
      />
    </>
  );
}

function NameAndDescription({
  agent,
  canEdit,
  onUpdate,
}: {
  agent: Agent;
  canEdit: boolean;
  onUpdate: (data: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const displayName = resolveActorDisplayName(agent, agent.id);
  const handleLabel = formatActorHandleLabel(resolveActorHandle(agent));

  if (!canEdit) {
    return (
      <div className="flex flex-col gap-1">
        <ActorIdentityRow
          identity={agent}
          agentHonorLevel={agent.honor_level}
          primaryClassName="text-base font-semibold leading-tight"
          className="min-w-0"
        />
        {agent.description ? (
          <span className="text-xs leading-relaxed text-muted-foreground">
            {agent.description}
          </span>
        ) : (
          <span className="text-xs italic leading-relaxed text-muted-foreground/50">
            {t(($) => $.inspector.no_description_placeholder)}
          </span>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1">
      <InlineFieldEditor
        value={displayName}
        onSave={(v) => onUpdate({ display_name: v.trim() })}
        kind="input"
        label={t(($) => $.inspector.display_name_title)}
        placeholder={t(($) => $.inspector.display_name_placeholder)}
        validate={(v) => (v.trim().length > 0 ? null : t(($) => $.inspector.display_name_required))}
        displayClassName="text-base font-semibold leading-tight"
        testId="agent-inspector-display-name"
      />
      {handleLabel ? (
        <span className="text-xs leading-tight text-muted-foreground">{handleLabel}</span>
      ) : null}

      <InlineFieldEditor
        value={agent.description ?? ""}
        onSave={(v) => onUpdate({ description: v })}
        kind="textarea"
        label={t(($) => $.inspector.edit_description_title)}
        placeholder={t(($) => $.inspector.description_placeholder)}
        emptyLabel={t(($) => $.inspector.no_description_placeholder)}
        maxLength={AGENT_DESCRIPTION_MAX_LENGTH}
        displayClassName="text-xs leading-relaxed text-muted-foreground"
        testId="agent-inspector-description"
      />
    </div>
  );
}
