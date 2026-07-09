import { cn } from "@multica/ui/lib/utils";

/**
 * Semantic mention kinds for body @mentions.
 *
 * Mentions are prose emphasis ("someone was addressed"), not chips/tags.
 * Baseline (Linear / GitHub / restrained Slack): brand-ink text in the flow.
 * Permanent fill is reserved for the *message row* when the viewer is
 * addressed — not every token. Per-actor rainbow colors (`agentColor`) stay
 * on avatars only.
 *
 * Visual language:
 * - default / agent / squad → brand ink, medium weight, no rest fill
 * - @all / self → same ink, semibold only (no permanent wash)
 * - hover / focus → soft brand wash (progressive)
 * - self-mentioned row → cool brand row wash (the real "you" signal)
 */
export type MentionTokenKind = "default" | "all" | "self";

/**
 * Resolve the visual kind for a mention:// token.
 * - `all` → broadcast emphasis (weight only at token level)
 * - member id matching the viewer → self emphasis (weight; row wash elsewhere)
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
 * Reads as inline brand-ink prose — no chip padding, no rest-state fill.
 */
export function mentionTokenClassName(
  kind: MentionTokenKind = "default",
  className?: string,
): string {
  return cn(
    "mention not-prose inline rounded-sm",
    "font-medium text-brand",
    "transition-colors duration-100",
    "hover:bg-brand/[0.08]",
    "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-brand/30 focus-visible:bg-brand/[0.08]",
    // @all / self: weight step only — the row wash carries "you were addressed".
    (kind === "all" || kind === "self") && "font-semibold",
    className,
  );
}

/**
 * Message-row wash when the body addresses the viewer (@me / @all).
 * Cool brand tint — product family. Deep-link highlight layers above via cn order.
 */
export const SELF_MENTION_ROW_CLASS =
  "bg-brand/[0.04] hover:bg-brand/[0.07] focus-within:bg-brand/[0.07]";
