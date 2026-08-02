import { cn } from "@multica/ui/lib/utils";

/**
 * Semantic mention kinds for body @mentions.
 *
 * Mentions are Slack-style soft-background emphasis links — not heavy
 * capsules and not bare brand ink. Person / agent / squad / @all share one
 * token; only @self uses a warm yellow wash. Per-actor rainbow colors
 * (`agentColor`) stay on avatars only.
 *
 * Visual language (design-mention-slack-token.html / LRM-269 / LRM-350):
 * - default / all → brand ink + bold + soft brand rest fill, radius ≤4px, px ≤2px
 * - self (light) → warm yellow fill (#faf0c8) + ink text
 * - self (dark) → brand tint fill + brand ink (never cream yellow on light text)
 * - hover / focus → slightly stronger wash
 * - self-mentioned row → light warm wash (#fef9e8); dark cool brand tint + left bar
 */
export type MentionTokenKind = "default" | "all" | "self";

/**
 * Resolve the visual kind for a mention:// token.
 * - `all` → same token as default (broadcast uses weight+fill already shared)
 * - member id matching the viewer → self emphasis (yellow token + row wash)
 * - everything else (member/agent/squad/…) → default
 */
export function resolveMentionTokenKind(
  type: string,
  id: string,
  viewerUserId?: string | null,
): MentionTokenKind {
  if (type === "all") return "all";
  if (type === "member" && viewerUserId && id === viewerUserId) return "self";
  return "default";
}

/**
 * Shared class string for body/editor mention text.
 * Slack soft-bg token — brand ink + light rest fill; self is warm yellow in
 * light mode and brand tint + brand ink in dark (LRM-350).
 * Keep padding thin (≤2px) and radius small (≤4px); never `rounded-full`.
 */
export function mentionTokenClassName(
  kind: MentionTokenKind = "default",
  className?: string,
): string {
  const isSelf = kind === "self";
  return cn(
    "mention not-prose inline rounded-sm px-0.5",
    "font-bold box-decoration-clone",
    "transition-colors duration-100",
    "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-brand/30",
    isSelf
      ? [
          "bg-[#faf0c8] text-foreground hover:bg-[#f5e8a8] focus-visible:bg-[#f5e8a8]",
          "dark:bg-brand/[0.14] dark:text-brand dark:hover:bg-brand/[0.18] dark:focus-visible:bg-brand/[0.18]",
        ]
      : "bg-brand/[0.10] text-brand hover:bg-brand/[0.14] focus-visible:bg-brand/[0.14]",
    className,
  );
}

/**
 * Message-row wash when the body addresses the viewer (@me / @all).
 * Light: warm tint (#fef9e8). Dark: 2px brand bar + cool brand tint (not
 * the light wash). Deep-link highlight layers above via cn order.
 */
export const SELF_MENTION_ROW_CLASS =
  "bg-[#fef9e8] hover:bg-[#fdf3d0] focus-within:bg-[#fdf3d0] dark:border-l-2 dark:border-brand dark:bg-brand/[0.06] dark:hover:bg-brand/[0.08] dark:focus-within:bg-brand/[0.08]";

/**
 * On dark self-mention rows, inline @mentions drop pill fill for contrast;
 * hover/focus only adds a light brand wash.
 */
export const SELF_MENTION_ROW_MENTION_CLASS =
  "[&_.mention]:dark:bg-transparent [&_.mention]:dark:hover:bg-brand/[0.08] [&_.mention]:dark:focus-visible:bg-brand/[0.08]";

export type MessageCollapseFadeVariant =
  | "default"
  | "self-mention"
  | "highlighted"
  | "search"
  | "muted";

export function resolveMessageCollapseFadeVariant(options: {
  selfMentioned: boolean;
  highlighted: boolean;
  searchHighlighted: boolean;
}): MessageCollapseFadeVariant {
  if (options.searchHighlighted) return "search";
  if (options.highlighted) return "highlighted";
  if (options.selfMentioned) return "self-mention";
  return "default";
}

/**
 * Bottom fade for collapsed long messages (LRM-302 / LRM-368). Must match the
 * visible message-row background — never hard-bind `background` on rows that
 * use a different wash (self-mention cream, deep-link ring, search tint).
 */
export function messageCollapseFadeClassName(
  variant: MessageCollapseFadeVariant,
): string {
  // LRM-987: taller opaque wash so collapsed bottom edge does not leak half-lines
  // of table/prose under the 「查看更多」control (screenshot: faint truncated row).
  const layout =
    "pointer-events-none absolute inset-x-0 bottom-0 flex justify-start pb-0.5 pt-14";
  switch (variant) {
    case "search":
      return cn(
        layout,
        "bg-gradient-to-t from-primary/5 from-35% via-primary/5 via-60% to-transparent",
      );
    case "highlighted":
      return cn(
        layout,
        "bg-gradient-to-t from-primary/10 from-35% via-primary/10 via-60% to-transparent",
      );
    case "self-mention":
      return cn(
        layout,
        "bg-gradient-to-t from-[#fef9e8] from-35% via-[#fef9e8] via-60% to-transparent dark:from-brand/[0.06] dark:via-brand/[0.06]",
      );
    case "muted":
      return cn(
        layout,
        "bg-gradient-to-t from-muted from-35% via-muted via-60% to-transparent",
      );
    default:
      return cn(
        layout,
        "bg-gradient-to-t from-background from-35% via-background via-60% to-transparent",
      );
  }
}
