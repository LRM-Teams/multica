"use client";

import { useEffect, useRef, useState } from "react";
import type { ActorIdentityPresentation } from "@multica/core/identity";
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
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { useT } from "../../i18n";

export type InviteCandidate = {
  key: string;
  type: "user" | "agent";
  id: string;
  avatarUrl?: string | null;
  presentation: ActorIdentityPresentation;
};

/** Soft hint after first-paint SLA; hard timeout UI after this (LRM-621). */
const INVITE_SLOW_MS = 2_000;
const INVITE_TIMEOUT_MS = 8_000;

/**
 * LRM-211 — Slack-style Add people dialog (chips + checklist + Cancel/Add).
 * Opened from header Invite or Members dialog 「Add people」.
 * LRM-225 — mobile bottom sheet + flex-1 scrollable suggestions list.
 * LRM-623 — first-screen skeleton / explicit error·timeout (no silent empty).
 */
export function ChannelAddPeopleDialog({
  open,
  onOpenChange,
  channelName,
  candidates,
  allCandidates,
  loading,
  error,
  onRetry,
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
  /** Fetch failed — never treat as empty list (LRM-238). */
  error?: boolean;
  onRetry?: () => void;
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
  const loadKey = open && loading && !error ? "active" : "inactive";
  const prevLoadKeyRef = useRef(loadKey);
  const [loadSlow, setLoadSlow] = useState(false);
  const [loadTimedOut, setLoadTimedOut] = useState(false);

  if (loadKey !== prevLoadKeyRef.current) {
    prevLoadKeyRef.current = loadKey;
    setLoadSlow(false);
    setLoadTimedOut(false);
  }

  useEffect(() => {
    if (loadKey !== "active") return;
    const slowTimer = window.setTimeout(() => setLoadSlow(true), INVITE_SLOW_MS);
    const timeoutTimer = window.setTimeout(() => setLoadTimedOut(true), INVITE_TIMEOUT_MS);
    return () => {
      window.clearTimeout(slowTimer);
      window.clearTimeout(timeoutTimer);
    };
  }, [loadKey]);

  const showTimeout = Boolean(loading && loadTimedOut && !error);
  const showError = Boolean(error) || showTimeout;
  const showSkeleton = Boolean(loading && !showError);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn(
          "flex w-full flex-col gap-0 overflow-hidden bg-card p-0 sm:max-w-[520px]",
          "h-[min(85dvh,640px)] max-h-[min(85dvh,640px)]",
          "max-sm:top-auto max-sm:bottom-0 max-sm:left-1/2 max-sm:right-auto max-sm:max-w-[calc(100%-0px)] max-sm:w-full max-sm:translate-x-[-50%] max-sm:translate-y-0 max-sm:rounded-b-none max-sm:rounded-t-2xl",
          "max-sm:h-[min(90dvh,640px)] max-sm:max-h-[min(90dvh,640px)]",
        )}
        showCloseButton
      >
        <DialogHeader className="shrink-0 gap-1 px-5 pb-2 pt-5 text-left">
          <DialogTitle className="text-lg font-bold tracking-tight">
            {t(($) => $.members.add_people_title, { name: channelName })}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.members.add_people_subtitle)}
          </DialogDescription>
        </DialogHeader>

        <div className="shrink-0 px-5 pb-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder={t(($) => $.members.search)}
              className="h-10 rounded-lg border-input bg-muted/40 pl-9"
              disabled={showError}
            />
          </div>
        </div>

        {chipItems.length > 0 && (
          <div className="flex shrink-0 flex-wrap gap-1.5 px-5 pb-2.5">
            {chipItems.map((c) => (
              <span
                key={c.key}
                className="inline-flex h-7 items-center gap-1.5 rounded-full bg-brand-soft py-0 pl-1 pr-2 text-xs font-semibold text-brand"
              >
                <ActorAvatar
                  actorType={c.type === "agent" ? "agent" : "member"}
                  actorId={c.id}
                  size={20}
                  avatarUrlHint={c.avatarUrl}
                  profileLink={false}
                />
                <span className="max-w-[7rem] truncate">{c.presentation.displayName}</span>
                <button
                  type="button"
                  onClick={() => onClearOne(c.key)}
                  aria-label={t(($) => $.members.remove_aria)}
                  className="rounded p-0.5 hover:bg-brand/15"
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
          </div>
        )}

        <p className="shrink-0 px-5 pb-1 text-[11px] font-bold uppercase tracking-wide text-muted-foreground">
          {t(($) => $.members.suggestions)}
        </p>

        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain [-webkit-overflow-scrolling:touch] pb-2">
          {showError ? (
            <div
              className="mx-5 my-6 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-6 text-center text-sm text-destructive"
              data-testid="add-people-error"
              role="alert"
            >
              <p>
                {showTimeout
                  ? t(($) => $.members.candidates_timeout)
                  : t(($) => $.members.candidates_error)}
              </p>
              {onRetry && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="mt-3 h-8 border-destructive/30 text-destructive"
                  onClick={onRetry}
                >
                  {t(($) => $.members.candidates_retry)}
                </Button>
              )}
            </div>
          ) : showSkeleton ? (
            <div className="space-y-2 px-5 py-3" aria-busy="true" data-testid="add-people-loading">
              {Array.from({ length: 6 }).map((_, i) => (
                <div key={i} className="flex items-center gap-3">
                  <Skeleton className="size-4 rounded" />
                  <Skeleton className="size-9 shrink-0 rounded-full" />
                  <div className="min-w-0 flex-1 space-y-1.5">
                    <Skeleton className="h-3.5 w-28" />
                    <Skeleton className="h-3 w-20" />
                  </div>
                </div>
              ))}
              {loadSlow && (
                <p className="pt-2 text-center text-xs text-muted-foreground">
                  {t(($) => $.members.candidates_slow)}
                </p>
              )}
            </div>
          ) : candidates.length === 0 ? (
            <p
              className="px-5 py-10 text-center text-sm text-muted-foreground"
              data-testid="add-people-empty"
            >
              {query.trim()
                ? t(($) => $.members.no_results)
                : t(($) => $.members.no_candidates)}
            </p>
          ) : (
            candidates.map((c) => {
              const checkboxId = `invite-${c.key.replace(/[^a-zA-Z0-9_-]/g, "-")}`;
              return (
                <label
                  key={c.key}
                  htmlFor={checkboxId}
                  className={cn(
                    "flex min-h-[52px] cursor-pointer items-center gap-3 px-5 py-2.5 hover:bg-hover",
                    selected.has(c.key) && "bg-brand-soft/70",
                  )}
                >
                  <Checkbox
                    id={checkboxId}
                    checked={selected.has(c.key)}
                    onCheckedChange={() => onToggle(c.key)}
                  />
                  <ActorAvatar
                    actorType={c.type === "agent" ? "agent" : "member"}
                    actorId={c.id}
                    size={36}
                    avatarUrlHint={c.avatarUrl}
                    showStatusDot={c.type === "agent"}
                    profileLink={false}
                  />
                  <ActorIdentityRow
                    displayName={c.presentation.displayName}
                    handle={c.presentation.handle}
                    showHandle
                    className="min-w-0 flex-1"
                    primaryClassName="truncate text-sm font-semibold text-ink"
                  />
                </label>
              );
            })
          )}
        </div>

        <DialogFooter className="mx-0 mb-0 mt-0 shrink-0 gap-2 rounded-none border-t border-border bg-muted/30 px-5 py-3 sm:justify-end max-sm:pb-[max(0.75rem,env(safe-area-inset-bottom))]">
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {t(($) => $.members.cancel)}
          </Button>
          <Button
            type="button"
            className="rounded-lg bg-success font-bold text-white hover:bg-success/90"
            disabled={selected.size === 0 || submitting || showError || showSkeleton}
            onClick={onSubmit}
          >
            {t(($) => $.members.add)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
