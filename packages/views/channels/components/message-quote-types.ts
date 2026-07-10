import type { ChannelMessage } from "@multica/core/types";

export type QuoteTarget = Pick<
  ChannelMessage,
  "id" | "channel_id" | "author_name" | "content" | "parts" | "attachments"
>;
