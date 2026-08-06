"use client";

import { useRef, useState } from "react";
import { Camera, Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { AvatarCropDialog } from "../agents/components/avatar-crop-dialog";
import { useT } from "../i18n/use-t";

/** Same constraints as the agent avatar editor (LRM-542 SoT §2). */
const AVATAR_MAX_BYTES = 5 * 1024 * 1024;
const AVATAR_MIN_DIMENSION = 256;
const AVATAR_ACCEPT = "image/png,image/jpeg";

interface MemberSelfAvatarEditorProps {
  userId: string;
  /** Renders the member avatar visual (image + presence dot), 64px. */
  children: React.ReactNode;
}

/**
 * LRM-751 / LRM-719 — own profile card avatar edit affordance.
 * A persistent camera badge at the avatar's bottom-right (design gate
 * design-lrm719-self-profile-edit.html). Click → pick PNG/JPG → circular
 * crop dialog (reuses LRM-456 `AvatarCropDialog`) → upload → PATCH /api/me
 * `avatar_url` → auth store + member caches invalidated so every surface
 * (panel hero, sidebar, messages) flips to the new face.
 *
 * Rendered only for the self view; the parent keeps the plain read-only
 * avatar for everyone else.
 */
export function MemberSelfAvatarEditor({
  userId,
  children,
}: MemberSelfAvatarEditorProps) {
  const { t } = useT("members");
  const wsId = useWorkspaceId();
  const setUser = useAuthStore((s) => s.setUser);
  const qc = useQueryClient();
  const { upload, uploading } = useFileUpload(api);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [cropSrc, setCropSrc] = useState<string | null>(null);
  const lastObjectUrlRef = useRef<string | null>(null);

  const openPicker = () => {
    if (uploading) return;
    fileInputRef.current?.click();
  };

  const releaseObjectUrl = () => {
    if (lastObjectUrlRef.current) {
      URL.revokeObjectURL(lastObjectUrlRef.current);
      lastObjectUrlRef.current = null;
    }
  };

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;

    const type = file.type.toLowerCase();
    if (type !== "image/png" && type !== "image/jpeg") {
      showErrorToast(t(($) => $.panel.avatar_err_type));
      return;
    }
    if (file.size > AVATAR_MAX_BYTES) {
      showErrorToast(t(($) => $.panel.avatar_err_size));
      return;
    }
    try {
      const dims = await readImageDimensions(file);
      if (dims.width < AVATAR_MIN_DIMENSION || dims.height < AVATAR_MIN_DIMENSION) {
        showErrorToast(t(($) => $.panel.avatar_err_dimensions));
        return;
      }
    } catch {
      showErrorToast(t(($) => $.panel.avatar_err_type));
      return;
    }

    const url = URL.createObjectURL(file);
    releaseObjectUrl();
    lastObjectUrlRef.current = url;
    setCropSrc(url);
  };

  const handleCropCancel = () => {
    setCropSrc(null);
    releaseObjectUrl();
  };

  const handleCropConfirm = async (cropped: File) => {
    setCropSrc(null);
    releaseObjectUrl();
    let result;
    try {
      result = await upload(cropped);
    } catch (err) {
      showErrorToast(
        err instanceof Error ? err.message : t(($) => $.panel.avatar_upload_failed),
      );
      return;
    }
    if (!result) return;
    try {
      const updated = await api.updateMe({ avatar_url: result.link });
      setUser(updated);
    } catch (err) {
      showErrorToast(
        err instanceof Error ? err.message : t(($) => $.panel.avatar_upload_failed),
      );
      return;
    }
    void qc.invalidateQueries({
      predicate: (q) => q.queryKey[0] === "workspaces" && q.queryKey[2] === "members",
    });
    void qc.invalidateQueries({
      queryKey: workspaceKeys.memberProfile(wsId, "user", userId),
    });
    toast.success(t(($) => $.panel.avatar_updated_toast));
  };

  return (
    <div className="relative inline-flex shrink-0" data-testid="member-self-avatar-editor">
      {children}
      <button
        type="button"
        onClick={openPicker}
        disabled={uploading}
        aria-label={t(($) => $.panel.change_avatar_aria)}
        data-testid="member-self-avatar-change"
        className="absolute -bottom-1 -right-1 flex size-[26px] items-center justify-center rounded-full border border-border bg-background text-muted-foreground shadow-sm transition-colors hover:bg-accent hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-progress"
      >
        {uploading ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : (
          <Camera className="size-3.5" />
        )}
      </button>

      <input
        ref={fileInputRef}
        type="file"
        accept={AVATAR_ACCEPT}
        className="hidden"
        aria-label={t(($) => $.panel.change_avatar_aria)}
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
