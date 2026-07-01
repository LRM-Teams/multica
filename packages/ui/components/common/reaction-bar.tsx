"use client";

import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { QuickEmojiPicker } from "./quick-emoji-picker";

interface ReactionItem {
  id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
}

interface GroupedReaction {
  emoji: string;
  count: number;
  reacted: boolean;
  actors: { type: string; id: string }[];
}

function groupReactions(reactions: ReactionItem[], currentUserId?: string): GroupedReaction[] {
  const map = new Map<string, GroupedReaction>();
  for (const r of reactions) {
    let group = map.get(r.emoji);
    if (!group) {
      group = { emoji: r.emoji, count: 0, reacted: false, actors: [] };
      map.set(r.emoji, group);
    }
    group.count++;
    group.actors.push({ type: r.actor_type, id: r.actor_id });
    if (r.actor_type === "member" && r.actor_id === currentUserId) {
      group.reacted = true;
    }
  }
  return Array.from(map.values());
}

interface ReactionBarProps {
  reactions: ReactionItem[];
  currentUserId?: string;
  onToggle: (emoji: string) => void;
  getActorName: (type: string, id: string) => string;
  className?: string;
  hideAddButton?: boolean;
  quickEmojis?: string[];
}

function ReactionBar({
  reactions,
  currentUserId,
  onToggle,
  getActorName,
  className,
  hideAddButton,
  quickEmojis = [],
}: ReactionBarProps) {
  const grouped = groupReactions(reactions, currentUserId);
  const groupedEmojis = new Set(grouped.map((g) => g.emoji));
  const quickOnly = quickEmojis.filter((emoji) => !groupedEmojis.has(emoji));

  return (
    <div className={`flex flex-wrap items-center gap-1.5 ${className ?? ""}`}>
      {quickOnly.map((emoji) => (
        <button
          key={`quick-${emoji}`}
          type="button"
          onClick={() => onToggle(emoji)}
          aria-label={`React with ${emoji}`}
          className="inline-flex h-8 min-w-8 touch-manipulation items-center justify-center rounded-full border border-brand/10 bg-brand/4 px-2 text-sm text-muted-foreground transition-colors hover:bg-brand/15 hover:text-foreground active:scale-95 md:h-6 md:min-w-6 md:text-xs"
        >
          {emoji}
        </button>
      ))}
      {grouped.map((g) => (
        <Tooltip key={g.emoji}>
          <TooltipTrigger
            render={
              <button
                type="button"
                onClick={() => onToggle(g.emoji)}
                className={`inline-flex h-8 touch-manipulation items-center gap-1 rounded-full border px-2 py-0.5 text-xs transition-colors hover:bg-brand/15 active:scale-95 md:h-6 ${
                  g.reacted
                    ? "border-brand/30 bg-brand/8 text-brand"
                    : "border-brand/10 bg-brand/4 text-muted-foreground"
                }`}
              >
                <span>{g.emoji}</span>
                <span>{g.count}</span>
              </button>
            }
          />
          <TooltipContent side="top">
            {g.actors.map((a) => getActorName(a.type, a.id)).join(", ")}
          </TooltipContent>
        </Tooltip>
      ))}
      {!hideAddButton && <QuickEmojiPicker onSelect={onToggle} />}
    </div>
  );
}

export { ReactionBar, type ReactionBarProps, type ReactionItem };
