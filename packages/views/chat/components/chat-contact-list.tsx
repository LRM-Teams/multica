"use client";

import { useMemo } from "react";
import type { Agent, ChatSession } from "@multica/core/types";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { cn } from "@multica/ui/lib/utils";
import { agentColor } from "../../common/agent-color";
import { initialsOf } from "../../common/initials";
import { useT, useTimeAgo } from "../../i18n";

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
 * talk happens in channels, never here. Rows are sorted most-recent first and
 * carry an unread dot so agent-initiated DMs surface immediately.
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
  const timeAgo = useTimeAgo();

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
            const displayName = agent.display_name || agent.name;
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
                    name={displayName}
                    initials={initialsOf(displayName)}
                    avatarUrl={agent.avatar_url}
                    isAgent
                    size={28}
                    tint={agentColor(agent.id)}
                  />
                  {hasUnread && (
                    <span className="absolute -right-0.5 -top-0.5 size-2.5 rounded-full bg-brand ring-2 ring-sidebar" />
                  )}
                </div>
                <div className="min-w-0 flex-1">
                  <p
                    className={cn(
                      "truncate text-sm",
                      hasUnread ? "font-semibold" : "font-normal",
                    )}
                  >
                    {displayName}
                  </p>
                  <span className="text-[11px] text-muted-foreground">
                    {timeAgo(updatedAt)}
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
