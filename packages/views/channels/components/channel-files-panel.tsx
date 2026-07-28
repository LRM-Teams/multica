"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { channelAttachmentsOptions } from "@multica/core/channels";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { useActorName } from "@multica/core/workspace/hooks";
import type { Attachment } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useAttachmentPreview, useDownloadAttachment, isPreviewable } from "../../editor";
import { formatFileSize, getFileExtension } from "../../editor/utils/file-meta";
import { getPreviewKind } from "../../editor/utils/preview";
import { useT } from "../../i18n";
import { useMessageTime } from "../../i18n/use-message-time";

/**
 * Channel files list (LRM-461 lock A / LRM-607 / LRM-675): message
 * attachments uploaded in this channel via GET /api/channels/{id}/attachments.
 * Never falls back to project-files / GitHub tree (LRM-238).
 *
 * Hosts: channel-details Files drill-down (compact) and the main-area
 * 「文件」 tab (wide — LRM-675). Image rows show a real thumbnail (visible
 * preview); clicking anywhere on the row opens the shared preview modal
 * (lightbox for images, rendered md / monospaced txt). Non-previewable
 * binaries open nothing and keep Download as the only action.
 */
export function ChannelFilesPanel({
  channelId,
  wide = false,
}: {
  channelId: string;
  wide?: boolean;
}) {
  const { t } = useT("channels");
  const { data, isPending, isError, refetch, isFetching } = useQuery(
    channelAttachmentsOptions(channelId),
  );
  const sorted = useMemo(
    () =>
      (data ?? []).toSorted(
        (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
      ),
    [data],
  );
  const preview = useAttachmentPreview();
  const download = useDownloadAttachment();

  if (isPending) {
    return (
      <div className="space-y-2 p-3" data-testid="channel-files-loading">
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
      </div>
    );
  }

  if (isError) {
    return (
      <div
        className="mx-1 my-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-4 text-center text-xs text-destructive"
        data-testid="channel-files-error"
      >
        <p>{t(($) => $.files.error)}</p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-2 h-7 border-destructive/30 text-destructive"
          disabled={isFetching}
          onClick={() => void refetch()}
        >
          {t(($) => $.files.retry)}
        </Button>
      </div>
    );
  }

  if (sorted.length === 0) {
    return (
      <div
        className="px-3 py-9 text-center text-xs leading-relaxed text-muted-foreground"
        data-testid="channel-files-empty"
      >
        <strong className="mb-1 block text-[13px] font-semibold text-foreground">
          {t(($) => $.files.empty_title)}
        </strong>
        {t(($) => $.files.empty_hint)}
      </div>
    );
  }

  return (
    <>
      <ul
        className={cn(
          "list-none space-y-0 overflow-auto p-0",
          wide ? "flex-1" : "max-h-80",
        )}
        data-testid="channel-files-list"
      >
        {sorted.map((att) => (
          <ChannelAttachmentRow
            key={att.id}
            attachment={att}
            onOpen={() => {
              const opened = preview.tryOpen({ kind: "full", attachment: att });
              if (!opened) void download(att.id);
            }}
            onDownload={() => void download(att.id)}
          />
        ))}
      </ul>
      {preview.modal}
    </>
  );
}

function ChannelAttachmentRow({
  attachment,
  onOpen,
  onDownload,
}: {
  attachment: Attachment;
  onOpen: () => void;
  onDownload: () => void;
}) {
  const { t } = useT("channels");
  const messageTime = useMessageTime();
  const { getMemberName, getAgentName } = useActorName();
  const uploader =
    attachment.uploader_type === "agent"
      ? getAgentName(attachment.uploader_id)
      : getMemberName(attachment.uploader_id);
  const ext = getFileExtension(attachment.filename).toUpperCase() || "FILE";
  const size = formatFileSize(attachment.size_bytes);
  const when = messageTime.format(attachment.created_at);
  const meta = [uploader, when, size].filter(Boolean).join(" · ");
  const kind = getPreviewKind(attachment.content_type, attachment.filename);
  const previewable = isPreviewable(attachment.content_type, attachment.filename);
  const thumbUrl =
    kind === "image"
      ? (resolvePublicFileUrl(attachment.download_url) ?? attachment.download_url)
      : "";

  return (
    <li
      className="group border-b border-border/70 transition-colors last:border-b-0 hover:bg-muted/60"
      data-testid="channel-file-row"
    >
      {/* react-doctor-disable-next-line react-doctor/click-events-have-key-events, react-doctor/no-static-element-interactions -- row body click mirrors the row's explicit Open/Download buttons (keyboard/AT path); click-anywhere is the LRM-675 design affordance */}
      <div
        className="flex cursor-pointer items-center gap-2.5 px-3 py-2"
        onClick={onOpen}
      >
        {kind === "image" && thumbUrl ? (
          <span
            className="size-10 shrink-0 overflow-hidden rounded-md border border-border bg-muted"
            aria-hidden
          >
            {/* thumbnail only; object-cover crop per LRM-675 design */}
            {/* react-doctor-disable-next-line react-doctor/nextjs-no-img-element */}
            <img
              src={thumbUrl}
              alt=""
              loading="lazy"
              className="size-full object-cover"
              data-testid="channel-file-thumb"
            />
          </span>
        ) : (
          <div
            className="grid size-10 shrink-0 place-items-center rounded-md border border-border bg-brand/10 text-[10px] font-bold text-brand"
            aria-hidden
          >
            {ext.slice(0, 4)}
          </div>
        )}
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-semibold text-foreground">
            {attachment.filename}
          </div>
          <div className="truncate text-[11px] text-muted-foreground">{meta}</div>
        </div>
        <div className="flex shrink-0 gap-1.5 md:opacity-0 md:transition-opacity md:group-hover:opacity-100">
          {previewable ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className={cn("h-7 px-2 text-[11px] text-brand")}
              onClick={(e) => {
                e.stopPropagation();
                onOpen();
              }}
            >
              {t(($) => $.files.open)}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 px-2 text-[11px]"
            onClick={(e) => {
              e.stopPropagation();
              onDownload();
            }}
          >
            {t(($) => $.files.download)}
          </Button>
        </div>
      </div>
    </li>
  );
}
