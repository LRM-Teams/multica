import type { Attachment, MessagePart } from "@multica/core/types";

export type AttachmentPart = Extract<MessagePart, { type: "attachment" }>;

export type ResolvedAttachmentItem =
  | { kind: "record"; attachmentId: string; attachment: Attachment }
  | { kind: "missing"; attachmentId: string };

/**
 * Collect attachment parts in message order and hydrate each via `attachments`
 * by id. Missing / unauthorized rows become placeholders — never leak part
 * filename hints for denied ids (PRD).
 */
export function resolveAttachmentZoneItems(
  parts: MessagePart[] | null | undefined,
  attachments: Attachment[] | null | undefined,
): ResolvedAttachmentItem[] {
  if (!parts?.length) return [];
  const byId = new Map((attachments ?? []).map((a) => [a.id, a]));
  const items: ResolvedAttachmentItem[] = [];
  for (const part of parts) {
    if (part.type !== "attachment") continue;
    const id = part.attachment_id?.trim();
    if (!id) continue;
    const record = byId.get(id);
    if (record) {
      items.push({ kind: "record", attachmentId: id, attachment: record });
    } else {
      items.push({ kind: "missing", attachmentId: id });
    }
  }
  return items;
}

export function collectAttachmentParts(
  parts: MessagePart[] | null | undefined,
): AttachmentPart[] {
  if (!parts?.length) return [];
  return parts.filter(
    (p): p is AttachmentPart => p.type === "attachment" && !!p.attachment_id?.trim(),
  );
}
