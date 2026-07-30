"use client";

import { useMemo } from "react";
import type { Agent, ChatSession } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { cn } from "@multica/ui/lib/utils";
import { useT, Time } from "../../i18n";

interface Contact {
  agent: Agent;
  /** Most-recent session id for this agent — the thread we open on click. */
  sessionId: string;
  updatedAt: string;
  hasUnread: boolean;
}

/**
 * Left pane of the agent-DM panel: one contact row per agent the user has a
 * direct-message thread with. DMs are strictly 1:1 human ↔ agent; agent ↔ agent
 * talk happens in channels, never here. Rows are sorted most-recent first.
 *
 * Presence belongs on the avatar and uses the shared ActorAvatar projection,
 * matching the open-chat header. Unread is an inbox signal, so it lives beside
 * the conversation metadata rather than impersonating a presence dot.
 */
export function ChatContactList({
  sessions,
  agents,
  activeAgentId,
  onSelect,
}: {
  sessions: ChatSession[];
  agents: Agent[];
  activeAgentId: string | null;
  onSelect: (agentId: string, sessionId: string) => void;
}) {
  const { t } = useT("chat");

  const contacts = useMemo<Contact[]>(() => {
    const byAgent = new Map<string, Contact>();
    for (const s of sessions) {
      if (s.status === "archived") continue;
      const agent = agents.find((a) => a.id === s.agent_id);
      if (!agent || agent.archived_at) continue;
      const existing = byAgent.get(s.agent_id);
      const isNewer =
        !existing ||
        new Date(s.updated_at).getTime() > new Date(existing.updatedAt).getTime();
      byAgent.set(s.agent_id, {
        agent,
        sessionId: isNewer ? s.id : existing!.sessionId,
        updatedAt: isNewer ? s.updated_at : existing!.updatedAt,
        hasUnread: (existing?.hasUnread ?? false) || s.has_unread,
      });
    }
    return [...byAgent.values()].sort(
      (a, b) =>
        new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime(),
    );
  }, [sessions, agents]);

  return (
    <div className="flex h-full w-[208px] shrink-0 flex-col border-r bg-sidebar/60">
      <div className="border-b px-3 py-2.5 text-xs font-medium text-muted-foreground">
        {t(($) => $.contacts.title)}
      </div>
      {contacts.length === 0 ? (
        <p className="px-3 py-6 text-center text-xs text-muted-foreground">
          {t(($) => $.contacts.empty)}
        </p>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto py-1">
          {contacts.map(({ agent, sessionId, hasUnread, updatedAt }) => {
            const active = agent.id === activeAgentId;
            return (
              <button
                key={agent.id}
                type="button"
                onClick={() => onSelect(agent.id, sessionId)}
                className={cn(
                  "flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent",
                  active && "bg-accent",
                )}
              >
                <div className="relative shrink-0">
                  <ActorAvatar
                    actorType="agent"
                    actorId={agent.id}
                    size={28}
                    showStatusDot
                    profileLink={false}
                  />
                </div>
                <div className="min-w-0 flex-1">
                  <ActorIdentityRow
                    identity={agent}
                    primaryClassName={cn(
                      "truncate text-sm",
                      hasUnread ? "font-semibold" : "font-normal",
                    )}
                  />
                  <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                    <Time kind="list" value={updatedAt} />
                    {hasUnread && (
                      <span
                        aria-label={t(($) => $.window.unread)}
                        title={t(($) => $.window.unread)}
                        data-testid={`contact-unread-${agent.id}`}
                        className="size-2 shrink-0 rounded-full bg-brand"
                      />
                    )}
                  </span>
                </div>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
