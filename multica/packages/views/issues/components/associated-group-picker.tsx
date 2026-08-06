"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Hash, X } from "lucide-react";
import { channelsOptions } from "@multica/core/channels";
import { useSetIssueChannel } from "@multica/core/issues/mutations";
import { useWorkspaceId } from "@multica/core/hooks";
import type { IssueSourceChannelRef } from "@multica/core/types";
import { Popover, PopoverTrigger, PopoverContent } from "@multica/ui/components/ui/popover";
import { useT } from "../../i18n/use-t";

/**
 * Issue Properties "Associated group" field (#629). Reads the issue's canonical
 * `channel` (never derives from description/source text), and lets an editor
 * set / change / clear it (1:1, server-enforced). Changing an existing
 * association takes a light confirmation (backflow reroutes); clearing is an
 * explicit action. Only visible, unarchived group channels are offered.
 */
export function AssociatedGroupPicker({
  issueId,
  channel,
}: {
  issueId: string;
  channel: IssueSourceChannelRef | null;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  // A channel id awaiting the "change existing association" confirmation.
  const [pendingChange, setPendingChange] = useState<string | null>(null);
  const setChannel = useSetIssueChannel(issueId);
  const { data: channels = [] } = useQuery(channelsOptions(wsId));

  const emptyLabel = t(($) => $.detail.no_associated_group);
  // Full, untruncated value — used verbatim as the trigger's `title` so a
  // narrow value column can ellipsize without hiding the real value (#629 768px).
  const triggerLabel = channel ? channel.channel_name ?? channel.channel_id : emptyLabel;

  const groups = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return channels.filter(
      (c) =>
        c.kind === "group" &&
        !c.archived_at &&
        (!q || c.name.toLowerCase().includes(q)),
    );
  }, [channels, filter]);

  const reset = () => {
    setOpen(false);
    setFilter("");
    setPendingChange(null);
  };

  const commitSet = (channelId: string) => {
    setChannel.mutate(channelId);
    reset();
  };

  const onPick = (channelId: string) => {
    if (channelId === channel?.channel_id) {
      reset();
      return;
    }
    // Changing an EXISTING association needs a light confirm (backflow reroutes).
    if (channel) {
      setPendingChange(channelId);
      return;
    }
    commitSet(channelId);
  };

  const clear = () => {
    setChannel.mutate(null);
    reset();
  };

  return (
    <Popover open={open} onOpenChange={(v) => (v ? setOpen(true) : reset())}>
      <PopoverTrigger
        render={
          <button
            type="button"
            title={triggerLabel}
            className="inline-flex min-w-0 items-center gap-1.5 text-xs transition-colors hover:text-foreground"
          >
            {channel ? (
              <>
                <Hash className="size-3 shrink-0 text-muted-foreground" />
                <span className="min-w-0 truncate">{channel.channel_name ?? channel.channel_id}</span>
              </>
            ) : (
              <span className="min-w-0 truncate text-muted-foreground">{emptyLabel}</span>
            )}
          </button>
        }
      />
      <PopoverContent align="start" className="w-56 p-0">
        {pendingChange ? (
          <div className="p-2 text-xs">
            <p className="mb-2 text-muted-foreground">
              {t(($) => $.detail.associated_group_change_confirm)}
            </p>
            <div className="flex justify-end gap-1.5">
              <button
                type="button"
                className="rounded px-2 py-1 hover:bg-accent"
                onClick={() => setPendingChange(null)}
              >
                {t(($) => $.detail.associated_group_cancel)}
              </button>
              <button
                type="button"
                className="rounded bg-primary px-2 py-1 text-primary-foreground"
                onClick={() => commitSet(pendingChange)}
              >
                {t(($) => $.detail.associated_group_confirm)}
              </button>
            </div>
          </div>
        ) : (
          <>
            <div className="border-b px-2 py-1.5">
              <input
                type="text"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder={t(($) => $.detail.associated_group_search)}
                aria-label={t(($) => $.detail.associated_group_search)}
                className="w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
              />
            </div>
            <div className="max-h-60 overflow-y-auto p-1">
              {channel && (
                <button
                  type="button"
                  onClick={clear}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground hover:bg-accent"
                >
                  <X className="size-3.5 shrink-0" />
                  <span>{t(($) => $.detail.associated_group_clear)}</span>
                </button>
              )}
              {groups.map((c) => (
                <button
                  key={c.id}
                  type="button"
                  onClick={() => onPick(c.id)}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                >
                  <Hash className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 truncate">{c.name}</span>
                  {c.id === channel?.channel_id && <Check className="ml-auto size-3.5 shrink-0" />}
                </button>
              ))}
              {groups.length === 0 && (
                <div className="px-2 py-3 text-center text-sm text-muted-foreground">
                  {t(($) => $.detail.associated_group_no_results)}
                </div>
              )}
            </div>
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}
