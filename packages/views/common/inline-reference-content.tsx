"use client";

import * as React from "react";
import type { Issue, IssuePriority, IssueStatus, MessagePart } from "@multica/core/types";
import { STATUS_CONFIG, PRIORITY_CONFIG } from "@multica/core/issues/config";
import { useActorName } from "@multica/core/workspace/hooks";
import { MemoizedMarkdown, ActorMention } from "./markdown";
import { AppLink } from "../navigation/app-link";
import { useWorkspacePaths } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";
import { mentionTokenClassName } from "./mention-token";
import { StatusIcon } from "../issues/components/status-icon";
import { PriorityIcon } from "../issues/components/priority-icon";
import { ProjectChip } from "../projects/components/project-chip";
import { useResolvedIssue } from "../issues/components/issue-chip";
import { projectInlineReferences, type ReferencePart } from "./inline-references";

/**
 * Renders a message body from `content` + structured `reference` parts as prose
 * with inline tokens — the shared consumer of {@link projectInlineReferences}.
 * One place turns bare `@Label` / `MUL-123` text into interactive tokens (with
 * the SAME hover card / link as legacy `mention://` markdown), which is what the
 * bare-text migration window dropped. Text runs render as inline markdown so
 * emphasis + bare-issue auto-linking survive between tokens.
 *
 * `interactive={false}` (e.g. the issue-detail provenance excerpt, itself a
 * jump-link) renders reference tokens as styled but non-clickable text — no
 * nested links, no hover card.
 */
export function InlineReferenceContent({
  content,
  parts,
  interactive = true,
  highlightQuery,
  className,
}: {
  content: string | null | undefined;
  parts: readonly MessagePart[] | null | undefined;
  interactive?: boolean;
  highlightQuery?: string;
  className?: string;
}): React.JSX.Element {
  // Key each run by its character offset in the body (stable across renders,
  // never the array index) so React reconciles the same run/token cleanly.
  const keyed = React.useMemo(() => {
    let cursor = 0;
    return projectInlineReferences(content, parts).map((seg) => {
      const key = `${seg.kind}:${cursor}`;
      cursor += seg.text.length;
      return { seg, key };
    });
  }, [content, parts]);

  return (
    <span className={cn("min-w-0", className)}>
      {keyed.map(({ seg, key }) =>
        seg.kind === "text" ? (
          <TextRun key={key} text={seg.text} highlightQuery={highlightQuery} />
        ) : (
          <ReferenceToken
            key={key}
            reference={seg.ref}
            text={seg.text}
            interactive={interactive}
            highlightQuery={highlightQuery}
          />
        ),
      )}
    </span>
  );
}

/**
 * A run of plain text between reference tokens, rendered as inline markdown so
 * **emphasis**, links, and bare-issue auto-linking survive. remark trims each
 * run's LEADING whitespace, which would collapse the space between a token and
 * the next word ("@alice check" → "@alicecheck"); we re-emit that leading
 * whitespace as a literal text node so the word break is preserved. (The
 * trailing side is kept by the inline renderer's block-flatten spacing.)
 */
function TextRun({
  text,
  highlightQuery,
}: {
  text: string;
  highlightQuery?: string;
}): React.JSX.Element {
  const leading = text.match(/^\s+/)?.[0] ?? "";
  const rest = leading ? text.slice(leading.length) : text;
  return (
    <>
      {leading}
      {rest && (
        <MemoizedMarkdown mode="inline" highlightQuery={highlightQuery}>
          {rest}
        </MemoizedMarkdown>
      )}
    </>
  );
}

/** Dispatch a single structured reference to its token renderer. */
function ReferenceToken({
  reference,
  text,
  interactive,
  highlightQuery,
}: {
  reference: ReferencePart;
  text: string;
  interactive: boolean;
  highlightQuery?: string;
}): React.JSX.Element {
  if (reference.ref_type === "mention") {
    // Non-interactive surfaces (e.g. the excerpt row, itself a link) render the
    // mention as styled text only — ActorMention would nest a link/hover card.
    // Display the span substring VERBATIM — the projector decorates, never
    // rewrites the author's content (#467/#600 contract).
    if (!interactive) {
      return (
        <span className={mentionTokenClassName("default")} data-mention-type={reference.ref_subtype}>
          {text}
        </span>
      );
    }
    // Interactive: reuse the ONE mention token (brand ink + hover profile card +
    // click) so structured mentions look/behave exactly like legacy ones. The
    // label carries the anchored `@Label` (name resolution happens inside).
    return (
      <ActorMention
        type={reference.ref_subtype ?? "member"}
        id={reference.ref_id}
        label={reference.label ?? text}
        highlightQuery={highlightQuery}
      />
    );
  }

  // issue-ref (#469): raft-style lightweight inline link — uniform link color,
  // no inline status decoration; the status lives in the hover card.
  // Non-interactive surfaces (the excerpt) render the span substring as styled
  // text and must NOT resolve the issue — returning here keeps the live-issue
  // query out of those rows entirely.
  if (!interactive) {
    return <span className="text-brand">{text}</span>;
  }
  return <IssueRefToken reference={reference} text={text} />;
}

