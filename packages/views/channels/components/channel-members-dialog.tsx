"use client";

import type { ChannelMember } from "@multica/core/types";
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
import {
  ChannelMembersList,
  type MemberRoleLabel,
} from "./channel-members-list";

/**
 * LRM-211 — Slack-style centered Members dialog (~520).
 * Opened from the header avatar stack / member count.
 */
export function ChannelMembersDialog({
  open,
  onOpenChange,
  channelName,
  memberCount,
  agentCount,
  members,
  loading,
  query,
  onQueryChange,
  roleForMember,
  canManage,
  isMobile,
  currentUserId,
  onAddPeople,
  onOpenDm,
  onRemove,
  dmPending,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channelName: string;
  memberCount: number;
  agentCount: number;
  members: ChannelMember[];
  loading?: boolean;
  query: string;
  onQueryChange: (q: string) => void;
  roleForMember: (member: ChannelMember) => MemberRoleLabel;
  canManage: boolean;
  isMobile: boolean;
  currentUserId: string;
  onAddPeople?: () => void;
  onOpenDm?: (member: ChannelMember) => void;
  onRemove?: (member: ChannelMember) => void;
  dmPending?: boolean;
}) {
  const { t } = useT("channels");
  const total = memberCount + agentCount;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="flex max-h-[min(85dvh,640px)] w-full flex-col gap-0 overflow-hidden p-0 sm:max-w-[520px]"
        showCloseButton
      >
        <DialogHeader className="gap-1 px-5 pb-2 pt-5 text-left">
          <DialogTitle className="text-lg font-bold tracking-tight">
            {t(($) => $.members.dialog_title)}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.members.dialog_subtitle, {
              name: channelName,
              members: memberCount,
              agents: agentCount,
            })}
          </DialogDescription>
        </DialogHeader>

        <div className="flex items-center gap-2.5 px-5 pb-3">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder={t(($) => $.members.find_members)}
              className="h-10 bg-muted/40 pl-9"
            />
          </div>
          {canManage && onAddPeople && (
            <Button
              type="button"
              className="h-9 shrink-0 bg-[#1264a3] px-3.5 text-sm font-semibold text-white hover:bg-[#1264a3]/90"
              onClick={onAddPeople}
            >
              {t(($) => $.members.add_people)}
            </Button>
          )}
        </div>

        <p className="px-5 pb-1 text-[11px] font-bold uppercase tracking-wide text-muted-foreground">
          {t(($) => $.members.in_channel, { count: total })}
        </p>

        <ChannelMembersList
          members={members}
          loading={loading}
          emptyLabel={
            query.trim()
              ? t(($) => $.members.no_results)
              : t(($) => $.members.empty)
          }
          noResultsLabel={t(($) => $.members.no_results)}
          roleForMember={roleForMember}
          canRemove={canManage}
          isMobile={isMobile}
          currentUserId={currentUserId}
          onOpenDm={onOpenDm}
          onRemove={onRemove}
          dmPending={dmPending}
        />

        <DialogFooter className="mx-0 mb-0 mt-0 flex-row items-center justify-between gap-2 rounded-none border-t bg-muted/30 px-5 py-3 sm:justify-between">
          <span className="text-xs text-muted-foreground">
            {t(($) => $.members.footer_count, { count: total })}
          </span>
          <Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
            {t(($) => $.members.done)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
