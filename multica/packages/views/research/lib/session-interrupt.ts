import type { ResearchMessage } from "@multica/core/types";
import { shortProcessLine } from "./fleet-step-cards";

/** Stable reason codes from BE wake_failed meta (LRM-771). */
export type WakeFailureReasonCode =
  | "fleet_member_not_found"
  | "fleet_member_pending_review"
  | "fleet_member_archived"
  | "fleet_member_not_active"
  | "agent_archived"
  | "agent_no_runtime"
  | "agent_model_required"
  | "runtime_offline"
  | "wake_internal_error"
  | "unknown";

export type SessionInterrupt = {
  messageId: string;
  reason: WakeFailureReasonCode;
  /** Sanitized one-line product copy — never a stack dump. */
  headline: string;
  recoveryHint: string | null;
  createdAt: string;
};

const KNOWN_REASONS = new Set<string>([
  "fleet_member_not_found",
  "fleet_member_pending_review",
  "fleet_member_archived",
  "fleet_member_not_active",
  "agent_archived",
  "agent_no_runtime",
  "agent_model_required",
  "runtime_offline",
  "wake_internal_error",
]);

function metaRecord(meta: unknown): Record<string, unknown> | null {
  if (!meta || typeof meta !== "object") return null;
  return meta as Record<string, unknown>;
}

function metaString(meta: unknown, key: string): string | null {
  const value = metaRecord(meta)?.[key];
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function isProcessMessage(message: ResearchMessage): boolean {
  return message.card_kind === "process";
}

function processOp(message: ResearchMessage): string | null {
  return metaString(message.meta, "op");
}

/**
 * Strip stack frames / raw exception dumps so the banner never shows
 * technical noise (AC: 可读原因，非技术堆栈).
 */
export function sanitizeWakeHeadline(body: string, max = 96): string {
  const withoutStack = body
    .replace(/\r\n/g, "\n")
    .split("\n")
    .filter((line) => {
      const t = line.trim();
      if (!t) return false;
      if (/^\s*at\s+\S+/i.test(t)) return false;
      if (/^\s*\/[\w./-]+:\d+/i.test(t)) return false;
      if (/stack trace/i.test(t)) return false;
      return true;
    })
    .join(" ")
    .replace(/\s+/g, " ")
    .trim();
  return shortProcessLine(withoutStack || body, max);
}

export function normalizeWakeReason(raw: string | null | undefined): WakeFailureReasonCode {
  if (!raw) return "unknown";
  const key = raw.trim().toLowerCase();
  if (KNOWN_REASONS.has(key)) return key as WakeFailureReasonCode;
  if (key.includes("offline") || key.includes("daemon") || key.includes("disconnect")) {
    return "runtime_offline";
  }
  if (key.includes("model")) return "agent_model_required";
  if (key.includes("runtime") && key.includes("no")) return "agent_no_runtime";
  if (key.includes("archived")) return "agent_archived";
  if (key.includes("pending")) return "fleet_member_pending_review";
  if (key.includes("not") && key.includes("member")) return "fleet_member_not_found";
  return "unknown";
}

function chronologically(a: ResearchMessage, b: ResearchMessage): number {
  const ta = Date.parse(a.created_at) || 0;
  const tb = Date.parse(b.created_at) || 0;
  if (ta !== tb) return ta - tb;
  return a.id.localeCompare(b.id);
}

/**
 * Active session interrupt from wake_failed / disconnect.
 * Banner shows only when the latest process event is still a wake_failed —
 * historical failures that the fleet already moved past do not surface.
 */
export function resolveSessionInterrupt(
  messages: ResearchMessage[],
): SessionInterrupt | null {
  const processMsgs = messages.filter(isProcessMessage).slice().sort(chronologically);
  if (processMsgs.length === 0) return null;

  const latest = processMsgs[processMsgs.length - 1]!;
  if (processOp(latest) !== "wake_failed") return null;

  const reason = normalizeWakeReason(metaString(latest.meta, "reason"));
  const hint = metaString(latest.meta, "recovery_hint");
  const title = metaString(latest.meta, "title");
  const rawBody = (latest.body || title || "").trim();
  const headline = sanitizeWakeHeadline(rawBody || "wake failed");

  return {
    messageId: latest.id,
    reason,
    headline,
    recoveryHint: hint,
    createdAt: latest.created_at,
  };
}

/**
 * True when the active interrupt tip is a different wake_failed than the one
 * the user retried (a newer failure after the retry request).
 */
export function isPostRetryWakeFailure(
  messages: ResearchMessage[],
  priorMessageId: string | null,
): boolean {
  if (!priorMessageId) return false;
  const tip = resolveSessionInterrupt(messages);
  return Boolean(tip && tip.messageId !== priorMessageId);
}