type IssueRefPart = Extract<ReferencePart, { ref_type: "issue-ref" }>;

const ISSUE_STATUSES: readonly string[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
];

const ISSUE_PRIORITIES: readonly string[] = ["urgent", "high", "medium", "low", "none"];

/** Guard against a status we have no renderer for (StatusIcon would blow up). */
function toIssueStatus(value: string | undefined): IssueStatus | null {
  return value && ISSUE_STATUSES.includes(value) ? (value as IssueStatus) : null;
}

/**
 * Same guard for priority. `Issue.priority` is *typed* `IssuePriority`, but it
 * arrives from an unvalidated API cast — a value we've never seen (contract drift,
 * a future priority) would make `PRIORITY_CONFIG[value]` undefined and throw on
 * `.label`, taking the whole card down. Unknown → hidden, exactly like status.
 */
function toIssuePriority(value: string | undefined): IssuePriority | null {
  return value && ISSUE_PRIORITIES.includes(value) ? (value as IssuePriority) : null;
}

function IssueRefToken({
  reference,
  text,
}: {
  reference: IssueRefPart;
  text: string;
}): React.JSX.Element {
  const paths = useWorkspacePaths();
  // Mutable issue state is resolved live (#504), just like an @actor hover card
  // fetches a live profile. The persisted part stays anchor/identity only
  // (ref_id + span), so later issue changes cannot leave the peek stale.
  const issue = useResolvedIssue(reference.ref_id);

  // The token text itself is still the author's exact span substring — the
  // projector links it, never rewrites it or synthesizes a prefix (#467/#600).
  const link = (
    <AppLink href={paths.issueDetail(reference.ref_id)} className="text-brand hover:underline">
      {text}
    </AppLink>
  );

  // Nothing resolved yet (loading / deleted / other workspace / no permission) →
  // plain clickable token, and we render NOTHING rather than an empty shell: an
  // inline hover is a high-frequency, low-intent gesture (the pointer just sweeps
  // across), so a skeleton that flashes and refills is worse than a card that
  // opens 100ms later (Iris's #504 spec §3.3).
  const title = issue?.title;
  const status = toIssueStatus(issue?.status);
  if (!title && !status) return link;

  return (
    <HoverCard>
      <HoverCardTrigger render={<span />} className="inline">
        {link}
      </HoverCardTrigger>
      <HoverCardContent side="top" sideOffset={8} className="w-[320px] p-3">
        <div className="min-w-0">
          <div className="text-[11px] leading-none text-muted-foreground">{text}</div>
          {title ? (
            <div className="mt-1 text-sm font-medium leading-snug text-foreground">{title}</div>
          ) : null}
          <IssuePeekProperties issue={issue} status={status} />
        </div>
      </HoverCardContent>
    </HoverCard>
  );
}

/**
 * The peek's property row: status · priority · assignee · project (#504 — the four
 * Frank named; Linear's peek field list backs the first three).
 *
 * Deliberately NOT description/labels/estimate/dates: those fit Linear's full-screen
 * quicklook, not an inline hover — the card's job is "judge without opening it", not
 * to become a second detail page (Iris's spec §3.1).
 *
 * Every field is independently omitted when absent — we never draw a placeholder or
 * a guessed value.
 */
function IssuePeekProperties({
  issue,
  status,
}: {
  issue: Issue | undefined;
  status: IssueStatus | null;
}): React.JSX.Element | null {
  const { getActorName } = useActorName();
  if (!issue) return null;

  // `none` is the ABSENCE of a priority, not a value worth a row; anything we
  // have no renderer for is dropped rather than risked (see toIssuePriority).
  const rawPriority = toIssuePriority(issue.priority);
  const priority = rawPriority && rawPriority !== "none" ? rawPriority : null;
  // Assignee resolves live by id, same rule as status: the name follows the
  // workspace, never a snapshot.
  const assignee =
    issue.assignee_type && issue.assignee_id
      ? getActorName(issue.assignee_type, issue.assignee_id) || null
      : null;
  const projectId = issue.project_id ?? null;

  if (!status && !priority && !assignee && !projectId) return null;

  return (
    <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
      {status ? (
        <span className="inline-flex items-center gap-1">
          <StatusIcon status={status} className="h-3.5 w-3.5" />
          {STATUS_CONFIG[status].label}
        </span>
      ) : null}
      {priority ? (
        <span className="inline-flex items-center gap-1">
          <PriorityIcon priority={priority} className="h-3.5 w-3.5" />
          {PRIORITY_CONFIG[priority].label}
        </span>
      ) : null}
      {assignee ? <span className="truncate">{assignee}</span> : null}
      {projectId ? <ProjectChip projectId={projectId} /> : null}
    </div>
  );
}
