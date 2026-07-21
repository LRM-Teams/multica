"use client";

import { useMemo, useState } from "react";
import type { ActorIdentityPresentation } from "@multica/core/identity";
import { matchesActorIdentitySearch } from "@multica/core/identity";
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
import { cn } from "@multica/ui/lib/utils";
import { Search, X } from "lucide-react";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { avatarGlyph, avatarToneClass } from "../../common/initials";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { useT } from "../../i18n";

const identitySearchOptions = { extendedMatch: matchesPinyin };

export type AddPeopleCandidate = {
  key: string;
  type: "user" | "agent";
  id: string;
  avatarUrl?: string | null;
  presentation: ActorIdentityPresentation;
};

/**
 * LRM-211 — independent Add people modal. Opened from header Invite or
 * Members modal 「Add people」. Selected chips + checkbox list + Cancel/Add.
 */
export function ChannelAddPeopleDialog({
  open,
  onOpenChange,
  channelName,
  candidates,
  pending,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channelName: string;
  candidates: AddPeopleCandidate[];
  pending?: boolean;
  onSubmit: (keys: string[]) => void;
}) {
  const { t } = useT("channels");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const filtered = useMemo(() => {
    const q = query.trim();
    if (!q) return candidates;
    return candidates.filter((c) =>
      matchesActorIdentitySearch(
        c.presentation.displayName,
        c.presentation.handle,
        q,
        identitySearchOptions,
      ),
    );
  }, [candidates, query]);

  const selectedList = useMemo(
    () => candidates.filter((c) => selected.has(c.key)),
    [candidates, selected],
  );

  const reset = () => {
    setQuery("");
    setSelected(new Set());
  };

  const toggle = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset();
        onOpenChange(next);
      }}
    >
      <DialogContent
        showCloseButton
        className="flex max-h-[min(85dvh,640px)] w-full max-w-[calc(100%-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-[520px]"
      >
        <DialogHeader className="gap-1 px-5 pb-2 pt-5 text-left">
          <DialogTitle className="text-lg font-bold tracking-tight">
            {t(($) => $.members.add_people_to, { name: channelName })}
          </DialogTitle>
          <DialogDescription>{t(($) => $.members.add_people_hint)}</DialogDescription>
        </DialogHeader>

        <div className="px-5 pb-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t(($) => $.members.search)}
              className="h-10 bg-muted/40 pl-9"
            />
          </div>
        </div>

        {selectedList.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-5 pb-2">
            {selectedList.map((c) => (
              <button
                key={c.key}
                type="button"
                onClick={() => toggle(c.key)}
                className="inline-flex h-7 items-center gap-1.5 rounded-full bg-primary/10 px-2 text-xs font-semibold text-primary"
              >
                <ActorAvatar
                  name={c.presentation.displayName}
                  initials={avatarGlyph(c.presentation.displayName || "?")}
                  avatarUrl={resolvePublicFileUrl(c.avatarUrl)}
                  isAgent={c.type === "agent"}
                  size={20}
                  className={avatarToneClass(c.key)}
                />
                <span className="max-w-[8rem] truncate">{c.presentation.displayName}</span>
                <X className="size-3.5 shrink-0 opacity-70" />
              </button>
            ))}
          </div>
        )}

        <p className="px-5 pb-1 text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.members.suggestions)}
        </p>

        <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
          {filtered.length === 0 ? (
            <p className="px-3 py-8 text-center text-sm text-muted-foreground">
              {query.trim()
                ? t(($) => $.members.no_results)
                : t(($) => $.members.no_candidates)}
            </p>
          ) : (
            filtered.map((c) => (
              <button
                key={c.key}
                type="button"
                onClick={() => toggle(c.key)}
                aria-pressed={selected.has(c.key)}
                className={cn(
                  "flex min-h-[52px] w-full items-center gap-3 rounded-md px-2.5 py-2 text-left hover:bg-accent",
                  selected.has(c.key) && "bg-accent/60",
                )}
              >
                <Checkbox
                  checked={selected.has(c.key)}
                  tabIndex={-1}
                  aria-hidden
                  className="pointer-events-none"
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
                      ? `${c.presentation.handle ?? c.id} · ${t(($) => $.message.agent_badge)}`
                      : c.presentation.handle
                  }
                  showHandle
                  primaryClassName="truncate text-sm font-semibold"
                />
              </button>
            ))
          )}
        </div>

        <DialogFooter className="mx-0 mb-0 mt-auto gap-2 border-t bg-muted/40 sm:justify-end">
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
          >
            {t(($) => $.members.add_cancel)}
          </Button>
          <Button
            type="button"
            disabled={selected.size === 0 || pending}
            onClick={() => {
              onSubmit(Array.from(selected));
              reset();
            }}
          >
            {t(($) => $.members.add_confirm)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
