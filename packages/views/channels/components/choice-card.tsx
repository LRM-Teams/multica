"use client";

import * as React from "react";
import { api } from "@multica/core/api";
import type { MessagePart } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";

type ChoicePart = Extract<MessagePart, { type: "choice" }>;

export function ChoiceCard({
  part,
  channelId,
  messageId,
}: {
  part: ChoicePart;
  channelId?: string;
  messageId?: string;
}) {
  const [pendingOptionId, setPendingOptionId] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const locked = Boolean(part.selected_option_id);
  const canChoose = Boolean(channelId && messageId) && !locked && !pendingOptionId;

  const onSelect = async (optionId: string) => {
    if (!canChoose || !channelId || !messageId) return;
    setPendingOptionId(optionId);
    setError(null);
    try {
      await api.chooseChannelMessageOption(channelId, messageId, part.choice_id, optionId);
    } catch (err) {
      setError(err instanceof Error ? err.message : "选择失败");
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
          const selected = part.selected_option_id === opt.id;
          const pending = pendingOptionId === opt.id;
          return (
            <button
              key={opt.id}
              type="button"
              disabled={!canChoose}
              aria-label={opt.label}
              aria-pressed={selected}
              onClick={() => void onSelect(opt.id)}
              className={cn(
                "min-h-8 touch-manipulation rounded-md border px-3 py-2 text-left text-sm transition-colors",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                selected
                  ? "border-primary bg-primary/10 text-foreground"
                  : "border-border bg-background hover:bg-accent/60",
                !canChoose && !selected && "opacity-60",
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
      {error ? <p className="mt-1.5 text-xs text-destructive">{error}</p> : null}
    </div>
  );
}

export function ChoiceReplyPart({ part }: { part: Extract<MessagePart, { type: "choice_reply" }> }) {
  return (
    <p className="text-sm text-muted-foreground" data-testid="choice-reply">
      选择：{part.label}
    </p>
  );
}
