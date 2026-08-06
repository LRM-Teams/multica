"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { inboxListOptions, deduplicateInboxItems } from "@multica/core/inbox/queries";
import { useMarkInboxRead } from "@multica/core/inbox/mutations";
import { issueListOptions } from "@multica/core/issues/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import type { InboxItem } from "@multica/core/types";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { AppLink } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";

const MAX_ITEMS = 20;

// Inbox types surfaced in the "tasks & messages for me" panel — assignments,
// mentions, comments. Pending-approval items come from in_review issues below
// (kept consistent with the "Pending Approval" KPI), not from inbox.
const RELEVANT_TYPES = new Set<InboxItem["type"]>([
  "issue_assigned",
  "assignee_changed",
  "mentioned",
  "review_requested",
  "new_comment",
]);

type Priority = "high" | "medium" | "low" | "mention";

function priorityFor(item: InboxItem): Priority {
  if (item.type === "mentioned") return "mention";
  switch (item.severity) {
    case "action_required":
      return "high";
    case "attention":
      return "medium";
    default:
      return "low";
  }
}

// One row in the merged list: an approval issue, an inbox/issue item, or a
// channel mention. `href` is the navigation target; `inboxId` is set only for
// channel mentions, which are marked read on click so the prompt disappears.
type Row = {
  id: string;
  title: string;
  ts: string;
  badge: "approval" | Priority;
  href?: string;
  inboxId?: string;
};

export function MyTasksPanel({ wsId }: { wsId: string }) {
  const { t } = useT("overview");
  const timeAgo = useTimeAgo();
  const p = useWorkspacePaths();
  const markRead = useMarkInboxRead();

  const { data: inbox = [], isPending: inboxPending } = useQuery({
    ...inboxListOptions(wsId),
    enabled: !!wsId,
  });
  const { data: issues = [], isPending: issuesPending } = useQuery({
    ...issueListOptions(wsId),
    enabled: !!wsId,
  });

  const rows = useMemo<Row[]>(() => {
    // Approval queue first — these mirror the "Pending Approval" KPI.
    const approvals: Row[] = issues
      .filter((i) => i.status === "in_review")
      .map((i) => ({
        id: i.id,
        title: i.identifier ? `${i.identifier} ${i.title}` : i.title,
        ts: i.updated_at ?? i.created_at,
        badge: "approval" as const,
        href: p.issueDetail(i.id),
      }));

    const inboxRows: Row[] = deduplicateInboxItems(inbox)
      .filter((i) => RELEVANT_TYPES.has(i.type))
      .map((i): Row | null => {
        const channelId = i.details?.channel_id;
        if (channelId) {
          // Channel mention: shown only until clicked. Click marks it read and
          // routes to the channel, scrolling to the exact message.
          if (i.read) return null;
          const messageId = i.details?.message_id;
          const href = `${p.channelDetail(channelId)}${
            messageId ? `?message=${messageId}` : ""
          }`;
          return {
            id: i.id,
            title: t(($) => $.my_tasks.channel_mention, {
              actor: i.details?.actor_name ?? "",
              channel: i.details?.channel_name ?? "",
            }),
            ts: i.created_at,
            badge: "mention",
            href,
            inboxId: i.id,
          };
        }
        return {
          id: i.id,
          title: i.title,
          ts: i.created_at,
          badge: priorityFor(i),
          href: i.issue_id ? p.issueDetail(i.issue_id) : undefined,
        };
      })
      .filter((r): r is Row => r !== null);

    return [...approvals, ...inboxRows].slice(0, MAX_ITEMS);
  }, [issues, inbox, p, t]);

  const isPending = inboxPending || issuesPending;

  return (
    <Card size="sm" className="h-full min-h-0">
      <CardHeader>
        <CardTitle>{t(($) => $.my_tasks.title)}</CardTitle>
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {isPending ? (
          Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="my-2 h-9 w-full" />)
        ) : rows.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">
            {t(($) => $.my_tasks.empty)}
          </p>
        ) : (
          rows.map((row) => {
            const badge =
              row.badge === "approval" ? (
                <Badge variant="outline" className="shrink-0 border-warning/40 text-warning">
                  {t(($) => $.my_tasks.review_badge)}
                </Badge>
              ) : (
                <Badge
                  variant={
                    row.badge === "high"
                      ? "destructive"
                      : row.badge === "low"
                        ? "secondary"
                        : row.badge === "mention"
                          ? "default"
                          : "outline"
                  }
                  className={cn("shrink-0", row.badge === "medium" && "border-warning/40 text-warning")}
                >
                  {t(($) => $.priority[row.badge as Priority])}
                </Badge>
              );

            const inner = (
              <>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm">{row.title}</p>
                  <span className="text-[11px] text-muted-foreground">{timeAgo(row.ts)}</span>
                </div>
                {badge}
              </>
            );

            const rowClass = "flex items-start gap-2 border-b py-2.5 last:border-b-0";

            return row.href ? (
              <AppLink
                key={row.id}
                href={row.href}
                onClick={row.inboxId ? () => markRead.mutate(row.inboxId!) : undefined}
                className={cn(rowClass, "-mx-3 rounded-md px-3 transition-colors hover:bg-accent")}
              >
                {inner}
              </AppLink>
            ) : (
              <div key={row.id} className={rowClass}>
                {inner}
              </div>
            );
          })
        )}
      </CardContent>
    </Card>
  );
}
