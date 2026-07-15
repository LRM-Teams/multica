"use client";

import * as React from "react";
import type { MessagePart } from "@multica/core/types";
import { MemoizedMarkdown, ActorMention } from "./markdown";
import { AppLink } from "../navigation/app-link";
import { useWorkspacePaths } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { mentionTokenClassName } from "./mention-token";
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
          // Inline markdown so **emphasis** and bare-issue auto-linking survive
          // in the runs between tokens, while flowing inline with the tokens.
          <MemoizedMarkdown key={key} mode="inline" highlightQuery={highlightQuery}>
            {seg.text}
          </MemoizedMarkdown>
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
  // no status decoration inline; hover/detail land in the follow-up wiring.
  return <IssueRefToken reference={reference} text={text} interactive={interactive} />;
}

function IssueRefToken({
  reference,
  text,
  interactive,
}: {
  reference: ReferencePart;
  text: string;
  interactive: boolean;
}): React.JSX.Element {
  const paths = useWorkspacePaths();
  // Render the author's exact span substring (`MUL-123` or `#MUL-123`) — the
  // projector links it, it NEVER rewrites the content or synthesizes a prefix
  // (#467/#600: content preserved as-is, span only decorates; metadata label is
  // not the render source).
  if (!interactive) {
    return <span className="text-brand">{text}</span>;
  }
  return (
    <AppLink href={paths.issueDetail(reference.ref_id)} className="text-brand hover:underline">
      {text}
    </AppLink>
  );
}
