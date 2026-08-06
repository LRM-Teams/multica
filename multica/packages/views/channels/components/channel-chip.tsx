import { Hash } from "lucide-react";

/**
 * Compact, presentation-only chip for a CHANNEL reference — `# <name>`, a
 * low-saturation rounded block in the same visual family as {@link IssueChip}
 * and {@link ProjectChip} (Iris #765: references get chips; person names stay
 * plain text — the pill Frank killed was for `@name`, NOT for refs).
 *
 * Deliberately not a link/button: callers wrap it in whatever interactive shell
 * they need. The `name` is rendered verbatim; a leading `#` is added only when
 * the caller's string doesn't already carry one (BE `target` may be "#multica"
 * or "multica").
 */
export interface ChannelChipProps {
  /** Channel display name, with or without a leading `#`. */
  name: string;
  /** Extra classes — callers layer interaction hints (e.g. `cursor-pointer hover:bg-accent`). */
  className?: string;
}

const BASE_CLASS =
  "channel-ref inline-flex items-center gap-0.5 rounded-md bg-muted px-1.5 py-0.5 text-xs font-medium text-foreground/80 max-w-52 align-middle";

export function ChannelChip({ name, className }: ChannelChipProps) {
  const label = name.startsWith("#") ? name.slice(1) : name;
  const cls = className ? `${BASE_CLASS} ${className}` : BASE_CLASS;
  return (
    <span className={cls} data-testid="channel-chip">
      <Hash className="size-3 shrink-0 text-muted-foreground" />
      <span className="truncate">{label}</span>
    </span>
  );
}
