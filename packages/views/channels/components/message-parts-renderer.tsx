"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { stickerCatalogOptions } from "@multica/core/stickers";
import { api } from "@multica/core/api";
import type { MessagePart, StickerAsset, StickerCatalogResponse } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { MemoizedMarkdown } from "../../common/markdown";
import { useT } from "../../i18n/use-t";

const SAFE_STICKER_ID = /^[a-z0-9-]+$/;
const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

interface StickerAssetIndex {
  byStickerId: Map<string, StickerAsset>;
  byPackAndStickerId: Map<string, StickerAsset>;
}

export function MessagePartsRenderer({
  parts,
  highlightQuery,
}: {
  parts: MessagePart[];
  highlightQuery?: string;
}) {
  const keyCounts = new Map<string, number>();
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {parts.map((part) => {
        const key = createMessagePartKey(part, keyCounts);
        if (part.type === "text") {
          if (!part.text.trim()) return null;
          return (
            <MemoizedMarkdown
              key={key}
              highlightQuery={highlightQuery}
              enableStickerShortcodes={false}
            >
              {part.text}
            </MemoizedMarkdown>
          );
        }
        if (part.type === "sticker") {
          return <StickerPart key={key} part={part} />;
        }
        return null;
      })}
    </div>
  );
}

function StickerPart({ part }: { part: Extract<MessagePart, { type: "sticker" }> }) {
  const { t } = useT("channels");
  const { data: catalog, isError: catalogIsError } = useQuery(stickerCatalogOptions());
  const [failed, setFailed] = React.useState(false);
  const prefersReducedMotion = usePrefersReducedMotion();
  const packId = part.pack_id;
  const stickerId = part.sticker_id;
  const assetIndex = React.useMemo(() => buildStickerAssetIndex(catalog), [catalog]);
  const asset = findStickerAsset(assetIndex, { pack_id: packId, sticker_id: stickerId });

  if (!SAFE_STICKER_ID.test(stickerId)) {
    return <StickerPlaceholder label={t(($) => $.message.sticker_unavailable)} />;
  }

  if (!catalog && !catalogIsError) {
    return <StickerPlaceholder label={t(($) => $.message.sticker_loading)} muted />;
  }

  if (!asset?.asset_url) {
    return <StickerPlaceholder label={t(($) => $.message.sticker_unavailable)} />;
  }

  const alt = safeStickerAlt(part, asset, t(($) => $.message.sticker_alt));
  if (failed) {
    return <StickerPlaceholder label={t(($) => $.message.sticker_failed)} title={alt} />;
  }

  if (asset.animated && prefersReducedMotion) {
    return <StickerPlaceholder label={alt} muted />;
  }

  return (
    <StickerImage
      src={absolutizeStickerURL(asset.asset_url)}
      alt={alt}
      onError={() => setFailed(true)}
    />
  );
}

function createMessagePartKey(part: MessagePart, counts: Map<string, number>): string {
  const base =
    part.type === "text"
      ? `text-${hashString(part.text)}`
      : `sticker-${part.pack_id ?? "default"}-${part.sticker_id}-${hashString(part.alt ?? "")}`;
  const count = (counts.get(base) ?? 0) + 1;
  counts.set(base, count);
  return count === 1 ? base : `${base}-${count}`;
}

function hashString(value: string): string {
  let hash = 0;
  for (let i = 0; i < value.length; i += 1) {
    hash = (hash << 5) - hash + value.charCodeAt(i);
    hash |= 0;
  }
  return Math.abs(hash).toString(36);
}

function buildStickerAssetIndex(catalog: StickerCatalogResponse | undefined): StickerAssetIndex {
  const byStickerId = new Map<string, StickerAsset>();
  const byPackAndStickerId = new Map<string, StickerAsset>();
  for (const pack of catalog?.packs ?? []) {
    for (const asset of pack.stickers) {
      if (!byStickerId.has(asset.sticker_id)) {
        byStickerId.set(asset.sticker_id, asset);
      }
      byPackAndStickerId.set(packStickerKey(pack.id, asset.sticker_id), asset);
    }
  }
  return { byStickerId, byPackAndStickerId };
}

function findStickerAsset(
  index: StickerAssetIndex,
  part: Pick<Extract<MessagePart, { type: "sticker" }>, "pack_id" | "sticker_id">,
): StickerAsset | undefined {
  if (part.pack_id) {
    return index.byPackAndStickerId.get(packStickerKey(part.pack_id, part.sticker_id));
  }
  return index.byStickerId.get(part.sticker_id);
}

function packStickerKey(packId: string, stickerId: string): string {
  return `${packId}\u0000${stickerId}`;
}

function safeStickerAlt(
  part: Extract<MessagePart, { type: "sticker" }>,
  asset: StickerAsset,
  fallback: string,
): string {
  const candidates = [part.alt, asset.alt, asset.name, asset.name_en, fallback];
  for (const candidate of candidates) {
    const value = candidate?.trim();
    if (value && value !== part.sticker_id && value !== `:sticker:${part.sticker_id}:`) {
      return value;
    }
  }
  return fallback;
}

function absolutizeStickerURL(src: string): string {
  const baseUrl = (api.getBaseUrl?.() ?? "").replace(/\/+$/, "");
  return baseUrl && src.startsWith("/") ? `${baseUrl}${src}` : src;
}

function StickerPlaceholder({
  label,
  title,
  muted = false,
}: {
  label: string;
  title?: string;
  muted?: boolean;
}) {
  return (
    <span
      data-testid="message-sticker-placeholder"
      title={title}
      className={cn(
        "not-prose inline-flex min-h-20 w-fit max-w-32 items-center justify-center rounded-md border border-dashed border-border/80 px-3 py-2 text-center text-xs sm:max-w-40",
        muted ? "bg-muted/25 text-muted-foreground" : "bg-muted/35 text-muted-foreground",
      )}
    >
      {label}
    </span>
  );
}

function StickerImage({
  src,
  alt,
  onError,
}: {
  src: string;
  alt: string;
  onError: () => void;
}) {
  return React.createElement("img", {
    "data-testid": "message-sticker",
    src,
    alt,
    className:
      "not-prose block h-auto w-auto max-h-32 max-w-32 select-none object-contain sm:max-h-40 sm:max-w-40",
    draggable: false,
    loading: "lazy",
    onError,
  });
}

function usePrefersReducedMotion(): boolean {
  return React.useSyncExternalStore(
    subscribeReducedMotion,
    getReducedMotionSnapshot,
    () => false,
  );
}

function subscribeReducedMotion(onStoreChange: () => void): () => void {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return () => {};
  }
  const query = window.matchMedia(REDUCED_MOTION_QUERY);
  query.addEventListener?.("change", onStoreChange);
  return () => query.removeEventListener?.("change", onStoreChange);
}

function getReducedMotionSnapshot(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia(REDUCED_MOTION_QUERY).matches;
}
