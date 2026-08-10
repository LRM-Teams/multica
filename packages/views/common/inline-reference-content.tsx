"use client";

import * as React from "react";
import type { MessagePart } from "@multica/core/types";
import { MemoizedMarkdown, ActorMention } from "./markdown";
import { cn } from "@multica/ui/lib/utils";
import { mentionTokenClassName } from "./mention-token";
import { useActorMentionChipLabel } from "./actor-mention-chip-label";
import { IssueRefLink } from "../issues/components/issue-ref-link";
import { ChannelRefLink } from "../channels/components/channel-ref-link";
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
  sourceMessageId,
  issueAppearance = "inline",
  mentionVariant = "soft-bg",
}: {
  content: string | null | undefined;
  parts: readonly MessagePart[] | null | undefined;
  interactive?: boolean;
  highlightQuery?: string;
  className?: string;
  /** The Messages row that owns these references, for a precise return target. */
  sourceMessageId?: string;
  /** LRM-609 A' system rows: unfilled brand text link (title-primary). */
  issueAppearance?: "inline" | "systemChip";
  /** LRM-1386 — chat surfaces pass `plain` for non-pill mention text. */
  mentionVariant?: import("./mention-token").MentionTokenVariant;
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
          <TextRun
            key={key}
            text={seg.text}
            highlightQuery={highlightQuery}
            sourceMessageId={sourceMessageId}
          />
        ) : (
          <ReferenceToken
            key={key}
            reference={seg.ref}
            text={seg.text}
            interactive={interactive}
            highlightQuery={highlightQuery}
            sourceMessageId={sourceMessageId}
            emphasis={seg.emphasis}
            issueAppearance={issueAppearance}
            mentionVariant={mentionVariant}
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
  sourceMessageId,
}: {
  text: string;
  highlightQuery?: string;
  sourceMessageId?: string;
}): React.JSX.Element {
  const leading = text.match(/^\s+/)?.[0] ?? "";
  const rest = leading ? text.slice(leading.length) : text;
  return (
    <>
      {leading}
      {rest && (
        <MemoizedMarkdown
          mode="inline"
          highlightQuery={highlightQuery}
          sourceMessageId={sourceMessageId}
        >
          {rest}
        </MemoizedMarkdown>
      )}
    </>
  );
}

/**
 * Dispatch a single structured reference to its token renderer, wrapping it
 * in `<strong>`/`<em>` when {@link stripStraddlingEmphasisMarkers} (#635)
 * folded a directly-touching `**`/`__`/`*`/`_` pair into this segment — the
 * author wrote `**LRM-188**`, and each side's markdown pass ran independently
 * so neither one alone had a matched pair to parse as bold.
 */
function ReferenceToken({
  reference,
  text,
  interactive,
  highlightQuery,
  sourceMessageId,
  emphasis,
  issueAppearance,
  mentionVariant = "soft-bg",
}: {
  reference: ReferencePart;
  text: string;
  interactive: boolean;
  highlightQuery?: string;
  sourceMessageId?: string;
  emphasis?: "strong" | "em";
  issueAppearance: "inline" | "systemChip";
  mentionVariant?: import("./mention-token").MentionTokenVariant;
}): React.JSX.Element {
  const token = renderReferenceToken({
    reference,
    text,
    interactive,
    highlightQuery,
    sourceMessageId,
    issueAppearance,
    mentionVariant,
  });
  if (emphasis === "strong") return <strong>{token}</strong>;
  if (emphasis === "em") return <em>{token}</em>;
  return token;
}

