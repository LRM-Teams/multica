import { cn } from "@multica/ui/lib/utils";

/**
 * Semantic mention kinds for body @mentions.
 *
 * Mentions are Slack-style soft-background emphasis links — not heavy
 * capsules and not bare brand ink. Person / agent / squad / @all share one
 * token; only @self uses a warm yellow wash. Per-actor rainbow colors
 * (`agentColor`) stay on avatars only.
 *
 * Visual language (design-mention-slack-token.html / LRM-269):
 * - default / all → brand ink + bold + soft brand rest fill, radius ≤4px, px ≤2px
 * - self → warm yellow fill (#faf0c8) + ink text
 * - hover / focus → slightly stronger wash
 * - self-mentioned row → warm row wash (#fef9e8)
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
 * Slack soft-bg token — brand ink + light rest fill; self is warm yellow.
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
      ? "bg-[#faf0c8] text-foreground hover:bg-[#f5e8a8] focus-visible:bg-[#f5e8a8]"
      : "bg-brand/[0.10] text-brand hover:bg-brand/[0.14] focus-visible:bg-brand/[0.14]",
    className,
  );
}

/**
 * Message-row wash when the body addresses the viewer (@me / @all).
 * Warm tint matching Slack self-row (#fef9e8). Deep-link highlight layers
 * above via cn order.
 */
export const SELF_MENTION_ROW_CLASS =
  "bg-[#fef9e8] hover:bg-[#fdf3d0] focus-within:bg-[#fdf3d0]";
