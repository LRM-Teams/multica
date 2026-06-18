"use client";

import { useQuery } from "@tanstack/react-query";
import { channelAttachmentsOptions } from "@multica/core/channels";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Attachment as AttachmentRenderer, AttachmentDownloadProvider } from "../../editor";
import { useT } from "../../i18n";

/**
 * Lists every file uploaded to the channel (newest first), rendered with the
 * shared attachment card so each is previewable / downloadable. Backed by the
 * channel_message_id / channel_id attachment links.
 */
export function ChannelFilesPanel({ channelId }: { channelId: string }) {
  const { t } = useT("channels");
  const { data: files = [], isPending } = useQuery(channelAttachmentsOptions(channelId));

  if (isPending) {
    return (
      <div className="space-y-1.5">
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
      </div>
    );
  }

  if (files.length === 0) {
    return <p className="py-6 text-center text-xs text-muted-foreground">{t(($) => $.files.empty)}</p>;
  }

  return (
    <AttachmentDownloadProvider attachments={files}>
      <div className="flex max-h-80 flex-col gap-1.5 overflow-y-auto">
        {files.map((a) => (
          <AttachmentRenderer key={a.id} attachment={{ kind: "record", attachment: a }} />
        ))}
      </div>
    </AttachmentDownloadProvider>
  );
}
