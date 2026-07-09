import { cn } from "@multica/ui/lib/utils";

/**
 * Semantic mention-token kinds for body @mentions (Iris / Slack-like).
 *
 * Mentions are a *semantic* signal ("someone was addressed"), not an identity
 * palette. Member, agent, and squad share one low-sat brand hue; @all and
 * self stay in the same family with a light emphasis. Per-actor rainbow colors
 * (`agentColor`) stay on avatars only.
 */
export type MentionTokenKind = "default" | "all" | "self";

/**
 * Resolve the visual kind for a mention:// token.
 * - `all` → broadcast emphasis
 * - member id matching the viewer → self emphasis
 * - everything else (member/agent/squad/…) → default semantic pill
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
 * Shared class string for body/editor mention chips.
 * Hover + focus-visible deepen the brand wash; no per-id inline colors.
 */
export function mentionTokenClassName(
  kind: MentionTokenKind = "default",
  className?: string,
): string {
  return cn(
    "mention not-prose inline rounded-[0.3125rem] px-[0.3125rem] py-px mx-0.5",
    "font-semibold text-brand bg-brand/10",
    "transition-colors duration-100",
    "hover:bg-brand/20",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30 focus-visible:bg-brand/20",
    kind === "all" &&
      "bg-brand/[0.16] ring-1 ring-inset ring-brand/20 hover:bg-brand/25 focus-visible:bg-brand/25",
    kind === "self" && "bg-brand/[0.14] hover:bg-brand/22 focus-visible:bg-brand/22",
    className,
  );
}
