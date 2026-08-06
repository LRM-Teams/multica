/**
 * LRM-840 — stage gate human confirm helpers.
 *
 * Approve uses POST /confirm (session → completed).
 * Reject posts a user chat tip so the fleet lead can revise; BE resumes
 * awaiting_user_confirm → running on user message (same family as paused resume).
 */

export function formatStageGateRejectReply(reason?: string): string {
  const trimmed = (reason ?? "").trim();
  if (trimmed) {
    return `驳回确认：${trimmed}`;
  }
  return "驳回确认：请根据意见继续修订调研交付。";
}
