"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { channelAttachmentsOptions } from "@multica/core/channels";
import { useActorName } from "@multica/core/workspace/hooks";
import type { Attachment } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useAttachmentPreview, useDownloadAttachment } from "../../editor";
import { formatFileSize, getFileExtension } from "../../editor/utils/file-meta";
import { useT } from "../../i18n";
import { useMessageTime } from "../../i18n/use-message-time";

/**
 * Channel settings Files tab (LRM-461 lock A / LRM-607): list message
 * attachments uploaded in this channel via GET /api/channels/{id}/attachments.
 * Never falls back to project-files / GitHub tree (LRM-238).
 */
export function ChannelFilesPanel({ channelId }: { channelId: string }) {
  const { t } = useT("channels");
  const { data, isPending, isError, refetch, isFetching } = useQuery(
    channelAttachmentsOptions(channelId),
  );
  const attachments = data ?? [];
  const sorted = useMemo(
    () =>
      [...attachments].sort(
        (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
      ),
    [attachments],
  );
  const preview = useAttachmentPreview();
  const download = useDownloadAttachment();

  if (isPending) {
    return (
      <div className="space-y-2" data-testid="channel-files-loading">
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
      <ul className="max-h-80 list-none space-y-0 overflow-auto p-0" data-testid="channel-files-list">
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

  return (
    <li
      className="flex items-center gap-2.5 border-b border-border/70 px-1 py-2 last:border-b-0"
      data-testid="channel-file-row"
    >
      <div
        className="grid size-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-[10px] font-bold text-brand"
        aria-hidden
      >
        {ext.slice(0, 4)}
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-semibold text-foreground">
          {attachment.filename}
        </div>
        <div className="truncate text-[11px] text-muted-foreground">{meta}</div>
      </div>
      <div className="flex shrink-0 gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className={cn("h-7 px-2 text-[11px] text-brand")}
          onClick={onOpen}
        >
          {t(($) => $.files.open)}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 px-2 text-[11px]"
          onClick={onDownload}
        >
          {t(($) => $.files.download)}
        </Button>
      </div>
    </li>
  );
}
