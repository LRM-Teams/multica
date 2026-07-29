"use client";

import { useRef, useState } from "react";
import { Camera, Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent } from "@multica/core/types";
import { api } from "@multica/core/api";
import { agentDetailKeys } from "@multica/core/agents";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { resolveActorDisplayName } from "@multica/core/identity";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { cn } from "@multica/ui/lib/utils";
import { initialsOf } from "../../common/initials";
import { AgentPresenceOverlay } from "../../common/actor-avatar";
import { AgentXpBurst } from "./agent-xp-burst";
import { AvatarCropDialog } from "./avatar-crop-dialog";
import { useT } from "../../i18n/use-t";

/** LRM-542 SoT §2 constraints: PNG/JPG, ≤5 MB, ≥256² input, 512² output. */
const AVATAR_MAX_BYTES = 5 * 1024 * 1024;
const AVATAR_MIN_DIMENSION = 256;
const AVATAR_ACCEPT = "image/png,image/jpeg";

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
 * - Read-only viewers (`!canEdit`): renders the standard presence + XP-burst
 *   avatar, identical to before, so nothing else changes for them.
 * - Editors (`canEdit`): the avatar becomes a button. Hover/focus dims it with
 *   a camera affordance (desktop); on touch (`hover: none`) the same overlay
 *   stays visible as a persistent "edit" badge. Click → pick a PNG/JPG →
 *   circular crop dialog (512² output) → upload → `avatar_selection`.
 *
 * Mirrors the affordance language of `AvatarEditor` in
 * `agent-detail-inspector.tsx` (camera overlay + `useFileUpload`), but adds
 * the crop step the SoT requires and keeps the panel's presence dot / XP burst.
 *
 * Avatar **removal** (SoT state ③ "移除头像") is intentionally not wired here:
 * the backend agent-update contract has no clear/reset path — omitting
 * `avatar_selection` is a no-op, `kind` must be `uploaded|picked`, and raw
 * `avatar_url` is rejected (`use avatar_selection`). That needs a server-side
 * change (split as a backend follow-up), so we don't ship a non-functional
 * "remove" control.
 */
