"use client";

import type { ActorIdentityPresentation } from "@multica/core/identity";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { Search, X } from "lucide-react";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { avatarGlyph, avatarToneClass } from "../../common/initials";
import { useT } from "../../i18n";

export type InviteCandidate = {
  key: string;
  type: "user" | "agent";
  id: string;
  avatarUrl?: string | null;
  presentation: ActorIdentityPresentation;
};

/**
 * LRM-211 — Slack-style Add people dialog (chips + checklist + Cancel/Add).
 * Opened from header Invite or Members dialog 「Add people」.
 */
export function ChannelAddPeopleDialog({
  open,
  onOpenChange,
  channelName,
  candidates,
  allCandidates,
  loading,
  query,
  onQueryChange,
  selected,
  onToggle,
  onClearOne,
  onSubmit,
  submitting,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channelName: string;
  /** Filtered list shown under Suggestions. */
  candidates: InviteCandidate[];
  /** Unfiltered pool used to render selected chips. */
  allCandidates: InviteCandidate[];
  loading?: boolean;
  query: string;
  onQueryChange: (q: string) => void;
  selected: Set<string>;
  onToggle: (key: string) => void;
  onClearOne: (key: string) => void;
  onSubmit: () => void;
  submitting?: boolean;
}) {
  const { t } = useT("channels");
  const chipItems = allCandidates.filter((c) => selected.has(c.key));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="flex max-h-[min(85dvh,640px)] w-full flex-col gap-0 overflow-hidden p-0 sm:max-w-[520px]"
        showCloseButton
      >
        <DialogHeader className="gap-1 px-5 pb-2 pt-5 text-left">
          <DialogTitle className="text-lg font-bold tracking-tight">
            {t(($) => $.members.add_people_title, { name: channelName })}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.members.add_people_subtitle)}
          </DialogDescription>
        </DialogHeader>

        <div className="px-5 pb-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder={t(($) => $.members.search)}
              className="h-10 bg-muted/40 pl-9"
            />
          </div>
        </div>

        {chipItems.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-5 pb-2.5">
            {chipItems.map((c) => (
              <span
                key={c.key}
                className="inline-flex h-7 items-center gap-1.5 rounded-full bg-[#e8f5fa] py-0 pl-1 pr-2 text-xs font-semibold text-[#1264a3]"
              >
                <ActorAvatar
                  name={c.presentation.displayName}
                  initials={avatarGlyph(c.presentation.displayName || "?")}
                  avatarUrl={resolvePublicFileUrl(c.avatarUrl)}
                  isAgent={c.type === "agent"}
                  size={20}
                  className={avatarToneClass(c.key)}
                />
                <span className="max-w-[7rem] truncate">{c.presentation.displayName}</span>
                <button
                  type="button"
                  onClick={() => onClearOne(c.key)}
                  aria-label={t(($) => $.members.remove_aria)}
                  className="rounded p-0.5 hover:bg-[#1264a3]/15"
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
          </div>
        )}

        <p className="px-5 pb-1 text-[11px] font-bold uppercase tracking-wide text-muted-foreground">
          {t(($) => $.members.suggestions)}
        </p>

        <div className="max-h-[min(280px,40vh)] overflow-y-auto pb-2">
          {loading ? (
            <div className="space-y-2 px-5 py-3" aria-busy="true">
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="flex items-center gap-3">
                  <Skeleton className="size-4 rounded" />
                  <Skeleton className="size-9 shrink-0 rounded-full" />
                  <div className="min-w-0 flex-1 space-y-1.5">
                    <Skeleton className="h-3.5 w-28" />
                    <Skeleton className="h-3 w-20" />
                  </div>
                </div>
              ))}
            </div>
          ) : candidates.length === 0 ? (
            <p className="px-5 py-10 text-center text-sm text-muted-foreground">
              {query.trim()
                ? t(($) => $.members.no_results)
                : t(($) => $.members.no_candidates)}
            </p>
          ) : (
            candidates.map((c) => (
              <label
                key={c.key}
                className={cn(
                  "flex min-h-[52px] cursor-pointer items-center gap-3 px-5 py-2.5 hover:bg-accent/60",
                  selected.has(c.key) && "bg-accent/40",
                )}
              >
                <Checkbox
                  checked={selected.has(c.key)}
                  onCheckedChange={() => onToggle(c.key)}
                />
                <ActorAvatar
                  name={c.presentation.displayName}
                  initials={avatarGlyph(c.presentation.displayName || "?")}
                  avatarUrl={resolvePublicFileUrl(c.avatarUrl)}
                  isAgent={c.type === "agent"}
                  size={36}
                  className={avatarToneClass(c.key)}
                />
                <ActorIdentityRow
                  displayName={c.presentation.displayName}
                  handle={
                    c.type === "agent"
                      ? `${c.presentation.handle} · ${t(($) => $.profile_popover.role.agent)}`
                      : c.presentation.handle
                  }
                  showHandle
                  className="min-w-0 flex-1"
                  primaryClassName="truncate text-sm font-semibold"
                />
              </label>
            ))
          )}
        </div>

        <DialogFooter className="mx-0 mb-0 mt-0 gap-2 rounded-none border-t bg-muted/30 px-5 py-3 sm:justify-end">
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {t(($) => $.members.cancel)}
          </Button>
          <Button
            type="button"
            className="bg-[#007a5a] font-bold text-white hover:bg-[#007a5a]/90"
            disabled={selected.size === 0 || submitting}
            onClick={onSubmit}
          >
            {t(($) => $.members.add)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
