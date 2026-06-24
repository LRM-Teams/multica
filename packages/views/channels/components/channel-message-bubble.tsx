"use client";

import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelMessage } from "@multica/core/types";
import { MemoizedMarkdown } from "../../common/markdown";
import { AttachmentList } from "../../issues/components/comment-card";
import { agentColor } from "../../common/agent-color";
import { initialsOf } from "../../common/initials";
import { useT } from "../../i18n";

function formatTime(value: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date(value));
  } catch {
    return "";
  }
}

/**
 * One message in the group-chat thread. Own messages right-align with a neutral
 * card bubble; everyone else left-aligns. Agents get a per-agent identity color
 * on the avatar plus an "Agent" pill, and a faint-primary bubble so agent
 * output reads as distinct from human messages at a glance. Body is rendered
 * through the shared Markdown pipeline (mentions, attachments, light formatting).
 */
export function ChannelMessageBubble({
  message,
  currentUserId,
  ownName,
  highlighted = false,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  /** Display name for the viewer's own messages. Defaults to the stored name. */
  ownName?: string;
  /** Deep-link target: briefly ring-highlights the message, then fades. */
  highlighted?: boolean;
}) {
  const { t } = useT("channels");
  const { getActorAvatarUrl } = useActorName();
  const isOwn =
    message.author_type === "user" &&
    message.author_id != null &&
    message.author_id === currentUserId;
  const isAgent = message.author_type === "agent";
  const isExternal = message.source === "lark";
  const tint = isAgent
    ? agentColor(message.author_id ?? message.author_name)
    : undefined;
  // Resolve the avatar from the live members/agents cache (keyed by id) rather
  // than a value snapshotted into the message — so a settings avatar change
  // shows up here too. Falls back to the tinted/initials avatar when the author
  // isn't a workspace member/agent (lark / system) or has no photo.
  const avatarUrl =
    message.author_id == null
      ? null
      : isAgent
        ? getActorAvatarUrl("agent", message.author_id)
        : message.author_type === "user"
          ? getActorAvatarUrl("member", message.author_id)
          : null;
  const displayName = isOwn ? ownName ?? message.author_name : message.author_name;

  return (
    <div
      id={`message-${message.id}`}
      data-testid="message-bubble"
      data-own={isOwn}
      className={cn(
        "group flex gap-2.5 rounded-lg px-2 py-2 transition-colors duration-1000",
        isOwn ? "flex-row-reverse" : "flex-row",
        highlighted && "bg-primary/10 ring-2 ring-primary/40 duration-0",
      )}
    >
      <ActorAvatar
        name={message.author_name}
        initials={initialsOf(message.author_name)}
        avatarUrl={avatarUrl ?? undefined}
        isAgent={isAgent}
        isSystem={message.author_type === "system"}
        size={28}
        className="mt-0.5"
        tint={tint}
      />
      <div
        className={cn(
          "flex min-w-0 max-w-[78%] flex-col gap-1",
          isOwn && "items-end",
        )}
      >
        <div
          className={cn(
            "flex items-center gap-2 text-sm",
            isOwn && "flex-row-reverse",
          )}
        >
          <span className="truncate font-medium text-foreground">{displayName}</span>
          {isAgent && (
            <span className="shrink-0 rounded-full border border-primary/20 bg-primary/[0.08] px-2 py-0.5 text-[11px] font-normal leading-none text-primary">
              {t(($) => $.message.agent_badge)}
            </span>
          )}
          {isExternal && (
            <span className="shrink-0 rounded-full border bg-muted px-2 py-0.5 text-[11px] leading-none text-muted-foreground">
              {t(($) => $.message.feishu_badge)}
            </span>
          )}
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {formatTime(message.created_at)}
          </span>
        </div>
        <div
          className={cn(
            // min-w-0 + break long tokens so a wide code span / URL wraps
            // inside the bubble instead of forcing the column wider.
            "w-fit min-w-0 max-w-full overflow-hidden break-words rounded-lg border px-3 py-2 text-sm leading-6",
            isOwn
              ? "border-border/60 bg-card"
              : "border-primary/20 bg-primary/[0.06]",
          )}
        >
          <MemoizedMarkdown attachments={message.attachments}>{message.content}</MemoizedMarkdown>
          <AttachmentList
            attachments={message.attachments}
            content={message.content}
            className="mt-1.5"
          />
        </div>
      </div>
    </div>
  );
}