export function AgentProfileAvatarEditor({
  agent,
  canEdit,
  onUpdate,
}: AgentProfileAvatarEditorProps) {
  const { t } = useT("agents");
  const { upload, uploading } = useFileUpload(api);
  const qc = useQueryClient();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [cropSrc, setCropSrc] = useState<string | null>(null);
  const lastObjectUrlRef = useRef<string | null>(null);

  const displayName = resolveActorDisplayName(agent, agent.id);
  const initials = initialsOf(displayName);
  const avatarUrl = resolvePublicFileUrl(agent.avatar_url);
  const archived = !!agent.archived_at;

  // Revoke the last object URL when it is no longer needed (dialog closed or
  // a new one staged) so we don't leak the raw selected file. Revoke happens
  // inline at each transition; there is intentionally no unmount effect — a
  // stray URL on panel-close-mid-crop is freed on document unload, and an
  // empty-dep effect here trips react-doctor's exhaustive-deps heuristic
  // without adding real safety.

  const openPicker = () => {
    if (uploading) return;
    fileInputRef.current?.click();
  };

  const validateSelection = (file: File): string | null => {
    const type = file.type.toLowerCase();
    if (type !== "image/png" && type !== "image/jpeg") {
      return t(($) => $.side_panel.avatar_err_type);
    }
    if (file.size > AVATAR_MAX_BYTES) {
      return t(($) => $.side_panel.avatar_err_size);
    }
    return null;
  };

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;

    const typeError = validateSelection(file);
    if (typeError) {
      showErrorToast(typeError);
      return;
    }
    // Dimensions require decoding the image; enforce ≥256² before cropping.
    try {
      const dims = await readImageDimensions(file);
      if (dims.width < AVATAR_MIN_DIMENSION || dims.height < AVATAR_MIN_DIMENSION) {
        showErrorToast(t(($) => $.side_panel.avatar_err_dimensions));
        return;
      }
    } catch {
      showErrorToast(t(($) => $.side_panel.avatar_err_type));
      return;
    }

    const url = URL.createObjectURL(file);
    if (lastObjectUrlRef.current) URL.revokeObjectURL(lastObjectUrlRef.current);
    lastObjectUrlRef.current = url;
    setCropSrc(url);
  };

  const handleCropCancel = () => {
    setCropSrc(null);
    if (lastObjectUrlRef.current) {
      URL.revokeObjectURL(lastObjectUrlRef.current);
      lastObjectUrlRef.current = null;
    }
  };

  const handleCropConfirm = async (cropped: File) => {
    setCropSrc(null);
    if (lastObjectUrlRef.current) {
      URL.revokeObjectURL(lastObjectUrlRef.current);
      lastObjectUrlRef.current = null;
    }
    // Upload failure must surface its own toast — `useFileUpload.upload` throws
    // on network/server errors and has no built-in toast (unlike uploadWithToast).
    let result;
    try {
      result = await upload(cropped);
    } catch (err) {
      showErrorToast(
        err instanceof Error
          ? err.message
          : t(($) => $.inspector.avatar_upload_failed_toast),
      );
      return;
    }
    if (!result) return;
    try {
      await onUpdate(agent.id, {
        avatar_selection: { kind: "uploaded", attachment_id: result.id },
      });
    } catch {
      // useUpdateAgent already toasted a generic failure and rolled back;
      // swallow so we don't double-toast.
      return;
    }
    // useUpdateAgent only patches the touched request key (`avatar_selection`),
    // but the rendered avatar reads `agent.avatar_url` (server-derived).
    // Refetch detail so the new face flips in the panel + directory.
    await qc.invalidateQueries({
      queryKey: agentDetailKeys.detail(agent.workspace_id, agent.id),
    });
    toast.success(t(($) => $.side_panel.avatar_updated_toast));
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
    <div className="relative inline-flex shrink-0" data-testid="agent-profile-avatar" data-can-edit={String(canEdit)}>
      {canEdit ? (
        <button
          type="button"
          onClick={openPicker}
          disabled={uploading}
          aria-label={t(($) => $.side_panel.change_avatar_aria)}
          className="group relative inline-flex size-14 rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-progress"
        >
          {readPresence}
          {/* Camera affordance. Hover/focus on pointer devices; on touch
              (hover:none) the overlay stays visible as a persistent badge so
              the avatar reads as editable without a hover cycle. */}
          <span
            aria-hidden
            className={cn(
              "pointer-events-none absolute inset-0 flex items-center justify-center rounded-full bg-black/40 opacity-100 transition-opacity",
              " [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100 [@media(hover:hover)]:group-focus-visible:opacity-100",
            )}
          >
            {uploading ? (
              <Loader2 className="size-4 animate-spin text-white" />
            ) : (
              <Camera className="size-4 text-white" />
            )}
          </span>
        </button>
      ) : (
        readPresence
      )}

      <input
        ref={fileInputRef}
        type="file"
        accept={AVATAR_ACCEPT}
        className="hidden"
        aria-label={t(($) => $.side_panel.change_avatar_aria)}
        tabIndex={-1}
        onChange={handleFileChange}
      />

      {cropSrc ? (
        <AvatarCropDialog
          key={cropSrc}
          src={cropSrc}
          busy={uploading}
          onCancel={handleCropCancel}
          onConfirm={handleCropConfirm}
        />
      ) : null}
    </div>
  );
}

/** Decodes an image file to its natural pixel dimensions. Rejects non-images. */
function readImageDimensions(
  file: File,
): Promise<{ width: number; height: number }> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      URL.revokeObjectURL(url);
      resolve({ width: img.naturalWidth, height: img.naturalHeight });
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("not an image"));
    };
    img.src = url;
  });
}
