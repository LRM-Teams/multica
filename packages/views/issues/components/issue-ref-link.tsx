"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import type { Issue, IssuePriority, IssueStatus } from "@multica/core/types";
import { STATUS_CONFIG, PRIORITY_CONFIG } from "@multica/core/issues/config";
import { useActorName } from "@multica/core/workspace/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWorkspaceId } from "@multica/core/hooks";
import { projectListOptions, projectDetailOptions } from "@multica/core/projects/queries";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";
import { AppLink } from "../../navigation/app-link";
import { ActorAvatar } from "../../common/actor-avatar";
import { ProjectIcon } from "../../projects/components/project-icon";
import { StatusIcon } from "./status-icon";
import { PriorityIcon } from "./priority-icon";
import { useResolvedIssue } from "./issue-chip";

/**
 * Where a rendered issue reference came from. USER-INVISIBLE by design — it is
 * emitted as a `data-ref-source` attribute purely so tests/QA can tell the two
 * paths apart, because after #520 they are deliberately indistinguishable on
 * screen.
 *
 * - `anchor`   — the server anchored this reference to a UTF-16 span (#463).
 * - `fallback` — nobody anchored it; the client's legacy linkify caught the bare
 *                identifier in prose.
 *
 * Why this exists: unifying the appearance (#520) closed the channel that made a
 * missed anchor obvious. Frank spotted #521's parser bug ONLY because the
 * unanchored occurrence rendered as a chip; once every occurrence looks the same,
 * a miss becomes invisible. Rather than keep a worse UI as a bug detector, the
 * signal moves somewhere users never see but assertions do: "N occurrences of one
 * identifier must all be `anchor`; a `fallback` means the parser missed one."
 *
 * SCAFFOLDING, NOT FURNITURE: once #521 lands and every bare reference is anchored,
 * the fallback path has nothing left to catch and both it and this attribute get
 * deleted with the rest of the legacy chain (#463/#510 tail).
 */
export type IssueRefSource = "anchor" | "fallback";

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

/**
 * THE one way an issue reference looks in a message body: a zero-decoration link
 * that opens a peek card on hover (Iris's spec; Parker's #520 ruling).
 *
 * Both paths that can produce a reference in prose render THIS component — the
 * span projector (anchored) and the legacy linkify fallback (unanchored). That is
 * the point: "the two look identical" is not a promise two call sites keep by being
 * careful, it is a fact of there being one component. A compat path may survive;
 * it may not grow a second face.
 *
 * The chip form deliberately stays in the EDITOR only: there you are operating on
 * the reference (select it, delete it whole, step the caret over it), so the box is
 * a functional signal that it is one atomic token. Here you are reading, so the
 * reference is just clickable text. Same concept, two contexts, two jobs.
 *
 * `issueId` may be a uuid OR an identifier ("LRM-126") — `useResolvedIssue` accepts
 * both, which is how the old chip showed a title for `mention://issue/LRM-126`.
 */
export function IssueRefLink({
  issueId,
  text,
  source = "anchor",
}: {
  issueId: string;
  /** Rendered verbatim — the author's own span substring; never rewritten (#467/#600). */
  text: string;
  source?: IssueRefSource;
}): React.JSX.Element {
  const paths = useWorkspacePaths();
  // Mutable issue state is resolved live (#504/#622), never from a write-time
  // snapshot — the part carries identity (ref_id + span), not state.
  const issue = useResolvedIssue(issueId);

  const link = (
    <AppLink
      href={paths.issueDetail(issue?.id ?? issueId)}
      className="text-brand hover:underline"
      // Declares "this link is an issue reference and owns its own hover card", so
      // generic link affordances (the editor's URL preview) stand down instead of
      // stacking a second popup on the peek — see link-hover-card.tsx.
      //
      // This is an ATTRIBUTE, not the old `issue-mention` CLASS, deliberately.
      // That class carried the suppression AND chip styling
      // (`.rich-text-editor a.issue-mention { color: inherit; text-decoration: none }`),
      // so reusing it would drag back the very decoration #520 removed. Behaviour
      // riding on a styling class is exactly how this broke: #520 dropped the class
      // as "chip styling", silently taking a behavioural contract with it, and my
      // test even asserted the class was GONE — green, and wrong.
      data-issue-ref=""
      data-ref-source={source}
    >
      {text}
    </AppLink>
  );

  // Nothing resolved (loading / deleted / other workspace / no permission) → plain
  // clickable token, and NOTHING rather than an empty shell: an inline hover is a
  // high-frequency, low-intent gesture (the pointer just sweeps across), so a
  // skeleton that flashes and refills is worse than a card that opens 100ms later
  // (Iris's #504 spec §3.3).
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
 * ONE grammar for every property (#517): each is a 14px marker + 4px + a 12px muted
 * label, 10px between properties, and NOTHING wears a chip/pill/background. That
 * grammar is not new — it is `list-row`'s house style (ActorAvatar / ProjectIcon+title
 * / PriorityIcon), which is also what Linear does. Frank's "有点乱" was this card
 * diverging from it.
 *
 * Every field is independently omitted when absent — never a placeholder or a guessed
 * value. Absence is decided ONLY by the issue's own data: the card must never inherit
 * the list's `storeProperties` display toggles, or it would hide a fact because of an
 * unrelated setting elsewhere (Iris's spec, hard rule for #518).
 */
function IssuePeekProperties({
  issue,
  status,
}: {
  issue: Issue | undefined;
  status: IssueStatus | null;
}): React.JSX.Element | null {
  // `none` is the ABSENCE of a priority, not a value worth showing; anything we have
  // no renderer for is dropped rather than risked (see toIssuePriority).
  const rawPriority = toIssuePriority(issue?.priority);
  const priority = rawPriority && rawPriority !== "none" ? rawPriority : null;
  const hasAssignee = Boolean(issue?.assignee_type && issue?.assignee_id);
  const projectId = issue?.project_id ?? null;

  if (!issue) return null;
  if (!status && !priority && !hasAssignee && !projectId) return null;

  // Single line, truncating from the tail — project gives way first, then assignee;
  // status never shrinks (it is the one property that must stay legible).
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
  // Resolved live by id, same rule as status — the name follows the workspace, never
  // a write-time snapshot.
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
 * Project: icon + title, NO chip. The border/background a chip carries reads as "this
 * value owns a container", which is exactly the weight that made the row look busy —
 * a project is just another property here (Iris's spec §1: project 去 chip).
 *
 * Shrinks before every other property, so a long project title is what gives way when
 * the row runs out of room.
 */
function PeekProject({ projectId }: { projectId: string }): React.JSX.Element | null {
  const wsId = useWorkspaceId();
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const listProject = projects.find((p) => p.id === projectId);
  // A project outside the cached list (archived, or simply not fetched yet) still has
  // to show — the issue genuinely belongs to it. Same fallback ProjectChip uses.
  const { data: detailProject } = useQuery({
    ...projectDetailOptions(wsId, projectId),
    enabled: !listProject,
  });
  const project = listProject ?? detailProject;
  // Unresolved → render nothing rather than a guessed name or an empty icon: the card
  // degrades, it never fakes (#504).
  if (!project) return null;
  return (
    <span className="inline-flex min-w-0 shrink-[2] items-center gap-1">
      <ProjectIcon project={project} size="sm" />
      <span className="truncate">{project.title}</span>
    </span>
  );
}
