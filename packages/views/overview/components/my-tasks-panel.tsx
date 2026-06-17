"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { inboxListOptions, deduplicateInboxItems } from "@multica/core/inbox/queries";
import { issueListOptions } from "@multica/core/issues/queries";
import type { InboxItem } from "@multica/core/types";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useT, useTimeAgo } from "../../i18n";

const MAX_ITEMS = 8;

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

// One row in the merged list: either a pending-approval issue or an inbox item.
type Row =
  | { kind: "approval"; id: string; title: string; ts: string }
  | { kind: "inbox"; id: string; title: string; ts: string; priority: Priority };

export function MyTasksPanel({ wsId }: { wsId: string }) {
  const { t } = useT("overview");
  const timeAgo = useTimeAgo();

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
        kind: "approval" as const,
        id: i.id,
        title: i.identifier ? `${i.identifier} ${i.title}` : i.title,
        ts: i.updated_at ?? i.created_at,
      }));

    const inboxRows: Row[] = deduplicateInboxItems(inbox)
      .filter((i) => RELEVANT_TYPES.has(i.type))
      .map((i) => ({
        kind: "inbox" as const,
        id: i.id,
        title: i.title,
        ts: i.created_at,
        priority: priorityFor(i),
      }));

    return [...approvals, ...inboxRows].slice(0, MAX_ITEMS);
  }, [issues, inbox]);

  const isPending = inboxPending || issuesPending;

  return (
    <Card size="sm" className="min-h-0">
      <CardHeader>
        <CardTitle>{t(($) => $.my_tasks.title)}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col">
        {isPending ? (
          Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="my-2 h-9 w-full" />)
        ) : rows.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">
            {t(($) => $.my_tasks.empty)}
          </p>
        ) : (
          rows.map((row) => (
            <div key={row.id} className="flex items-start gap-2 border-b py-2.5 last:border-b-0">
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm">{row.title}</p>
                <span className="text-[11px] text-muted-foreground">{timeAgo(row.ts)}</span>
              </div>
              {row.kind === "approval" ? (
                <Badge variant="outline" className="shrink-0 border-warning/40 text-warning">
                  {t(($) => $.my_tasks.review_badge)}
                </Badge>
              ) : (
                <Badge
                  variant={
                    row.priority === "high"
                      ? "destructive"
                      : row.priority === "low"
                        ? "secondary"
                        : row.priority === "mention"
                          ? "default"
                          : "outline"
                  }
                  className={cn("shrink-0", row.priority === "medium" && "border-warning/40 text-warning")}
                >
                  {t(($) => $.priority[row.priority])}
                </Badge>
              )}
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}
