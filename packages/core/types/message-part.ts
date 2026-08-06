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
      /** The visible text is a speech transcript and should be rendered with voice controls. */
      type: "voice";
      /** Present when the sender supplied a playable recording; Agent TTS output leaves it unset. */
      attachment_id?: string;
      filename?: string;
      content_type?: string;
      size_bytes?: number;
      /** Present for recorded human input; Agent TTS output may leave it unset. */
      duration_ms?: number;
      /** Server-owned ASR state for recorded human input. */
      transcription_status?: "pending" | "completed" | "failed";
      /** Server-owned TTS state for Agent voice output. */
      synthesis_status?: "pending" | "completed" | "failed";
    }
  | {
      type: "reference";
      ref_type: "mention";
      ref_subtype: "member" | "agent" | "squad";
      ref_id: string;
      label?: string;
      /** Exact UTF-16 range in the message content; emitted by the server. */
      content_start_utf16?: number;
      content_end_utf16?: number;
    }
  | {
      type: "reference";
      ref_type: "issue-ref";
      ref_subtype?: "issue";
      ref_id: string;
      label?: string;
      /** Exact UTF-16 range in the message content; emitted by the server. */
      content_start_utf16?: number;
      content_end_utf16?: number;
    }
  | {
      /** task #912: server-resolved counterpart to the composer's
       *  `[Label](mention://channel/<id>)` link (ChannelReferenceExtension). */
      type: "reference";
      ref_type: "channel-ref";
      ref_id: string;
      label?: string;
      /** Exact UTF-16 range in the message content; emitted by the server. */
      content_start_utf16?: number;
      content_end_utf16?: number;
    }
  | {
      /** Canonical Message-backed agent:create Proposal. */
      type: "reference";
      ref_type: "agent:create";
      ref_id: string;
      label?: string;
      content_start_utf16?: number;
      content_end_utf16?: number;
      params?: {
        name?: string;
        description?: string;
        preferred_computer?: string;
        status?: "prepared" | "executed";
        committer_user_id?: string;
        result_agent_id?: string;
      };
    }
  | {
      type: "system_event";
      event: string;
      event_params: Record<string, unknown>;
    }
  | {
      /** Agent-emitted interactive choice card (Multica-native; not vendor AskUserQuestion). */
      type: "choice";
      choice_id: string;
      prompt: string;
      layout: "binary" | "list";
      options: Array<{
        id: string;
        label: string;
        description?: string;
      }>;
      allow_dismiss?: boolean;
      expires_at?: string;
      /** Current pick after the human selects (v1: one reselect allowed). */
      selected_option_id?: string;
      /** 1 = first pick (reselect left), 2 = locked after reselect. */
      select_count?: number;
    }
  | {
      /** User-visible answer produced when a choice option is tapped. */
      type: "choice_reply";
      choice_id: string;
      option_id: string;
      label: string;
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
