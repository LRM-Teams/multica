"use client";

import * as React from "react";
import type { IssueStatus, MessagePart } from "@multica/core/types";
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

/** Guard against a status we have no renderer for (StatusIcon would blow up). */
function toIssueStatus(value: string | undefined): IssueStatus | null {
  return value && ISSUE_STATUSES.includes(value) ? (value as IssueStatus) : null;
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
  // plain clickable token. Never fake a card or fall back to the stale snapshot.
  const title = issue?.title;
  const status = toIssueStatus(issue?.status);
  if (!title && !status) return link;

  return (
    <HoverCard>
      <HoverCardTrigger render={<span />} className="inline">
        {link}
      </HoverCardTrigger>
      <HoverCardContent side="top" sideOffset={8} className="w-[300px] p-3">
        <div className="flex items-start gap-2">
          {status ? <StatusIcon status={status} className="mt-0.5 h-4 w-4" /> : null}
          <div className="min-w-0 flex-1">
            <div className="text-[11px] leading-none text-muted-foreground">{text}</div>
            {title ? (
              <div className="mt-1 text-sm font-medium leading-snug text-foreground">{title}</div>
            ) : null}
          </div>
        </div>
      </HoverCardContent>
    </HoverCard>
  );
}
