"use client";

import { useQuery } from "@tanstack/react-query";
import {
  conversationHandleLookupOptions,
  parseConversationHandle,
} from "@multica/core/conversations";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { channelsOptions } from "@multica/core/channels/queries";
import type { Channel } from "@multica/core/types";
import { useOptionalNavigation } from "../../../navigation/context";
import { AppLink } from "../../../navigation/app-link";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import {
  parseActivitySubtext,
  resolveActivityHandleHref,
} from "./activity-subtext-target";

const HANDLE_CLASS =
  "inline-flex max-w-full items-center truncate rounded-md bg-muted px-1.5 py-0.5 font-medium text-foreground";

export function ActivitySubtext({ text, workspaceId }: { text: string; workspaceId: string }) {
  const slug = useWorkspaceSlug();
  const { data: channels } = useQuery({
    ...channelsOptions(workspaceId),
    enabled: Boolean(workspaceId),
  });
  const channelDetail = slug ? paths.workspace(slug).channelDetail : undefined;
  const parts = parseActivitySubtext(text);
  let offset = 0;

  return (
    <span
      data-testid="runner-activity-subtext"
      className="mt-0.5 block whitespace-pre-wrap break-words text-[12.5px] leading-relaxed text-muted-foreground"
    >
      {parts.map((part) => {
        const key = `${offset}-${part.kind}`;
        offset += part.value.length;
        if (part.kind === "text") return <span key={key}>{part.value}</span>;
        return (
          <ActivityHandle
            key={key}
            handle={part.value}
            workspaceId={workspaceId}
            channels={channels as Channel[] | undefined}
            channelDetail={channelDetail}
          />
        );
      })}
    </span>
  );
}

function ActivityHandle({
  handle,
  workspaceId,
  channels,
  channelDetail,
}: {
  handle: string;
  workspaceId: string;
  channels: Channel[] | undefined;
  channelDetail: ((id: string) => string) | undefined;
}) {
  const parsed = parseConversationHandle(handle);
  const { data: lookup } = useQuery({
    ...conversationHandleLookupOptions(workspaceId, handle),
    enabled:
      Boolean(workspaceId) && parsed?.kind === "channel" && parsed.messagePrefix != null,
  });
  const navigation = useOptionalNavigation();
  const href = parsed?.messagePrefix
    ? resolveActivityHandleHref(handle, [], () => "", lookup)
    : channelDetail && channels
      ? resolveActivityHandleHref(handle, channels, channelDetail)
      : null;
  if (!href) {
    return <span className={HANDLE_CLASS}>{handle}</span>;
  }
  const className = `${HANDLE_CLASS} text-primary hover:underline`;
  return navigation ? (
    <Tooltip>
      <TooltipTrigger render={<AppLink href={href} className={className}>{handle}</AppLink>} />
      <TooltipContent side="top">{handle}</TooltipContent>
    </Tooltip>
  ) : (
    <Tooltip>
      <TooltipTrigger render={<a href={href} className={className}>{handle}</a>} />
      <TooltipContent side="top">{handle}</TooltipContent>
    </Tooltip>
  );
}
