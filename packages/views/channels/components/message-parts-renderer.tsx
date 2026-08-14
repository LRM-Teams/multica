"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, FileText } from "lucide-react";
import { stickerCatalogOptions } from "@multica/core/stickers";
import { api } from "@multica/core/api";
import type { AgentCreationProposal, MessagePart, StickerAsset, StickerCatalogResponse } from "@multica/core/types";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@multica/ui/components/ui/collapsible";
import { cn } from "@multica/ui/lib/utils";
import { MemoizedMarkdown } from "../../common/markdown";
import { usePrefersReducedMotion } from "../../common/use-prefers-reduced-motion";
import { useT } from "../../i18n/use-t";
import { ChoiceCard, ChoiceReplyPart } from "./choice-card";
import { AgentCreationProposalCard } from "../../common/agent-creation-proposal-card";
import { AppLink } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";

const SAFE_STICKER_ID = /^[a-z0-9-]+$/;

interface StickerAssetIndex {
  byStickerId: Map<string, StickerAsset>;
  byPackAndStickerId: Map<string, StickerAsset>;
}

export function MessagePartsRenderer({
  parts,
  highlightQuery,
  choiceContext,
}: {
  parts: MessagePart[];
  highlightQuery?: string;
  choiceContext?: { channelId: string; messageId: string };
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
              mentionVariant="plain"
            >
              {part.text}
            </MemoizedMarkdown>
          );
        }
        if (part.type === "sticker") {
          return <StickerPart key={key} part={part} />;
        }
        if (part.type === "choice") {
          return (
            <ChoiceCard
              key={key}
              part={part}
              channelId={choiceContext?.channelId}
              messageId={choiceContext?.messageId}
            />
          );
        }
        if (part.type === "choice_reply") {
          return <ChoiceReplyPart key={key} part={part} />;
        }
        if (part.type === "note_brief") {
          return <NoteBriefPart key={key} part={part} />;
        }
        if (part.type === "reference") {
          if (part.ref_type === "agent:create" && choiceContext?.messageId) {
            return (
              <AgentCreationProposalCard
                key={key}
                proposal={agentCreationProposalFromPart(part, choiceContext.messageId)}
              />
            );
          }
          return null;
        }
        return null;
      })}
    </div>
  );
}

function NoteBriefPart({ part }: { part: Extract<MessagePart, { type: "note_brief" }> }) {
  const { t } = useT("channels");
  const paths = useWorkspacePaths();
  const title = part.label?.trim() || t(($) => $.message.note_brief_untitled);
  const body = part.text?.trim() || t(($) => $.message.note_brief_empty);
  const pageId = part.ref_id?.trim() || "";

  return (
    <Collapsible defaultOpen={false} className="mt-1 overflow-hidden rounded-md border bg-muted/20">
      <CollapsibleTrigger
        render={
          <button
            type="button"
            data-testid="note-brief-toggle"
            aria-label={t(($) => $.message.note_brief_toggle_aria, { title })}
            className="group/note-brief flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-[13px] hover:bg-muted/40"
          />
        }
      >
        <ChevronRight className="size-3.5 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[panel-open]/note-brief:rotate-90" />
        <FileText className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-medium text-foreground">{title}</span>
        <span className="shrink-0 text-[11px] text-muted-foreground">
          {t(($) => $.message.note_brief_collapsed_hint)}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div
          data-testid="note-brief-body"
          className="max-h-56 space-y-2 overflow-y-auto whitespace-pre-wrap break-words border-t px-2.5 py-2 text-[13px] leading-5 text-muted-foreground"
        >
          <div>{body}</div>
          {pageId ? (
            <AppLink
              href={paths.noteDetail(pageId)}
              data-testid="note-brief-open-note"
              className="inline-flex text-[12px] font-medium text-primary underline-offset-2 hover:underline"
              onClick={(event) => event.stopPropagation()}
            >
              {t(($) => $.message.note_brief_open_note)}
            </AppLink>
          ) : null}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function agentCreationProposalFromPart(
  part: Extract<MessagePart, { type: "reference"; ref_type: "agent:create" }>,
  messageId: string,
): AgentCreationProposal {
  const params = part.params ?? {};
  return {
    message_id: messageId,
    name: params.name?.trim() || part.label?.trim() || part.ref_id,
    description: params.description?.trim() || "",
    preferred_computer: params.preferred_computer?.trim() || undefined,
    status: params.status === "executed" ? "executed" : "prepared",
    committer_user_id: params.committer_user_id,
    result_agent_id: params.result_agent_id,
  };
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
    return <StickerMotionReduced alt={alt} />;
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
  let base: string;
  if (part.type === "text") {
    base = `text-${hashString(part.text)}`;
  } else if (part.type === "sticker") {
    base = `sticker-${part.pack_id ?? "default"}-${part.sticker_id}-${hashString(part.alt ?? "")}`;
  } else if (part.type === "reference") {
    const refSubtype = "ref_subtype" in part ? (part.ref_subtype ?? "none") : "none";
    base = `reference-${part.ref_type}-${refSubtype}-${part.ref_id}`;
  } else if (part.type === "system_event") {
    base = `system-event-${part.event}-${hashString(JSON.stringify(part.event_params))}`;
  } else if (part.type === "voice") {
    base = `voice-${part.duration_ms ?? 0}`;
  } else if (part.type === "choice") {
    base = `choice-${part.choice_id}-${part.selected_option_id ?? "open"}`;
  } else if (part.type === "choice_reply") {
    base = `choice-reply-${part.choice_id}-${part.option_id}`;
  } else if (part.type === "note_brief") {
    base = `note-brief-${part.ref_id}-${hashString(part.label ?? "")}`;
  } else {
    base = `attachment-${part.attachment_id}`;
  }
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

/**
 * Animated sticker rendered for a user who asked for reduced motion.
 *
 * LRM-1373 — this is deliberately NOT `StickerPlaceholder`. That component's
 * dashed border is this repo's "missing / broken / still loading" language and
 * it is used by the unsafe-id, catalog-loading, asset-missing and image-failed
 * branches. Reusing it here told reduced-motion users their sticker was broken:
 * the rendered class list was byte-identical to the muted loading placeholder.
 * A solid border plus body-coloured alt text says "this is content, shown
 * statically on purpose", and the second line says why.
 *
 * There is no real still frame to show: `StickerAsset` carries no static or
 * thumbnail URL, and painting the animation into a canvas to grab frame 0 taints
 * it, because `absolutizeStickerURL` can resolve onto the API origin. So this
 * branch stays text — it just stops lying about being an error.
 */
function StickerMotionReduced({ alt }: { alt: string }) {
  const { t } = useT("channels");
  return (
    <span
      data-testid="message-sticker-motion-reduced"
      className="not-prose inline-flex min-h-20 w-fit max-w-32 flex-col items-center justify-center gap-1 rounded-md border border-border bg-muted/40 px-3 py-2 text-center sm:max-w-40"
    >
      <span className="text-xs text-foreground">{alt}</span>
      <span className="text-[11px] text-muted-foreground">
        {t(($) => $.message.sticker_motion_reduced)}
      </span>
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
  // #689 perf audit: a fixed box (not a max-height/width upper bound applied
  // only once the browser knows the image's natural size) reserves the same
  // rendered footprint before and after the async image load, so loading a
  // sticker never shifts the surrounding message list layout.
  return React.createElement("img", {
    "data-testid": "message-sticker",
    src,
    alt,
    className:
      "not-prose block size-32 select-none object-contain sm:size-40",
    draggable: false,
    loading: "lazy",
    onError,
  });
}
