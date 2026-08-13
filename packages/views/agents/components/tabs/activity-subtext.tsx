"use client";

import { useQuery } from "@tanstack/react-query";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { channelsOptions } from "@multica/core/channels/queries";
import type { Channel } from "@multica/core/types";
import { useOptionalNavigation } from "../../../navigation/context";
import { AppLink } from "../../../navigation/app-link";
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
  const navigation = useOptionalNavigation();
  const parts = parseActivitySubtext(text);

  return (
    <span
      data-testid="runner-activity-subtext"
      className="mt-0.5 block whitespace-pre-wrap break-words text-[12.5px] leading-relaxed text-muted-foreground"
    >
      {parts.map((part, index) => {
        if (part.kind === "text") return <span key={index}>{part.value}</span>;
        const href =
          channelDetail && channels
            ? resolveActivityHandleHref(part.value, channels as Channel[], channelDetail)
            : null;
        if (!href) {
          return (
            <span key={index} className={HANDLE_CLASS}>
              {part.value}
            </span>
          );
        }
        const className = `${HANDLE_CLASS} text-primary hover:underline`;
        return navigation ? (
          <AppLink key={index} href={href} className={className} title={part.value}>
            {part.value}
          </AppLink>
        ) : (
          <a key={index} href={href} className={className} title={part.value}>
            {part.value}
          </a>
        );
      })}
    </span>
  );
}
