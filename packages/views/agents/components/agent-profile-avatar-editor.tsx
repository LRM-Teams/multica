"use client";

import { useRef, useState } from "react";
import { Camera, Check, Loader2, Upload } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent, AgentAvatarSelection } from "@multica/core/types";
import { api } from "@multica/core/api";
import { agentDetailKeys } from "@multica/core/agents";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { resolveActorDisplayName } from "@multica/core/identity";
import {
  AGENT_AVATAR_PRESETS,
  resolvePublicFileUrl,
} from "@multica/core/workspace/avatar-url";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
 * - Editors (`canEdit`): the avatar becomes a button. Click opens the canonical
 *   system preset grid plus a custom-upload action. Uploads still flow through
 *   the circular 512² crop dialog before `avatar_selection` is committed.
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
  const [pickerOpen, setPickerOpen] = useState(false);
  const [savingPreset, setSavingPreset] = useState(false);
  const [cropSrc, setCropSrc] = useState<string | null>(null);
  const lastObjectUrlRef = useRef<string | null>(null);

  const displayName = resolveActorDisplayName(agent, agent.id);
  const initials = initialsOf(displayName);
  const avatarUrl = resolvePublicFileUrl(agent.avatar_url);
  const archived = !!agent.archived_at;
  const busy = uploading || savingPreset;

  // Revoke the last object URL when it is no longer needed (dialog closed or
  // a new one staged) so we don't leak the raw selected file. Revoke happens
  // inline at each transition; there is intentionally no unmount effect — a
  // stray URL on panel-close-mid-crop is freed on document unload, and an
  // empty-dep effect here trips react-doctor's exhaustive-deps heuristic
  // without adding real safety.

  const openPicker = () => {
    if (busy) return;
    setPickerOpen(true);
  };

  const commitAvatarSelection = async (selection: AgentAvatarSelection) => {
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

  const handlePresetSelect = async (presetUrl: string) => {
    if (busy) return;
    setSavingPreset(true);
    const updated = await commitAvatarSelection({
      kind: "picked",
      preset_url: presetUrl,
    });
    setSavingPreset(false);
    if (updated) setPickerOpen(false);
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
    await commitAvatarSelection({
      kind: "uploaded",
      attachment_id: result.id,
    });
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
            onClick={openPicker}
            disabled={busy}
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

      <Dialog
        open={pickerOpen}
        onOpenChange={(open) => {
          if (!busy) setPickerOpen(open);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.side_panel.avatar_picker_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.side_panel.avatar_picker_description)}
            </DialogDescription>
          </DialogHeader>

          <div
            className="grid grid-cols-4 gap-2 sm:grid-cols-5"
            aria-label={t(($) => $.side_panel.avatar_system_choices_aria)}
          >
            {AGENT_AVATAR_PRESETS.map((presetUrl, index) => {
              const selected = agent.avatar_url === presetUrl;
              return (
                <button
                  key={presetUrl}
                  type="button"
                  aria-label={`${t(($) => $.side_panel.avatar_system_choice_aria)} ${index + 1}`}
                  aria-pressed={selected}
                  disabled={busy}
                  onClick={() => void handlePresetSelect(presetUrl)}
                  className={cn(
                    "relative aspect-square overflow-hidden rounded-full border-2 bg-muted outline-none transition-transform hover:scale-105 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-60",
                    selected ? "border-primary" : "border-transparent",
                  )}
                >
                  <img
                    src={presetUrl}
                    alt=""
                    className="h-full w-full object-cover"
                  />
                  {selected ? (
                    <span className="absolute bottom-0.5 right-0.5 flex size-5 items-center justify-center rounded-full bg-primary text-primary-foreground ring-2 ring-background">
                      <Check className="size-3" aria-hidden />
                    </span>
                  ) : null}
                </button>
              );
            })}
          </div>

          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => {
              setPickerOpen(false);
              fileInputRef.current?.click();
            }}
          >
            <Upload aria-hidden />
            {t(($) => $.side_panel.avatar_upload_custom)}
          </Button>
        </DialogContent>
      </Dialog>
    </>
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
