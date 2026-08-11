// Soft-fold helpers for Runner Activity body rows (2026-08-11 command
// readability). Timeline shell stays as-is; only default visibility of body
// content changes. Thresholds are product defaults — tune only if QA complains.

export const ACTIVITY_COMMAND_LONG_LINE_THRESHOLD = 12;
export const ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD = 2000;
export const ACTIVITY_COMMAND_FOLD_PREVIEW_LINES = 8;

/** True when a body should soft-fold instead of painting fully. */
export function isLongActivityCommand(content: string): boolean {
  const text = content.replace(/\r\n/g, "\n");
  if (text.length >= ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD) return true;
  let lines = 1;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10 /* \n */) {
      lines += 1;
      if (lines >= ACTIVITY_COMMAND_LONG_LINE_THRESHOLD) return true;
    }
  }
  return false;
}

/**
 * Prefix shown while a long body is folded. Prefers whole lines (first
 * ACTIVITY_COMMAND_FOLD_PREVIEW_LINES); single huge lines cut at the char cap.
 */
export function foldActivityCommandPreview(content: string): string {
  const text = content.replace(/\r\n/g, "\n");
  const lines = text.split("\n");
  if (lines.length > ACTIVITY_COMMAND_FOLD_PREVIEW_LINES) {
    return lines.slice(0, ACTIVITY_COMMAND_FOLD_PREVIEW_LINES).join("\n");
  }
  if (text.length > ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD) {
    return text.slice(0, ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD);
  }
  return text;
}
