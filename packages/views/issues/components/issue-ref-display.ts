import type { Issue } from "@multica/core/types";

/** True when `s` is a canonical UUID (an explicit mention) rather than a
 *  human identifier like "MUL-123" (an auto-linked reference). */
export function isIssueUuid(s: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s);
}

/**
 * Visible label for an issue reference in message bodies (LRM-493).
 *
 * Prefer a human identifier / title over a raw UUID — same口径 as LRM-423
 * aggregates (`issue_identifier`, then title). Never silently truncate to
 * `uuid.slice(0, 8)` (LRM-238).
 *
 * Author span text that is already an identifier (`LRM-487`, `#MUL-9`) stays
 * verbatim (#467/#600). Only UUID-shaped (or empty) tokens are upgraded.
 */
export function resolveIssueRefDisplayText(opts: {
  /** Anchored span substring or markdown link text. */
  text?: string | null;
  /** Structured part `label` when the span itself is a raw id. */
  label?: string | null;
  /** Live issue when resolved — identity + title only. */
  issue?: Pick<Issue, "id" | "identifier" | "title"> | null;
}): string {
  const text = opts.text?.trim() || "";
  const label = opts.label?.trim() || "";
  const identifier = opts.issue?.identifier?.trim() || "";
  const title = opts.issue?.title?.trim() || "";
  const issueId = opts.issue?.id?.trim() || "";

  const isRawId = (value: string) =>
    !value || isIssueUuid(value) || (issueId !== "" && value === issueId);

  // Author already wrote a human token — keep it (including a leading `#`).
  if (text && !isRawId(text)) return text;
  if (label && !isRawId(label)) return label;
  if (identifier) return identifier;
  if (title) return title;
  // Unresolved + only a UUID available: do not leak/truncate the id. Empty
  // string leaves the link navigable (href still uses issueId) without a
  // fake stand-in; once the issue resolves, identifier fills in.
  return "";
}
