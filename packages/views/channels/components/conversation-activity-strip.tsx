"use client";

import { useMemo } from "react";
import { useT } from "../../i18n/use-t";

export interface ConversationTypingActor {
  actorName: string;
}

const EMPTY_TYPING_ACTORS: readonly ConversationTypingActor[] = [];

/**
 * Bottom composer / reply strip shared by group channels and thread replies.
 * This strip is conversation-scoped, so it only renders transient human typing
 * events that belong to the open conversation. Workspace-wide Agent activity
 * belongs on Agent surfaces and must not be presented as a reply to the current
 * conversation.
 */
export function ConversationActivityStrip({
  typingActors = EMPTY_TYPING_ACTORS,
}: {
  typingActors?: readonly ConversationTypingActor[];
}) {
  const { t } = useT("channels");
  const typingNames = useMemo(
    () =>
      typingActors.flatMap((actor) => {
        const name = actor.actorName.trim();
        return name ? [name] : [];
      }),
    [typingActors],
  );

  const typingLabel =
    typingNames.length === 0
      ? null
      : typingNames.length === 1
        ? t(($) => $.typing.single, { name: typingNames[0]! })
        : typingNames.length === 2
          ? t(($) => $.typing.pair, { a: typingNames[0]!, b: typingNames[1]! })
          : t(($) => $.typing.overflow, {
              a: typingNames[0]!,
              b: typingNames[1]!,
              count: typingNames.length,
            });

  if (!typingLabel) return null;

  return (
    <div
      className="flex min-h-6 flex-col gap-1 px-5 pb-2 text-xs text-muted-foreground"
      aria-live="polite"
      data-testid="conversation-activity-strip"
    >
      <span
        className="flex min-w-0 items-center gap-1 truncate"
        data-testid="conversation-typing-row"
      >
        <span className="truncate">{typingLabel}</span>
        <TypingDots />
      </span>
    </div>
  );
}

function TypingDots() {
  return (
    <span className="flex shrink-0 items-end gap-0.5" aria-hidden="true">
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60 [animation-delay:-0.24s]" />
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60 [animation-delay:-0.12s]" />
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60" />
    </span>
  );
}
