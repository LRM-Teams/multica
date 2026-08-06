import type {
  ResearchClarificationField,
  ResearchClarificationLayout,
  ResearchClarificationOption,
  ResearchClarificationQuestion,
  ResearchMessage,
} from "@multica/core/types";

export const CLARIFICATION_OP = "clarification_question";

/** Prefix markers embedded in user reply bodies (machine-parseable). */
export const CLARIFICATION_ANSWER_PREFIX = "澄清回答";
export const CLARIFICATION_SKIP_PREFIX = "跳过澄清";

export type ClarificationResolution =
  | { status: "pending" }
  | {
      status: "answered";
      replyMessageId: string;
      optionId?: string;
      optionLabel?: string;
      formValues?: Record<string, string>;
    }
  | { status: "skipped"; replyMessageId: string };

function metaRecord(meta: unknown): Record<string, unknown> | null {
  if (!meta || typeof meta !== "object") return null;
  return meta as Record<string, unknown>;
}

function metaString(meta: unknown, key: string): string | null {
  const value = metaRecord(meta)?.[key];
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function metaBool(meta: unknown, key: string, fallback: boolean): boolean {
  const value = metaRecord(meta)?.[key];
  if (typeof value === "boolean") return value;
  return fallback;
}

function parseOptions(raw: unknown): ResearchClarificationOption[] {
  if (!Array.isArray(raw)) return [];
  const out: ResearchClarificationOption[] = [];
  for (const item of raw) {
    if (!item || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;
    const id = typeof rec.id === "string" ? rec.id.trim() : "";
    const label = typeof rec.label === "string" ? rec.label.trim() : "";
    if (!id || !label) continue;
    const description =
      typeof rec.description === "string" && rec.description.trim()
        ? rec.description.trim()
        : undefined;
    out.push({ id, label, description });
  }
  return out;
}

function parseFields(raw: unknown): ResearchClarificationField[] {
  if (!Array.isArray(raw)) return [];
  const out: ResearchClarificationField[] = [];
  for (const item of raw) {
    if (!item || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;
    const id = typeof rec.id === "string" ? rec.id.trim() : "";
    const label = typeof rec.label === "string" ? rec.label.trim() : "";
    if (!id || !label) continue;
    const type = rec.type === "textarea" ? "textarea" : "text";
    out.push({
      id,
      label,
      type,
      required: rec.required === true,
      placeholder:
        typeof rec.placeholder === "string" && rec.placeholder.trim()
          ? rec.placeholder.trim()
          : undefined,
    });
  }
  return out;
}

function normalizeLayout(
  raw: string | null,
  options: ResearchClarificationOption[],
  fields: ResearchClarificationField[],
): ResearchClarificationLayout | null {
  if (raw === "form") return fields.length > 0 ? "form" : null;
  if (raw === "binary") return options.length >= 2 ? "binary" : null;
  if (raw === "list") return options.length >= 1 ? "list" : null;
  // Infer when layout omitted.
  if (fields.length > 0) return "form";
  if (options.length === 2) return "binary";
  if (options.length >= 1) return "list";
  return null;
}

/**
 * Parse a research message into a clarification question when meta.op matches.
 * Accepts process or chat cards (agent/system).
 */
export function parseClarificationQuestion(
  message: ResearchMessage,
): ResearchClarificationQuestion | null {
  const op = metaString(message.meta, "op");
  if (op !== CLARIFICATION_OP) return null;

  const options = parseOptions(metaRecord(message.meta)?.options);
  const fields = parseFields(metaRecord(message.meta)?.fields);
  const layout = normalizeLayout(metaString(message.meta, "layout"), options, fields);
  if (!layout) return null;

  const questionId =
    metaString(message.meta, "question_id") ||
    metaString(message.meta, "choice_id") ||
    message.id;
  const prompt =
    metaString(message.meta, "prompt") ||
    (typeof message.body === "string" ? message.body.trim() : "") ||
    "";

  return {
    question_id: questionId,
    prompt,
    layout,
    options: layout === "form" ? [] : options,
    fields: layout === "form" ? fields : [],
    allow_skip: metaBool(message.meta, "allow_skip", true),
    message_id: message.id,
    created_at: message.created_at,
  };
}

function qidToken(questionId: string): string {
  return `[qid=${questionId}]`;
}

/** Format option pick as a user message that wakes the lead. */
export function formatClarificationOptionReply(
  question: ResearchClarificationQuestion,
  option: ResearchClarificationOption,
): string {
  const promptBit = question.prompt ? `「${question.prompt}」` : "";
  return `${CLARIFICATION_ANSWER_PREFIX} ${qidToken(question.question_id)}：${promptBit} → ${option.label}`;
}

/** Format short form values as a user message. */
export function formatClarificationFormReply(
  question: ResearchClarificationQuestion,
  values: Record<string, string>,
): string {
  const lines = question.fields
    .map((field) => {
      const value = (values[field.id] ?? "").trim();
      return value ? `${field.label}: ${value}` : null;
    })
    .filter((line): line is string => Boolean(line));
  const promptBit = question.prompt ? `「${question.prompt}」` : "";
  const head = `${CLARIFICATION_ANSWER_PREFIX} ${qidToken(question.question_id)}：${promptBit}`;
  return lines.length > 0 ? `${head}\n${lines.join("\n")}` : head;
}

/** Format skip — must not block session; still posts so the fleet can continue. */
export function formatClarificationSkipReply(
  question: ResearchClarificationQuestion,
): string {
  const promptBit = question.prompt ? `「${question.prompt}」` : "";
  return `${CLARIFICATION_SKIP_PREFIX} ${qidToken(question.question_id)}：${promptBit}`.trim();
}

function chronologically(a: ResearchMessage, b: ResearchMessage): number {
  const ta = Date.parse(a.created_at) || 0;
  const tb = Date.parse(b.created_at) || 0;
  if (ta !== tb) return ta - tb;
  return a.id.localeCompare(b.id);
}

/**
 * Resolve whether a clarification was answered/skipped from subsequent user messages.
 * Meta updates are not required — FE matches qid tokens in user bodies.
 */
export function resolveClarificationResolution(
  question: ResearchClarificationQuestion,
  messages: ResearchMessage[],
): ClarificationResolution {
  const token = qidToken(question.question_id);
  const askTime = Date.parse(question.created_at) || 0;
  const later = messages
    .filter((m) => m.sender_type === "user")
    .filter((m) => {
      const t = Date.parse(m.created_at) || 0;
      // Same-timestamp replies after the ask message still count (id order).
      return t > askTime || (t === askTime && m.id.localeCompare(question.message_id) > 0);
    })
    .filter((m) => typeof m.body === "string" && m.body.includes(token))
    .slice()
    .sort(chronologically);

  for (const reply of later) {
    const body = reply.body.trim();
    if (body.startsWith(CLARIFICATION_SKIP_PREFIX) || body.includes(CLARIFICATION_SKIP_PREFIX)) {
      return { status: "skipped", replyMessageId: reply.id };
    }
    if (body.startsWith(CLARIFICATION_ANSWER_PREFIX) || body.includes(CLARIFICATION_ANSWER_PREFIX)) {
      const matchedOption = question.options.find(
        (opt) => body.includes(`→ ${opt.label}`) || body.includes(opt.label),
      );
      return {
        status: "answered",
        replyMessageId: reply.id,
        optionId: matchedOption?.id,
        optionLabel: matchedOption?.label,
      };
    }
  }

  // Also honor meta flags if BE later stamps the ask card.
  const meta = metaRecord(
    messages.find((m) => m.id === question.message_id)?.meta,
  );
  if (meta?.skipped === true) {
    return { status: "skipped", replyMessageId: question.message_id };
  }
  if (typeof meta?.selected_option_id === "string" && meta.selected_option_id) {
    const opt = question.options.find((o) => o.id === meta.selected_option_id);
    return {
      status: "answered",
      replyMessageId: question.message_id,
      optionId: opt?.id,
      optionLabel: opt?.label,
    };
  }

  return { status: "pending" };
}
