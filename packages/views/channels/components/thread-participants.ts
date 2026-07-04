import type { ChannelMessage } from "@multica/core/types";

export type ThreadMemberType = "user" | "agent";
export type ThreadParticipantSource = "started" | "mentioned" | "replied" | "assignee";

export interface ThreadParticipant {
  /** Stable identity `${memberType}:${memberId}`. */
  key: string;
  memberType: ThreadMemberType;
  memberId: string;
  /** Stable handle when known (may equal displayName). */
  name: string;
  displayName: string;
  /** Every reason this actor is a participant, in first-seen order. */
  sources: ThreadParticipantSource[];
}

export interface ThreadAssignee {
  memberType: ThreadMemberType;
  memberId: string;
  name?: string;
  displayName?: string;
}

// Mentions are markdown links `[@Label](mention://member|agent/ID)` (mirrors
// message-preview's pattern). `member` maps to the `user` member type.
const MENTION_LINK_PATTERN = /\[@([^\]]+)\]\(mention:\/\/(member|agent)\/([^)]+)\)/g;

function normalizeMentionType(raw: string): ThreadMemberType {
  return raw === "agent" ? "agent" : "user";
}

/**
 * Derive the thread participant set — the union of who started the root, who
 * was @-mentioned anywhere in the thread, who replied, and (for issue threads)
 * the issue assignees. Pure so the derivation is unit-testable and the panel
 * stays presentational. Insertion order keeps the root author first.
 */
export function deriveThreadParticipants(
  root: ChannelMessage,
  replies: ChannelMessage[],
  options: { assignees?: ThreadAssignee[] } = {},
): ThreadParticipant[] {
  const byKey = new Map<string, ThreadParticipant>();

  const add = (
    memberType: ThreadMemberType,
    memberId: string,
    source: ThreadParticipantSource,
    labels: { name?: string; displayName?: string } = {},
  ) => {
    if (!memberId) return;
    const key = `${memberType}:${memberId}`;
    const existing = byKey.get(key);
    if (existing) {
      if (!existing.sources.includes(source)) existing.sources.push(source);
      if (labels.name && !existing.name) existing.name = labels.name;
      if (labels.displayName && (!existing.displayName || existing.displayName === existing.memberId)) {
        existing.displayName = labels.displayName;
      }
      return;
    }
    const displayName = labels.displayName || labels.name || memberId;
    byKey.set(key, {
      key,
      memberType,
      memberId,
      name: labels.name || displayName,
      displayName,
      sources: [source],
    });
  };

  const addAuthor = (message: ChannelMessage, source: ThreadParticipantSource) => {
    if ((message.type === "user" || message.type === "agent") && message.author_id) {
      add(message.type, message.author_id, source, { displayName: message.author_name });
    }
  };

  const addMentions = (content: string) => {
    for (const match of content.matchAll(MENTION_LINK_PATTERN)) {
      const label = match[1];
      const rawType = match[2];
      const id = match[3];
      if (!id || !rawType) continue;
      add(normalizeMentionType(rawType), id, "mentioned", { displayName: label });
    }
  };

  addAuthor(root, "started");
  addMentions(root.content);
  for (const reply of replies) {
    addAuthor(reply, "replied");
    addMentions(reply.content);
  }
  for (const assignee of options.assignees ?? []) {
    add(assignee.memberType, assignee.memberId, "assignee", {
      name: assignee.name,
      displayName: assignee.displayName,
    });
  }

  return [...byKey.values()];
}