function renderReferenceToken({
  reference,
  text,
  interactive,
  highlightQuery,
  sourceMessageId,
  issueAppearance,
  mentionVariant = "soft-bg",
}: {
  reference: ReferencePart;
  text: string;
  interactive: boolean;
  highlightQuery?: string;
  sourceMessageId?: string;
  issueAppearance: "inline" | "systemChip";
  mentionVariant?: import("./mention-token").MentionTokenVariant;
}): React.JSX.Element {
  if (reference.ref_type === "mention") {
    // Non-interactive surfaces (e.g. the excerpt row, itself a link) render the
    // mention as styled text only — ActorMention would nest a link/hover card.
    // LRM-515: still resolve display_name at render-time (not authored slug).
    if (!interactive) {
      return (
        <NonInteractiveActorMention
          type={reference.ref_subtype ?? "member"}
          id={reference.ref_id}
          label={reference.label ?? text}
          variant={mentionVariant}
        />
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
        variant={mentionVariant}
      />
    );
  }

  if (reference.ref_type === "channel-ref") {
    // A channel-ref span is one of two shapes: the composer's whole
    // `[Label](mention://channel/<uuid>)` markdown link, or (LRM-1153) the bare
    // `#name` an agent or a hand-typed message wrote, which the server now
    // anchors too. Neither may be rendered verbatim: the link form would leak
    // raw markdown + the internal UUID (Wren, PR review — same class of bug as
    // message-preview.ts's channel-ref branch), and only the resolved label is
    // guaranteed to be the live channel name. Both branches therefore render
    // from `label`, normalized to exactly one leading `#` so the author's hash
    // survives on the non-interactive surface (the chip draws its own).
    if (!interactive) {
      return <span className="text-brand">{`#${stripLeadingHash(reference.label ?? reference.ref_id)}`}</span>;
    }
    return <ChannelRefLink channelId={reference.ref_id} label={reference.label ?? text} />;
  }

  // Block agent:create Proposals render in MessagePartsRenderer, not as
  // inline tokens.
  if (reference.ref_type === "agent:create") {
    return <span className="text-muted-foreground">{reference.label ?? text}</span>;
  }

  // issue-ref (#469): raft-style lightweight inline link — uniform link color,
  // no inline status decoration; the status lives in the hover card.
  // Non-interactive surfaces (the excerpt) render the span substring as styled
  // text and must NOT resolve the issue — returning here keeps the live-issue
  // query out of those rows entirely.
  if (reference.ref_type !== "issue-ref") {
    return <span className="text-brand">{text}</span>;
  }
  if (!interactive) {
    return <span className="text-brand">{text}</span>;
  }
  return (
    <IssueRefToken
      reference={reference}
      text={text}
      sourceMessageId={sourceMessageId}
      appearance={issueAppearance}
    />
  );
}

type IssueRefPart = Extract<ReferencePart, { ref_type: "issue-ref" }>;

/**
 * Normalize a channel label to its bare name so callers can add exactly one
 * `#`. The server sends the bare channel name, but a composer-authored label
 * may carry the author's own hash — double-prefixing it ("##ops") is the bug
 * this guards, and it mirrors ChannelChip's identical tolerance.
 */
function stripLeadingHash(label: string): string {
  return label.startsWith("#") ? label.slice(1) : label;
}

/** Non-clickable mention chip with LRM-515 display_name primary ink. */
function NonInteractiveActorMention({
  type,
  id,
  label,
  variant = "soft-bg",
}: {
  type: string;
  id: string;
  label?: string;
  variant?: import("./mention-token").MentionTokenVariant;
}): React.JSX.Element {
  const { name, unresolved, handlePeek } = useActorMentionChipLabel(type, id, label);
  return (
    <span
      className={mentionTokenClassName(
        "default",
        unresolved
          ? "bg-muted text-muted-foreground hover:bg-muted focus-visible:bg-muted"
          : undefined,
        variant,
      )}
      data-mention-type={type}
      data-mention-unresolved={unresolved ? "true" : undefined}
      title={handlePeek ? `@${handlePeek}` : undefined}
    >
      @{name}
    </span>
  );
}

/**
 * An anchored issue reference. The rendering itself lives in {@link IssueRefLink} —
 * shared with the unanchored linkify fallback (#520) so the two cannot drift into
 * two different looks. This component only supplies what the projector knows: the
 * anchored id and the author's exact span substring.
 */
function IssueRefToken({
  reference,
  text,
  sourceMessageId,
  appearance = "inline",
}: {
  reference: IssueRefPart;
  text: string;
  sourceMessageId?: string;
  appearance?: "inline" | "systemChip";
}): React.JSX.Element {
  return (
    <IssueRefLink
      issueId={reference.ref_id}
      text={text}
      source="anchor"
      sourceMessageId={sourceMessageId}
      appearance={appearance}
    />
  );
}
