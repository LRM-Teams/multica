"use client";

import * as React from "react";
import { api } from "@multica/core/api";
import type { MessagePart } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

type ChoicePart = Extract<MessagePart, { type: "choice" }>;

/** First pick + one reselect, then locked (matches server maxChoiceSelectCount). */
const MAX_CHOICE_SELECT_COUNT = 2;

function choiceSelectCount(part: ChoicePart): number {
  if (!part.selected_option_id) return 0;
  if (part.select_count && part.select_count > 0) return part.select_count;
  return 1;
}

export function ChoiceCard({
  part,
  channelId,
  messageId,
}: {
  part: ChoicePart;
  channelId?: string;
  messageId?: string;
}) {
  const { t } = useT("channels");
  const [pendingOptionId, setPendingOptionId] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [localSelected, setLocalSelected] = React.useState<string | undefined>(undefined);
  const [localCount, setLocalCount] = React.useState<number | undefined>(undefined);

  const serverCount = choiceSelectCount(part);
  const selectedId = localSelected ?? part.selected_option_id;
  const selectCount = localCount ?? serverCount;
  const locked = selectCount >= MAX_CHOICE_SELECT_COUNT;
  const canChoose = Boolean(channelId && messageId) && !locked && !pendingOptionId;

  const onSelect = async (optionId: string) => {
    if (!canChoose || !channelId || !messageId) return;
    if (optionId === selectedId) return;
    setPendingOptionId(optionId);
    setError(null);
    try {
      const res = await api.chooseChannelMessageOption(channelId, messageId, part.choice_id, optionId);
      const nextPart = res.message?.parts?.find(
        (p): p is ChoicePart => p.type === "choice" && p.choice_id === part.choice_id,
      );
      if (nextPart?.selected_option_id) {
        setLocalSelected(nextPart.selected_option_id);
        setLocalCount(choiceSelectCount(nextPart));
      } else {
        setLocalSelected(optionId);
        setLocalCount(selectCount + 1);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t(($) => $.message.choice_failed));
    } finally {
      setPendingOptionId(null);
    }
  };

  const binary = part.layout === "binary";

  return (
    <div
      className="mt-1.5 min-w-[12rem] max-w-full rounded-lg border border-border/80 bg-muted/30 p-2.5"
      data-testid="choice-card"
      data-layout={part.layout}
      data-locked={locked ? "true" : "false"}
      data-select-count={selectCount}
    >
      {part.prompt ? (
        <p className="mb-2 text-sm font-medium text-foreground">{part.prompt}</p>
      ) : null}
      <div
        className={cn(
          "gap-2",
          binary ? "grid grid-cols-2" : "flex flex-col",
        )}
      >
        {part.options.map((opt) => {
          const selected = selectedId === opt.id;
          const pending = pendingOptionId === opt.id;
          return (
            <button
              key={opt.id}
              type="button"
              disabled={!canChoose || selected}
              aria-label={opt.label}
              aria-pressed={selected}
              onClick={() => void onSelect(opt.id)}
              className={cn(
                "min-h-8 touch-manipulation rounded-md border px-3 py-2 text-left text-sm transition-colors",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                selected
                  ? "border-primary bg-primary/10 text-foreground"
                  : "border-border bg-background hover:bg-accent/60",
                (!canChoose || selected) && !selected && "opacity-60",
                locked && !selected && "opacity-50",
                pending && "opacity-80",
              )}
            >
              <span className="font-medium">{opt.label}</span>
              {opt.description ? (
                <span className="mt-0.5 block text-xs text-muted-foreground">{opt.description}</span>
              ) : null}
            </button>
          );
        })}
      </div>
      {selectCount === 1 && !locked ? (
        <p className="mt-1.5 text-[11px] text-muted-foreground">
          {t(($) => $.message.choice_reselect_hint)}
        </p>
      ) : null}
      {locked ? (
        <p className="mt-1.5 text-[11px] text-muted-foreground">
          {t(($) => $.message.choice_locked)}
        </p>
      ) : null}
      {error ? <p className="mt-1.5 text-xs text-destructive">{error}</p> : null}
    </div>
  );
}

/** Structured-only for agent wake/context; human copy is the sibling text part. */
export function ChoiceReplyPart(_props: {
  part: Extract<MessagePart, { type: "choice_reply" }>;
}) {
  return null;
}
