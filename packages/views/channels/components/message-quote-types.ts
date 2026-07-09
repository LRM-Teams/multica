import type { ChannelMessage, ChannelMessageReply } from "@multica/core/types";

export type MessageQuoteStatus = "available" | "deleted" | "inaccessible" | (string & {});

export type QuoteTarget = Pick<
  ChannelMessage,
  "id" | "channel_id" | "author_name" | "content" | "parts" | "attachments"
>;

export type QuoteSource = (ChannelMessageReply & {
  status?: MessageQuoteStatus | null;
  deleted_at?: string | null;
  attachments?: ChannelMessage["attachments"];
}) | null | undefined;

export interface QuotePreviewSource {
  content?: string | null;
  parts?: ChannelMessage["parts"];
  attachments?: ChannelMessage["attachments"];
}
