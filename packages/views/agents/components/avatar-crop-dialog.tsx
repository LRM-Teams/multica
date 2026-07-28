"use client";

import { useCallback, useRef, useState } from "react";
import { ZoomIn } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Slider } from "@multica/ui/components/ui/slider";
import { useT } from "../../i18n/use-t";
import {
  AVATAR_OUTPUT_SIZE,
  computeAvatarCropSourceRect,
} from "./avatar-crop-utils";

/** Preview stage edge in CSS px. Only affects the on-screen crop preview;
 *  the exported canvas is always {@link AVATAR_OUTPUT_SIZE}. */
const CROP_STAGE_SIZE = 240;

/** Minimum zoom (image just covers the circle). Maximum ~3x for tight framing. */
const MIN_ZOOM = 1;
const MAX_ZOOM = 3;

interface AvatarCropDialogProps {
  /** Object URL (or data URL) of the raw selected image. */
  src: string;
  /** Whether the parent is applying the update. Disables controls + shows spinner. */
  busy?: boolean;
  onCancel: () => void;
  /** Hands back a 512² PNG file ready for upload. */
  onConfirm: (file: File) => void;
}

/**
 * Circular avatar crop dialog (LRM-542 SoT §2 state ④). Drag to pan, slider to
 * zoom; the visible circle is rendered to a 512² canvas and exported as PNG.
 *
 * Dependency-free on purpose: no sibling avatar editor in the app crops today
 * (account / workspace avatars are direct-upload), so there was no existing
 * primitive to reuse. Keeping it self-contained (pointer events + canvas) avoids
 * pulling in a crop library for a single surface.
 *
 * The caller mounts this with a `key` tied to the selected image so every new
 * selection starts from a fresh crop (zoom 1, no pan) — that replaces the
 * effect-based reset react-doctor flags, and keeps Fast Refresh clean.
 */
export function AvatarCropDialog({
  src,
  busy = false,
  onCancel,
  onConfirm,
}: AvatarCropDialogProps) {
  const { t } = useT("agents");
  const [zoom, setZoom] = useState(MIN_ZOOM);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null);
  const imgRef = useRef<HTMLImageElement>(null);
  const dragRef = useRef<{ active: boolean; lastX: number; lastY: number }>({
    active: false,
    lastX: 0,
    lastY: 0,
  });

  const onImgLoad = useCallback(() => {
    const img = imgRef.current;
    if (!img) return;
    setNatural({ w: img.naturalWidth, h: img.naturalHeight });
  }, []);

  // Clamp pan so the image cannot be dragged away from the circle edge (which
  // would reveal the stage background through the mask).
  const clampPan = useCallback(
    (nextX: number, nextY: number, z: number) => {
      if (!natural) return { x: 0, y: 0 };
      const coverScale = CROP_STAGE_SIZE / Math.min(natural.w, natural.h);
      const renderW = natural.w * coverScale * z;
      const renderH = natural.h * coverScale * z;
      const maxX = Math.max(0, (renderW - CROP_STAGE_SIZE) / 2);
      const maxY = Math.max(0, (renderH - CROP_STAGE_SIZE) / 2);
      return {
        x: Math.max(-maxX, Math.min(nextX, maxX)),
        y: Math.max(-maxY, Math.min(nextY, maxY)),
      };
    },
    [natural],
  );

  const handleZoomChange = (next: number) => {
    setZoom(next);
    // Re-clamp pan for the new zoom so the image stays covering the circle.
    setPan((p) => clampPan(p.x, p.y, next));
  };

  const onPointerDown = (e: React.PointerEvent) => {
    if (busy) return;
    dragRef.current = { active: true, lastX: e.clientX, lastY: e.clientY };
    (e.target as Element).setPointerCapture?.(e.pointerId);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    if (!dragRef.current.active) return;
    const dx = e.clientX - dragRef.current.lastX;
    const dy = e.clientY - dragRef.current.lastY;
    dragRef.current.lastX = e.clientX;
    dragRef.current.lastY = e.clientY;
    setPan((p) => clampPan(p.x + dx, p.y + dy, zoom));
  };
  const endDrag = () => {
    dragRef.current.active = false;
  };

  const handleConfirm = () => {
    const img = imgRef.current;
    if (!img || !natural) return;
    const { sx, sy, sw, sh } = computeAvatarCropSourceRect({
      naturalWidth: natural.w,
      naturalHeight: natural.h,
      stageSize: CROP_STAGE_SIZE,
      zoom,
      panX: pan.x,
      panY: pan.y,
    });
    const canvas = document.createElement("canvas");
    canvas.width = AVATAR_OUTPUT_SIZE;
    canvas.height = AVATAR_OUTPUT_SIZE;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    // White backdrop so transparent PNGs don't render as black on dark avatars;
    // the avatar mask is circular via CSS, but the stored PNG is square.
    ctx.fillStyle = "#ffffff";
    ctx.fillRect(0, 0, AVATAR_OUTPUT_SIZE, AVATAR_OUTPUT_SIZE);
    ctx.imageSmoothingQuality = "high";
    ctx.drawImage(img, sx, sy, sw, sh, 0, 0, AVATAR_OUTPUT_SIZE, AVATAR_OUTPUT_SIZE);
    canvas.toBlob((blob) => {
      if (!blob) return;
      onConfirm(new File([blob], "avatar.png", { type: "image/png" }));
    }, "image/png");
  };

  const coverScale = natural
    ? CROP_STAGE_SIZE / Math.min(natural.w, natural.h)
    : 1;
  const renderW = natural ? natural.w * coverScale : CROP_STAGE_SIZE;
  const renderH = natural ? natural.h * coverScale : CROP_STAGE_SIZE;

  return (
    <Dialog open onOpenChange={(o) => !o && onCancel()}>
      <DialogContent showCloseButton={false} className="sm:max-w-xs">
        <DialogHeader>
          <DialogTitle>{t(($) => $.side_panel.avatar_crop_title)}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col items-center gap-4">
          <div
            className="relative touch-none select-none overflow-hidden rounded-full ring-1 ring-border"
            style={{ width: CROP_STAGE_SIZE, height: CROP_STAGE_SIZE }}
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={endDrag}
            onPointerCancel={endDrag}
            data-testid="avatar-crop-stage"
          >
            <img
              ref={imgRef}
              src={src}
              alt=""
              crossOrigin="anonymous"
              onLoad={onImgLoad}
              draggable={false}
              className="pointer-events-none absolute left-1/2 top-1/2 origin-center"
              style={{
                width: renderW,
                height: renderH,
                transform: `translate(-50%, -50%) translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
              }}
            />
          </div>

          <div className="flex w-full items-center gap-2 text-muted-foreground">
            <ZoomIn className="size-4 shrink-0" />
            <Slider
              value={[zoom]}
              min={MIN_ZOOM}
              max={MAX_ZOOM}
              step={0.01}
              onValueChange={(v) => handleZoomChange(Array.isArray(v) ? v[0] : v)}
              disabled={busy}
              aria-label={t(($) => $.side_panel.avatar_crop_zoom_aria)}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t(($) => $.side_panel.avatar_crop_cancel)}
          </Button>
          <Button onClick={handleConfirm} disabled={busy || !natural}>
            {t(($) => $.side_panel.avatar_crop_save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
