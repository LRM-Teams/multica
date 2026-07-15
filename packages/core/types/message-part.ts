export type MessagePart =
  | {
      type: "text";
      text: string;
    }
  | {
      type: "sticker";
      pack_id?: string;
      sticker_id: string;
      alt?: string;
    }
  | {
      type: "attachment";
      attachment_id: string;
      filename?: string;
      content_type?: string;
      size_bytes?: number;
    }
  | {
      type: "reference";
      ref_type: "mention";
      ref_subtype: "member" | "agent" | "squad" | "all";
      ref_id: string;
      label?: string;
    }
  | {
      type: "reference";
      ref_type: "issue-ref";
      ref_subtype?: "issue";
      ref_id: string;
      label?: string;
      ref_title?: string;
      ref_status?: string;
    }
  | {
      type: "system_event";
      event: string;
      event_params: Record<string, unknown>;
    };

/**
 * Build structured channel message parts from plain text + attachment ids.
 * Used by channel/thread send so the wire format uses `parts` (attachment
 * truth) rather than a parallel `attachment_ids` field.
 */
export function buildChannelMessageParts(
  text: string,
  attachmentIds?: readonly string[],
): MessagePart[] {
  const parts: MessagePart[] = [];
  const trimmed = text.trim();
  if (trimmed) {
    parts.push({ type: "text", text: trimmed });
  }
  for (const id of attachmentIds ?? []) {
    if (!id) continue;
    parts.push({ type: "attachment", attachment_id: id });
  }
  return parts;
}
