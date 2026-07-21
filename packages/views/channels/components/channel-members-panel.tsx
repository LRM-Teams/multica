"use client";

import { useMemo, useState } from "react";
import type { ChannelMember } from "@multica/core/types";
import {
  matchesActorIdentitySearch,
  resolveActorDisplayName,
} from "@multica/core/identity";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Search } from "lucide-react";
import { useT } from "../../i18n";
import { ChannelMembersList } from "./channel-members-list";

const identitySearchOptions = { extendedMatch: matchesPinyin };

/**
 * Embedded Members body for Channel details 「成员」Tab — same list as the
 * LRM-211 Members modal, without dialog chrome.
 */
export function ChannelMembersPanel({
  members,
  loading,
  canManage,
  isMobile,
  currentUserId,
  roleForMember,
  onAddPeople,
  onRemove,
  onMessage,
}: {
  members: ChannelMember[];
  loading?: boolean;
  canManage: boolean;
  isMobile: boolean;
  currentUserId?: string | null;
  roleForMember: (member: ChannelMember) => string | null;
  onAddPeople?: () => void;
  onRemove?: (member: ChannelMember) => void;
  onMessage?: (member: ChannelMember) => void;
}) {
  const { t } = useT("channels");
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    const q = query.trim();
    if (!q) return members;
    return members.filter((m) =>
      matchesActorIdentitySearch(
        resolveActorDisplayName(
          m,
          m.member_type === "agent"
            ? t(($) => $.message.agent_badge)
            : t(($) => $.members.title),
        ),
        m.name,
        q,
        identitySearchOptions,
      ),
    );
  }, [members, query, t]);

  return (
    <div className="flex min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-2.5">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t(($) => $.members.find_members)}
            className="h-9 pl-8"
          />
        </div>
        {canManage && onAddPeople ? (
          <Button type="button" size="sm" className="shrink-0" onClick={onAddPeople}>
            {t(($) => $.members.add_people)}
          </Button>
        ) : null}
      </div>
      <ChannelMembersList
        members={filtered}
        loading={loading}
        emptyLabel={
          members.length === 0
            ? t(($) => $.members.empty)
            : t(($) => $.members.no_results)
        }
        canManage={canManage}
        isMobile={isMobile}
        currentUserId={currentUserId}
        roleForMember={roleForMember}
        agentFallbackLabel={t(($) => $.message.agent_badge)}
        onRemove={onRemove}
        onMessage={onMessage}
        className="max-h-[min(60vh,420px)]"
      />
      <div className="border-t px-4 py-2.5 text-xs text-muted-foreground">
        {t(($) => $.members.count_footer, { count: members.length })}
      </div>
    </div>
  );
}
