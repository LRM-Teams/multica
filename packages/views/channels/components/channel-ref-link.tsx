"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { conversationMessageHref } from "@multica/core/conversations";
import { useWorkspacePaths } from "@multica/core/paths";
import { channelsOptions } from "@multica/core/channels/queries";
import type { Channel } from "@multica/core/types";
import { useOptionalNavigation } from "../../navigation/context";
import { AppLink } from "../../navigation/app-link";
import { ChannelChip } from "./channel-chip";

/**
 * An anchored channel reference (task #912) rendered from a `channel-ref`
 * message part. Mirrors {@link IssueRefLink}'s minimal shape (span-anchored,
 * plain `href` — `AppLink` and the browser already handle click/cmd-click/
 * new-tab correctly, no manual handler needed) without the hover card —
 * channels have no status/priority to preview, so a chip + link is the whole
 * affordance.
 *
 * Prefers the live channel name from the already-cached channel list (a
 * channel can be renamed after the message was sent) and falls back to the
 * anchored `label` the server resolved at send time when the channel isn't in
 * cache (not yet fetched, or since archived/deleted).
 */
export function ChannelRefLink({
  channelId,
  label,
  messageId,
  threadId,
}: {
  channelId: string;
  label?: string;
  messageId?: string;
  threadId?: string;
}) {
  const wsId = useWorkspaceId();
  const { data: channels } = useQuery(channelsOptions(wsId));
  const liveName = (channels as Channel[] | undefined)?.find((c) => c.id === channelId)?.name;
  const displayName = liveName ?? label ?? channelId;
  const paths = useWorkspacePaths();
  const navigation = useOptionalNavigation();
  const href = conversationMessageHref(paths.channelDetail(channelId), { messageId, threadId });

  const chip = (
    <ChannelChip name={displayName} className="cursor-pointer hover:bg-accent transition-colors" />
  );
  return navigation ? (
    <AppLink href={href} className="inline-flex align-middle">
      {chip}
    </AppLink>
  ) : (
    <a href={href} className="inline-flex align-middle">
      {chip}
    </a>
  );
}
