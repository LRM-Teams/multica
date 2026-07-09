"use client";

import { useMemo, useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useT } from "../../i18n/use-t";
import {
  isConversationMuted,
  sumUnmutedUnreadCounts,
} from "./conversation-muted";
import type { PinnedConversationEntry } from "./pinned-conversations";

/**
 * Slack-style unified PINNED section at the top of the Messages sidebar.
 * Owns collapse + aggregate unread only; callers render each entry row.
 */
export function PinnedConversationsSection({
  entries,
  children,
}: {
  entries: PinnedConversationEntry[];
  children: ReactNode;
}) {
  const { t } = useT("channels");
  const [collapsed, setCollapsed] = useState(false);

  const aggregateUnread = useMemo(() => {
    let total = 0;
    total += sumUnmutedUnreadCounts(
      entries.flatMap((e) => (e.kind === "dm" ? [e.dm] : [])),
      (dm) => dm.real_unread ?? dm.unread ?? 0,
      (dm) => isConversationMuted(dm),
    );
    total += sumUnmutedUnreadCounts(
      entries.flatMap((e) => (e.kind === "channel" ? [e.channel] : [])),
      (c) => c.real_unread_count ?? c.unread_count ?? 0,
      (c) => isConversationMuted(c),
    );
    return total;
  }, [entries]);

  if (entries.length === 0) return null;

  return (
    <div className="pb-1" data-section="pinned">
      <div className="flex items-center gap-0.5 px-2 py-1.5">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex flex-1 items-center gap-1 text-xs font-medium uppercase tracking-wide text-muted-foreground transition-colors hover:text-foreground"
          aria-expanded={!collapsed}
        >
          {collapsed ? (
            <ChevronRight className="size-3.5 shrink-0" />
          ) : (
            <ChevronDown className="size-3.5 shrink-0" />
          )}
          <span className="flex-1 text-left">{t(($) => $.sidebar.pinned_section)}</span>
          {collapsed && aggregateUnread > 0 && (
            <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
              {aggregateUnread > 99 ? "99+" : aggregateUnread}
            </span>
          )}
        </button>
      </div>
      {!collapsed && children}
    </div>
  );
}
