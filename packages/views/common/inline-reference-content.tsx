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
import { ProjectIcon } from "../projects/components/project-icon";
import { ActorAvatar } from "./actor-avatar";
import { useQuery } from "@tanstack/react-query";
import { projectListOptions, projectDetailOptions } from "@multica/core/projects/queries";
import { useWorkspaceId } from "@multica/core/hooks";
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
            <div className="mt-1 line-clamp-2 text-sm font-semibold leading-snug text-foreground">
              {title}
            </div>
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
 * ONE grammar for every property (#517, Iris's property-display spec): each is a
 * 14px marker + 4px + a 12px muted label, 10px between properties, and NOTHING wears
 * a chip/pill/background. That grammar is not new — it is `list-row`'s house style
 * (ActorAvatar / ProjectIcon+title / PriorityIcon), which is also what Linear does.
 * Frank's "有点乱" was this card diverging from it: a bare-text assignee (an orphan
 * next to two icon+label pairs) and a bordered ProjectChip made four grammars share
 * one row.
 *
 * Every field is independently omitted when absent — we never draw a placeholder or
 * a guessed value. Absence is decided ONLY by the issue's own data: the card must
 * never inherit the list's `storeProperties` display toggles, or it would hide a
 * fact because of an unrelated setting elsewhere (Iris's spec, hard rule for #518).
 */
function IssuePeekProperties({
  issue,
  status,
}: {
  issue: Issue | undefined;
  status: IssueStatus | null;
}): React.JSX.Element | null {
  // `none` is the ABSENCE of a priority, not a value worth showing; anything we
  // have no renderer for is dropped rather than risked (see toIssuePriority).
  const rawPriority = toIssuePriority(issue?.priority);
  const priority = rawPriority && rawPriority !== "none" ? rawPriority : null;
  const hasAssignee = Boolean(issue?.assignee_type && issue?.assignee_id);
  const projectId = issue?.project_id ?? null;

  if (!issue) return null;
  if (!status && !priority && !hasAssignee && !projectId) return null;

  // Single line, and it truncates from the tail — project gives way first, then
  // assignee; status never shrinks (it is the one property that must stay legible).
  return (
    <div className="mt-2 flex items-center gap-2.5 overflow-hidden text-xs text-muted-foreground">
      {status ? (
        <span className="inline-flex shrink-0 items-center gap-1">
          <StatusIcon status={status} className="size-3.5" />
          {STATUS_CONFIG[status].label}
        </span>
      ) : null}
      {priority ? (
        <span className="inline-flex shrink-0 items-center gap-1">
          <PriorityIcon priority={priority} className="size-3.5" />
          {PRIORITY_CONFIG[priority].label}
        </span>
      ) : null}
      {hasAssignee ? (
        <PeekAssignee actorType={issue.assignee_type!} actorId={issue.assignee_id!} />
      ) : null}
      {projectId ? <PeekProject projectId={projectId} /> : null}
    </div>
  );
}

/**
 * Assignee: avatar + display name — the avatar is the person's "icon", which is what
 * put this property back in the row's rhythm (Iris's spec §1: 补头像, 病灶就没了).
 *
 * The avatar deliberately carries NO hover card of its own: this already lives inside
 * the peek's HoverCardContent, and ActorAvatar's own docs warn against nesting a
 * popover inside one. `profileLink` is off for the same reason — the peek is a
 * read-only "judge it without opening it" surface, not a second navigation layer.
 */
function PeekAssignee({
  actorType,
  actorId,
}: {
  actorType: string;
  actorId: string;
}): React.JSX.Element | null {
  const { getActorName } = useActorName();
  // Resolved live by id, same rule as status — the name follows the workspace,
  // never a write-time snapshot.
  const name = getActorName(actorType, actorId);
  if (!name) return null;
  return (
    <span className="inline-flex min-w-0 shrink items-center gap-1">
      <ActorAvatar
        actorType={actorType}
        actorId={actorId}
        size={14}
        enableHoverCard={false}
        profileLink={false}
      />
      <span className="truncate">{name}</span>
    </span>
  );
}

/**
 * Project: icon + title, NO chip. The border/background a chip carries reads as
 * "this value owns a container", which is exactly the weight that made the row look
 * busy — a project is just another property here (Iris's spec §1: project 去 chip).
 *
 * Shrinks before every other property, so a long project title is what gives way
 * when the row runs out of room.
 */
function PeekProject({ projectId }: { projectId: string }): React.JSX.Element | null {
  const wsId = useWorkspaceId();
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const listProject = projects.find((p) => p.id === projectId);
  // A project outside the cached list (archived, or simply not fetched yet) still
  // has to show — the issue genuinely belongs to it. Same fallback ProjectChip uses.
  const { data: detailProject } = useQuery({
    ...projectDetailOptions(wsId, projectId),
    enabled: !listProject,
  });
  const project = listProject ?? detailProject;
  // Unresolved → render nothing rather than a guessed name or an empty icon: the
  // card degrades, it never fakes (#504).
  if (!project) return null;
  return (
    <span className="inline-flex min-w-0 shrink-[2] items-center gap-1">
      <ProjectIcon project={project} size="sm" />
      <span className="truncate">{project.title}</span>
    </span>
  );
}
