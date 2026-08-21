"use client";

import { useState } from "react";
import { Camera, Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Agent, AgentAvatarSelection } from "@multica/core/types";
import { agentDetailKeys } from "@multica/core/agents";
import { resolveActorDisplayName } from "@multica/core/identity";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { cn } from "@multica/ui/lib/utils";
import { initialsOf } from "../../common/initials";
import { AgentPresenceOverlay } from "../../common/actor-avatar";
import { AgentXpBurst } from "./agent-xp-burst";
import { AgentAvatarPickerDialog } from "./agent-avatar-picker-dialog";
import { useT } from "../../i18n/use-t";

interface AgentProfileAvatarEditorProps {
  agent: Agent;
  /**
   * Permission gate from `useAgentPermissions(agent).canEdit.allowed`, computed
   * once by the panel and shared with the Profile tab so there's a single
   * source. When false the avatar renders read-only (no camera affordance).
   */
  canEdit: boolean;
  /**
   * `useUpdateAgent` handle (optimistic cache patch + generic toast). The
   * editor additionally invalidates the detail query after a successful
   * avatar write — `useUpdateAgent` only patches the request's touched keys
   * (`avatar_selection`), but the rendered avatar reads `agent.avatar_url`,
   * which the server re-derives. Invalidating detail refetches the canonical
   * URL so the new face actually flips in the panel.
   */
  onUpdate: (id: string, data: Record<string, unknown>) => Promise<void>;
}

/**
 * Header avatar for the agent profile panel (LRM-542).
 *
 * - Read-only viewers (`!canEdit`): presence + XP-burst avatar only.
 * - Editors (`canEdit`): click opens the shared {@link AgentAvatarPickerDialog}
 *   (system presets + custom upload; stage then Save).
 *
 * Avatar **removal** is intentionally not wired: the backend update contract
 * has no clear/reset path for `avatar_selection`.
 */
export function AgentProfileAvatarEditor({
  agent,
  canEdit,
  onUpdate,
}: AgentProfileAvatarEditorProps) {
  const { t } = useT("agents");
  const qc = useQueryClient();
  const [pickerOpen, setPickerOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  const displayName = resolveActorDisplayName(agent, agent.id);
  const initials = initialsOf(displayName);
  const avatarUrl = resolvePublicFileUrl(agent.avatar_url);
  const archived = !!agent.archived_at;
  const busy = saving;

  const commitAvatarSelection = async (
    selection: AgentAvatarSelection,
  ): Promise<boolean> => {
    try {
      await onUpdate(agent.id, { avatar_selection: selection });
    } catch {
      // useUpdateAgent already toasted a generic failure and rolled back.
      return false;
    }
    await qc.invalidateQueries({
      queryKey: agentDetailKeys.detail(agent.workspace_id, agent.id),
    });
    toast.success(t(($) => $.side_panel.avatar_updated_toast));
    return true;
  };

  const handleConfirm = async (selection: AgentAvatarSelection) => {
    setSaving(true);
    try {
      return await commitAvatarSelection(selection);
    } finally {
      setSaving(false);
    }
  };

  const avatar = (
    <ActorAvatarBase
      name={displayName}
      initials={initials}
      avatarUrl={avatarUrl}
      isAgent
      size={56}
      className={cn("rounded-full", archived && "opacity-50 grayscale")}
    />
  );

  const readPresence = (
    <AgentXpBurst agentId={agent.id}>
      <AgentPresenceOverlay agentId={agent.id} size={56}>
        {avatar}
      </AgentPresenceOverlay>
    </AgentXpBurst>
  );

  return (
    <>
      <div
        className="relative inline-flex shrink-0"
        data-testid="agent-profile-avatar"
        data-can-edit={String(canEdit)}
      >
        {canEdit ? (
          <button
            type="button"
            onClick={() => {
              if (busy) return;
              setPickerOpen(true);
            }}
            disabled={busy}
            aria-label={t(($) => $.side_panel.change_avatar_aria)}
            className="group relative inline-flex size-14 rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-progress"
          >
            {readPresence}
            <span
              aria-hidden
              className={cn(
                "pointer-events-none absolute inset-0 flex items-center justify-center rounded-full bg-black/40 opacity-100 transition-opacity",
                " [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100 [@media(hover:hover)]:group-focus-visible:opacity-100",
              )}
            >
              {busy ? (
                <Loader2 className="size-4 animate-spin text-white" />
              ) : (
                <Camera className="size-4 text-white" />
              )}
            </span>
          </button>
        ) : (
          readPresence
        )}
      </div>

      {canEdit ? (
        <AgentAvatarPickerDialog
          open={pickerOpen}
          onOpenChange={setPickerOpen}
          currentUrl={agent.avatar_url}
          onConfirm={(selection) => handleConfirm(selection)}
          uploadFailedMessage={t(($) => $.inspector.avatar_upload_failed_toast)}
        />
      ) : null}
    </>
  );
}
