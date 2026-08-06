"use client";

import { HoverCard, HoverCardTrigger, HoverCardContent } from "@multica/ui/components/ui/hover-card";
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

/** Directory-miss sentinels from useActorName — never show these in the hover card (LRM-364). */
const DIRECTORY_MISS_SENTINELS = new Set(["Unknown Agent", "Unknown"]);

function actorDisplayName(
  actor: { type: string; id: string },
  currentUserId: string | undefined,
  getActorName: (type: string, id: string) => string,
): string {
  if (actor.type === "member" && actor.id === currentUserId) return "You";
  const name = (getActorName(actor.type, actor.id) || "").trim();
  // Honest id placeholder while profile resolves / for deleted actors — never
  // the ListAgents miss sentinel (LRM-238 / LRM-364).
  if (!name || DIRECTORY_MISS_SENTINELS.has(name)) return actor.id;
  return name;
}

interface ReactionBarProps {
  reactions: ReactionItem[];
  currentUserId?: string;
  onToggle: (emoji: string) => void;
  getActorName: (type: string, id: string) => string;
  className?: string;
  hideAddButton?: boolean;
  quickEmojis?: string[];
  showQuickReactions?: boolean;
}

function ReactionBar({
  reactions,
  currentUserId,
  onToggle,
  getActorName,
  className,
  hideAddButton,
  quickEmojis = [],
  showQuickReactions = true,
}: ReactionBarProps) {
  const grouped = groupReactions(reactions, currentUserId);
  const groupedEmojis = new Set(grouped.map((g) => g.emoji));
  const quickOnly = showQuickReactions
    ? quickEmojis.filter((emoji) => !groupedEmojis.has(emoji))
    : [];

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
      {grouped.map((g) => {
        const actors = [...g.actors]
          .sort((a, b) => {
            const aIsCurrent = a.type === "member" && a.id === currentUserId;
            const bIsCurrent = b.type === "member" && b.id === currentUserId;
            return Number(bIsCurrent) - Number(aIsCurrent);
          })
          .map((a) => ({
            ...a,
            name: actorDisplayName(a, currentUserId, getActorName),
          }));
        const actorSummary = actors.map((actor) => actor.name).join(", ");
        // Attribution lives only in HoverCardContent. Do not also set
        // `title` — the native browser tooltip would stack under the card
        // and show the same actor names twice.
        return (
          <HoverCard key={g.emoji}>
            <HoverCardTrigger
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
            <HoverCardContent
              side="top"
              align="start"
              sideOffset={2}
              className="w-auto max-w-64 rounded-md border border-border/70 bg-popover px-2.5 py-1.5 text-xs text-foreground shadow-none ring-0"
            >
              <span className="block truncate">{actorSummary}</span>
            </HoverCardContent>
          </HoverCard>
        );
      })}
      {!hideAddButton && <QuickEmojiPicker onSelect={onToggle} />}
    </div>
  );
}

export { ReactionBar, type ReactionBarProps, type ReactionItem };
