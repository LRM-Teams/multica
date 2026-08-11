"use client";

import { useEffect, useReducer, useRef, useState } from "react";
import { Check, Loader2, Upload } from "lucide-react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import type { AgentAvatarSelection } from "@multica/core/types";
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

/** LRM-542 SoT §2 constraints: PNG/JPG, ≤5 MB, ≥256² input, 512² output. */
const AVATAR_MAX_BYTES = 5 * 1024 * 1024;
const AVATAR_MIN_DIMENSION = 256;
const AVATAR_ACCEPT = "image/png,image/jpeg";

type DraftState = {
  selection: AgentAvatarSelection | null;
  previewUrl: string | null;
  saving: boolean;
};

type DraftAction =
  | { type: "reset"; previewUrl: string | null }
  | {
      type: "select";
      selection: AgentAvatarSelection;
      previewUrl: string;
    }
  | { type: "setSaving"; saving: boolean };

function draftReducer(state: DraftState, action: DraftAction): DraftState {
  switch (action.type) {
    case "reset":
      return {
        selection: null,
        previewUrl: action.previewUrl,
        saving: false,
      };
    case "select":
      return {
        ...state,
        selection: action.selection,
        previewUrl: action.previewUrl,
      };
    case "setSaving":
      return { ...state, saving: action.saving };
  }
}

export interface AgentAvatarPickerDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /**
   * Currently committed face URL. Used as the initial highlight when the
   * dialog opens; a fresh pick is required before Save is enabled (matches
   * profile SoT — open is not itself a change).
   */
  currentUrl?: string | null;
  /**
   * Confirm a staged selection. Return `false` to keep the dialog open
   * (e.g. profile update failed). `true` / `void` closes it.
   */
  onConfirm: (
    selection: AgentAvatarSelection,
    previewUrl: string,
  ) => boolean | void | Promise<boolean | void>;
  /** Toast copy when crop upload fails (surface-specific). */
  uploadFailedMessage?: string;
}

/**
 * Shared agent avatar chooser: 15 system presets + custom upload with crop.
 *
 * Interaction model (same on Create and Profile):
 * - Clicking a face only **stages** it (dialog preview / highlight).
 * - **Save** commits the staged choice to the parent.
 * - **Cancel** / dismiss discards the draft.
 *
 * Parent surfaces own the trigger chrome (square create control vs profile
 * presence avatar); this dialog owns picker state, crop, and upload.
 */
export function AgentAvatarPickerDialog({
  open,
  onOpenChange,
  currentUrl = null,
  onConfirm,
  uploadFailedMessage,
}: AgentAvatarPickerDialogProps) {
  const { t } = useT("agents");
  const { upload, uploading } = useFileUpload(api);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [draft, dispatch] = useReducer(draftReducer, {
    selection: null,
    previewUrl: currentUrl,
    saving: false,
  });
  const [cropSrc, setCropSrc] = useState<string | null>(null);
  const lastObjectUrlRef = useRef<string | null>(null);
  const wasOpenRef = useRef(false);

  // Reset draft only on false → true open edge so crop hide/show does not
  // wipe a staged upload (crop keeps parent `open` true and only sets cropSrc).
  useEffect(() => {
    if (open && !wasOpenRef.current) {
      dispatch({ type: "reset", previewUrl: currentUrl ?? null });
    }
    if (!open) {
      setCropSrc(null);
      if (lastObjectUrlRef.current) {
        URL.revokeObjectURL(lastObjectUrlRef.current);
        lastObjectUrlRef.current = null;
      }
    }
    wasOpenRef.current = open;
  }, [open, currentUrl]);

  const busy = uploading || draft.saving;
  const chooserOpen = open && !cropSrc;

  const close = () => {
    if (busy) return;
    onOpenChange(false);
  };

  const handlePresetSelect = (presetUrl: string) => {
    if (busy) return;
    dispatch({
      type: "select",
      selection: { kind: "picked", preset_url: presetUrl },
      previewUrl: presetUrl,
    });
  };

  const handleSave = async () => {
    if (busy || !draft.selection || !draft.previewUrl) return;
    dispatch({ type: "setSaving", saving: true });
    try {
      const result = await onConfirm(draft.selection, draft.previewUrl);
      if (result === false) return;
      onOpenChange(false);
    } finally {
      dispatch({ type: "setSaving", saving: false });
    }
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
    // Hide chooser while cropping; parent `open` stays true so draft survives.
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
    let result;
    try {
      result = await upload(cropped);
    } catch (err) {
      showErrorToast(
        err instanceof Error
          ? err.message
          : uploadFailedMessage ?? t(($) => $.create_dialog.avatar.upload_failed_toast),
      );
      return;
    }
    if (!result) return;
    dispatch({
      type: "select",
      selection: { kind: "uploaded", attachment_id: result.id },
      previewUrl: result.link,
    });
  };

  return (
    <>
      <input
        ref={fileInputRef}
        type="file"
        accept={AVATAR_ACCEPT}
        className="hidden"
        tabIndex={-1}
        aria-hidden
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

      <Dialog
        open={chooserOpen}
        onOpenChange={(next) => {
          if (busy) return;
          if (next) onOpenChange(true);
          else close();
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
              const selected = draft.previewUrl === presetUrl;
              return (
                <button
                  key={presetUrl}
                  type="button"
                  aria-label={`${t(($) => $.side_panel.avatar_system_choice_aria)} ${index + 1}`}
                  aria-pressed={selected}
                  disabled={busy}
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

          {draft.selection?.kind === "uploaded" && draft.previewUrl ? (
            <div className="flex items-center gap-3 rounded-lg border bg-muted/30 p-2">
              <span className="relative size-12 shrink-0 overflow-hidden rounded-full border-2 border-primary">
                <img
                  src={resolvePublicFileUrl(draft.previewUrl) ?? draft.previewUrl}
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
            disabled={busy}
            onClick={() => fileInputRef.current?.click()}
          >
            {uploading ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : (
              <Upload aria-hidden />
            )}
            {t(($) => $.side_panel.avatar_upload_custom)}
          </Button>

          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" disabled={busy} onClick={close}>
              {t(($) => $.side_panel.avatar_picker_cancel)}
            </Button>
            <Button
              type="button"
              disabled={busy || !draft.selection}
              onClick={() => void handleSave()}
              data-testid="avatar-picker-save"
            >
              {draft.saving ? <Loader2 className="animate-spin" aria-hidden /> : null}
              {t(($) => $.side_panel.avatar_picker_save)}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

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
