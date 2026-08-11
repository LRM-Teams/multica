"use client";

import { useRef, useState } from "react";
import { Camera, Check, ImagePlus, Loader2, Upload, X } from "lucide-react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import {
  AGENT_AVATAR_PRESETS,
  resolvePublicFileUrl,
} from "@multica/core/workspace/avatar-url";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { AvatarCropDialog } from "./avatar-crop-dialog";

/** LRM-542: PNG/JPG, ≤5 MB, ≥256² input → 512² crop output. */
const AVATAR_MAX_BYTES = 5 * 1024 * 1024;
const AVATAR_MIN_DIMENSION = 256;

/**
 * What the picker hands back after a choice: either a system preset URL
 * (`picked`) or an uploaded attachment id (`uploaded`). Parents map this to
 * `avatar_selection` on create/update.
 */
export type AvatarPickerSelection =
  | { kind: "picked"; presetUrl: string; previewUrl: string }
  | { kind: "uploaded"; attachmentId: string; previewUrl: string };

interface AvatarPickerProps {
  /** Current preview URL. null when nothing chosen yet. */
  value: string | null;
  /** Fires after the user picks a system face or finishes upload. Re-fires
   *  with null when the user clears the choice. */
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
 * Compact avatar picker for the create-agent form. Click opens the same
 * system-preset grid used on the agent profile editor (15 CDN faces) plus a
 * custom-upload path with circular crop. Selecting a preset or finishing an
 * upload commits immediately into the parent draft (no separate Save —
 * create itself is the commit).
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
  const [pickerOpen, setPickerOpen] = useState(false);
  const [cropSrc, setCropSrc] = useState<string | null>(null);
  const lastObjectUrlRef = useRef<string | null>(null);

  const openPicker = () => {
    if (uploading) return;
    setPickerOpen(true);
  };

  const closePicker = () => {
    if (uploading) return;
    setPickerOpen(false);
  };

  const handlePresetSelect = (presetUrl: string) => {
    if (uploading) return;
    setPreviewError(false);
    onChange({ kind: "picked", presetUrl, previewUrl: presetUrl });
    setPickerOpen(false);
  };

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
    setPickerOpen(false);
    setCropSrc(url);
  };

  const handleCropCancel = () => {
    setCropSrc(null);
    if (lastObjectUrlRef.current) {
      URL.revokeObjectURL(lastObjectUrlRef.current);
      lastObjectUrlRef.current = null;
    }
    setPickerOpen(true);
  };

  const handleCropConfirm = async (cropped: File) => {
    setCropSrc(null);
    if (lastObjectUrlRef.current) {
      URL.revokeObjectURL(lastObjectUrlRef.current);
      lastObjectUrlRef.current = null;
    }
    try {
      const result = await upload(cropped);
      if (!result) {
        setPickerOpen(true);
        return;
      }
      setPreviewError(false);
      onChange({
        kind: "uploaded",
        attachmentId: result.id,
        previewUrl: result.link,
      });
    } catch (err) {
      showErrorToast(
        err instanceof Error
          ? err.message
          : t(($) => $.create_dialog.avatar.upload_failed_toast),
      );
      setPickerOpen(true);
    }
  };

  const hasValue = !!value && !previewError;
  const dimensionStyle = { width: size, height: size };
  const customSelected =
    !!value && !AGENT_AVATAR_PRESETS.includes(value as (typeof AGENT_AVATAR_PRESETS)[number]);

  return (
    <>
      <div className="relative shrink-0" style={dimensionStyle}>
        <button
          type="button"
          onClick={openPicker}
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
          data-testid="avatar-picker-trigger"
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
            data-testid="avatar-picker-clear"
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

      <Dialog
        open={pickerOpen}
        onOpenChange={(open) => {
          if (uploading) return;
          if (open) setPickerOpen(true);
          else closePicker();
        }}
      >
        <DialogContent className="sm:max-w-md" data-testid="avatar-picker-dialog">
          <DialogHeader>
            <DialogTitle>{t(($) => $.side_panel.avatar_picker_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.side_panel.avatar_picker_description)}
            </DialogDescription>
          </DialogHeader>

          <div
            className="grid grid-cols-4 gap-2 sm:grid-cols-5"
            aria-label={t(($) => $.side_panel.avatar_system_choices_aria)}
            data-testid="avatar-picker-presets"
          >
            {AGENT_AVATAR_PRESETS.map((presetUrl, index) => {
              const selected = value === presetUrl;
              return (
                <button
                  key={presetUrl}
                  type="button"
                  aria-label={`${t(($) => $.side_panel.avatar_system_choice_aria)} ${index + 1}`}
                  aria-pressed={selected}
                  disabled={uploading}
                  onClick={() => handlePresetSelect(presetUrl)}
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

          {customSelected && value ? (
            <div className="flex items-center gap-3 rounded-lg border bg-muted/30 p-2">
              <span className="relative size-12 shrink-0 overflow-hidden rounded-full border-2 border-primary">
                <img
                  src={resolvePublicFileUrl(value) ?? value}
                  alt=""
                  className="size-full object-cover"
                />
                <span className="absolute bottom-0 right-0 flex size-4 items-center justify-center rounded-full bg-primary text-primary-foreground ring-2 ring-background">
                  <Check className="size-2.5" aria-hidden />
                </span>
              </span>
              <span className="text-sm font-medium">
                {t(($) => $.side_panel.avatar_custom_selected)}
              </span>
            </div>
          ) : null}

          <Button
            type="button"
            variant="outline"
            disabled={uploading}
            onClick={() => fileInputRef.current?.click()}
          >
            {uploading ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : (
              <Upload aria-hidden />
            )}
            {t(($) => $.side_panel.avatar_upload_custom)}
          </Button>
        </DialogContent>
      </Dialog>

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
