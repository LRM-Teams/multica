"use client";

import { useMemo, useState } from "react";
import type { ChannelMember } from "@multica/core/types";
import { matchesActorIdentitySearch, resolveActorDisplayName } from "@multica/core/identity";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Search } from "lucide-react";
import { useT } from "../../i18n";
import { ChannelMembersList } from "./channel-members-list";

const identitySearchOptions = { extendedMatch: matchesPinyin };

/**
 * LRM-211 — Slack-style centered Members modal (~520). Header avatar stack /
 * count opens this; channel details Members tab reuses ChannelMembersList.
 */
export function ChannelMembersDialog({
  open,
  onOpenChange,
  channelName,
  subtitle,
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
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channelName: string;
  subtitle: string;
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
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setQuery("");
        onOpenChange(next);
      }}
    >
      <DialogContent
        showCloseButton
        className="flex max-h-[min(85dvh,640px)] w-full max-w-[calc(100%-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-[520px]"
      >
        <DialogHeader className="gap-1 px-5 pb-2 pt-5 text-left">
          <DialogTitle className="text-lg font-bold tracking-tight">
            {t(($) => $.members.title)}
          </DialogTitle>
          <DialogDescription>
            #{channelName}
            {subtitle ? ` · ${subtitle}` : ""}
          </DialogDescription>
        </DialogHeader>

        <div className="flex items-center gap-2.5 px-5 pb-3">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t(($) => $.members.find_members)}
              className="h-10 bg-muted/40 pl-9"
            />
          </div>
          {canManage && onAddPeople ? (
            <Button
              type="button"
              className="h-9 shrink-0"
              onClick={onAddPeople}
            >
              {t(($) => $.members.add_people)}
            </Button>
          ) : null}
        </div>

        <p className="px-5 pb-1 text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.members.in_channel, { count: members.length })}
        </p>

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
          className="min-h-0 flex-1 px-2"
        />

        <DialogFooter className="mx-0 mb-0 mt-auto flex-row items-center justify-between gap-2 border-t bg-muted/40 sm:justify-between">
          <span className="text-xs text-muted-foreground">
            {t(($) => $.members.count_footer, { count: members.length })}
          </span>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.members.done)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
