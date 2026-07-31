import type { ChannelMessage } from "@multica/core/types";

/**
 * LRM-873 / LRM-870 — spoken replies only. System events (issue created,
 * status moved, …) must not inflate reply counts or occupy preview slots.
 */
export function isSpokenChannelMessageType(
  type: string | null | undefined,
): type is "user" | "agent" {
  return type === "user" || type === "agent";
}

export function isSpokenChannelMessage(
  message: Pick<ChannelMessage, "type">,
): boolean {
  return isSpokenChannelMessageType(message.type);
}

/** Filter + keep chronological order for preview / count. */
export function filterSpokenChannelMessages<T extends Pick<ChannelMessage, "type">>(
  messages: T[],
): T[] {
  return messages.filter(isSpokenChannelMessage);
}

/**
 * One-line preview text: plain content, or placeholders for sticker / image /
 * attachment-only payloads (design: 「[图片]」「[表情]」).
 */
export function spokenMessagePreviewText(
  message: Pick<ChannelMessage, "content" | "parts" | "attachments">,
  labels: { image: string; sticker: string; attachment: string },
): string {
  const text = (message.content ?? "").replace(/\s+/g, " ").trim();
  if (text) return text;

  const parts = message.parts ?? [];
  for (const part of parts) {
    if (!part || typeof part !== "object") continue;
    const kind = (part as { type?: string }).type;
    if (kind === "sticker") return labels.sticker;
    if (kind === "image" || kind === "media") return labels.image;
  }
  const first = message.attachments?.[0];
  if (first) {
    const ct = (first.content_type ?? "").toLowerCase();
    if (ct.startsWith("image/")) return labels.image;
    return labels.attachment;
  }
  return "";
}
