"use client";

import { useState } from "react";
import { Camera, ImagePlus, X } from "lucide-react";
import type { AgentAvatarSelection } from "@multica/core/types";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { AgentAvatarPickerDialog } from "./agent-avatar-picker-dialog";

/**
 * What the create-form picker hands back after Save: either a system preset
 * (`picked`) or an uploaded attachment (`uploaded`). Parents map this to
 * `avatar_selection` on create.
 */
export type AvatarPickerSelection =
  | { kind: "picked"; presetUrl: string; previewUrl: string }
  | { kind: "uploaded"; attachmentId: string; previewUrl: string };

interface AvatarPickerProps {
  /** Current committed preview URL. null when nothing chosen yet. */
  value: string | null;
  /** Fires after Save with a staged choice, or with null when the user clears. */
  onChange: (selection: AvatarPickerSelection | null) => void;
  /** Pixel size of the square. Defaults to 56. */
  size?: number;
}

function toPickerSelection(
  selection: AgentAvatarSelection,
  previewUrl: string,
): AvatarPickerSelection {
  if (selection.kind === "uploaded") {
    return {
      kind: "uploaded",
      attachmentId: selection.attachment_id,
      previewUrl,
    };
  }
  return {
    kind: "picked",
    presetUrl: selection.preset_url,
    previewUrl,
  };
}

/**
 * Compact square trigger for the create-agent form. Opens the shared
 * {@link AgentAvatarPickerDialog} (system faces, random robot, or upload).
 *
 * No avatar yet → dashed placeholder with an ImagePlus icon.
 * Has avatar    → image fills the square; hover shows Camera; × clears.
 */
export function AvatarPicker({ value, onChange, size = 56 }: AvatarPickerProps) {
  const { t } = useT("agents");
  const [pickerOpen, setPickerOpen] = useState(false);
  const [previewError, setPreviewError] = useState(false);

  const hasValue = !!value && !previewError;
  const dimensionStyle = { width: size, height: size };

  return (
    <>
      <div className="relative shrink-0" style={dimensionStyle}>
        <button
          type="button"
          onClick={() => setPickerOpen(true)}
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
              <ImagePlus className="h-5 w-5" />
            </div>
          )}

          {hasValue && (
            <div className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
              <Camera className="h-4 w-4 text-white" />
            </div>
          )}
        </button>

        {hasValue && (
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
      </div>

      <AgentAvatarPickerDialog
        open={pickerOpen}
        onOpenChange={setPickerOpen}
        currentUrl={value}
        onConfirm={(selection, previewUrl) => {
          setPreviewError(false);
          onChange(toPickerSelection(selection, previewUrl));
        }}
        uploadFailedMessage={t(($) => $.create_dialog.avatar.upload_failed_toast)}
      />
    </>
  );
}
