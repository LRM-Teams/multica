"use client";

import { useRef, useState } from "react";
import { Camera, ImagePlus, Loader2, X } from "lucide-react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { AvatarCropDialog } from "./avatar-crop-dialog";

/** LRM-542: PNG/JPG, ≤5 MB, ≥256² input → 512² crop output. */
const AVATAR_MAX_BYTES = 5 * 1024 * 1024;
const AVATAR_MIN_DIMENSION = 256;

/** What the picker hands back after a successful upload: the attachment id
 * the server needs for `avatar_selection` plus the URL for local preview. */
export interface AvatarPickerSelection {
  attachmentId: string;
  previewUrl: string;
}

interface AvatarPickerProps {
  /** Current preview URL. null when nothing chosen yet. */
  value: string | null;
  /** Fires after a successful upload with the attachment id the parent
   *  must submit as `avatar_selection`. Re-fires with null when the user
   *  clears the choice. */
  onChange: (selection: AvatarPickerSelection | null) => void;
  /** Pixel size of the square. Defaults to 56 (h-14 / w-14), which lines
   *  up vertically with the Name + Description stack in the create-agent
   *  form so the two read as a single visual row. */
  size?: number;
}

function readImageDimensions(file: File): Promise<{ width: number; height: number }> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      const width = img.naturalWidth;
      const height = img.naturalHeight;
      URL.revokeObjectURL(url);
      resolve({ width, height });
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("decode failed"));
    };
    img.src = url;
  });
}

/**
 * Compact avatar picker — a single square that lives next to the Name
 * input in the create-agent form. Mirrors the visual language of
 * agent-detail-inspector.tsx (Camera overlay on hover, file input behind
 * the scenes), so users who've configured an avatar elsewhere in the app
 * recognise the affordance immediately.
 *
 * Upload path: pick PNG/JPG → circular crop (512²) → upload → onChange.
 *
 * No avatar yet → dashed placeholder with an ImagePlus icon.
 * Has avatar    → image fills the square, hover dims it with a Camera
 *                 overlay for "click to change". A small × in the corner
 *                 clears the choice.
 */
export function AvatarPicker({ value, onChange, size = 56 }: AvatarPickerProps) {
  const { t } = useT("agents");
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { upload, uploading } = useFileUpload(api);
  const [previewError, setPreviewError] = useState(false);
  const [cropSrc, setCropSrc] = useState<string | null>(null);
  const lastObjectUrlRef = useRef<string | null>(null);

  const handleFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = ""; // allow re-selecting the same file
    const type = file.type.toLowerCase();
    if (type !== "image/png" && type !== "image/jpeg") {
      showErrorToast(t(($) => $.side_panel.avatar_err_type));
      return;
    }
    if (file.size > AVATAR_MAX_BYTES) {
      showErrorToast(t(($) => $.side_panel.avatar_err_size));
      return;
    }
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
    try {
      const result = await upload(cropped);
      if (!result) return;
      setPreviewError(false);
      onChange({ attachmentId: result.id, previewUrl: result.link });
    } catch (err) {
      showErrorToast(
        err instanceof Error
          ? err.message
          : t(($) => $.create_dialog.avatar.upload_failed_toast),
      );
    }
  };

  const hasValue = !!value && !previewError;
  const dimensionStyle = { width: size, height: size };

  return (
    <>
      <div className="relative shrink-0" style={dimensionStyle}>
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          disabled={uploading}
          className={cn(
            "group relative h-full w-full overflow-hidden rounded-lg outline-none transition-colors",
            "focus-visible:ring-2 focus-visible:ring-ring",
            hasValue
              ? "border"
              : "border border-dashed bg-muted/40 hover:bg-muted",
          )}
          aria-label={
            hasValue
              ? t(($) => $.create_dialog.avatar.change_aria)
              : t(($) => $.create_dialog.avatar.upload_aria)
          }
          style={dimensionStyle}
        >
          {hasValue ? (
            <img
              src={resolvePublicFileUrl(value) ?? undefined}
              alt=""
              className="h-full w-full object-cover"
              onError={() => setPreviewError(true)}
            />
          ) : (
            <div className="flex h-full w-full items-center justify-center text-muted-foreground">
              {uploading ? (
                <Loader2 className="h-5 w-5 animate-spin" />
              ) : (
                <ImagePlus className="h-5 w-5" />
              )}
            </div>
          )}

          {hasValue && (
            <div className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
              {uploading ? (
                <Loader2 className="h-4 w-4 animate-spin text-white" />
              ) : (
                <Camera className="h-4 w-4 text-white" />
              )}
            </div>
          )}
        </button>

        {hasValue && !uploading && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onChange(null);
              setPreviewError(false);
            }}
            className="absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full border bg-background text-muted-foreground shadow-sm transition-colors hover:bg-muted hover:text-foreground"
            aria-label={t(($) => $.create_dialog.avatar.remove_aria)}
          >
            <X className="h-3 w-3" />
          </button>
        )}

        <input
          ref={fileInputRef}
          type="file"
          accept="image/png,image/jpeg"
          className="hidden"
          onChange={handleFile}
        />
      </div>

      {cropSrc ? (
        <AvatarCropDialog
          src={cropSrc}
          busy={uploading}
          onCancel={handleCropCancel}
          onConfirm={handleCropConfirm}
        />
      ) : null}
    </>
  );
}
