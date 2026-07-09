"use client";

import * as React from "react";
import {
  Markdown as MarkdownBase,
  highlightSearchText,
  type MarkdownProps as MarkdownBaseProps,
  type RenderMode,
} from "@multica/ui/markdown";
import { useConfigStore } from "@multica/core/config";
import { api } from "@multica/core/api";
import type { Attachment as AttachmentRecord } from "@multica/core/types";
import { useWorkspacePaths, useCurrentWorkspace } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import { agentColor } from "./agent-color";
import { ActorProfileTrigger } from "./actor-profile-popover";
import { IssueMentionCard } from "../issues/components/issue-mention-card";
import { ProjectChip } from "../projects/components/project-chip";
import { AppLink } from "../navigation/app-link";
import { Attachment as AttachmentRenderer } from "../editor/attachment";
import { AttachmentDownloadProvider } from "../editor/attachment-download-context";
import { WindyCreateAgentLink } from "./windy-create-agent-links";
import { isWindyCreateAgentLink } from "./windy-create-agent-link-utils";

export type { RenderMode };

export interface MarkdownProps extends MarkdownBaseProps {
  /**
   * Attachments associated with the surrounding entity (chat message, skill
   * file). When passed, the renderer resolves inline image / file-card URLs
   * to full attachment records via AttachmentDownloadProvider, unlocking the
   * unified hover toolbar / lightbox / preview-modal behavior used in
   * editor surfaces.
   */
  attachments?: AttachmentRecord[];
}

/**
 * Default renderMention that delegates to entity chips for issue/project mentions
 * and renders a styled span for other mention types.
 */
function ProjectMentionCard({ projectId }: { projectId: string }): React.ReactNode {
  const p = useWorkspacePaths();
  return (
    <AppLink href={p.projectDetail(projectId)} className="project-mention not-prose inline-flex">
      <ProjectChip
        projectId={projectId}
        className="cursor-pointer hover:bg-accent transition-colors"
      />
    </AppLink>
  );
}

/**
 * Member / agent / @all mention — a colored identity pill matching the editor
 * composer's mention chips. The name is resolved from the workspace cache
 * (same resolver as ActorAvatar) so renames reflect immediately.
 *
 * Member/agent chips use the full profile popover (same surface as message
 * author avatars/names). @all is a broadcast keyword (Slack-style): styled
 * pill only, no hover card — it is not a person/profile entity.
 */
function ActorMention({
  type,
  id,
  label,
  highlightQuery,
}: {
  type: string;
  id: string;
  label?: string;
  highlightQuery?: string;
}): React.JSX.Element {
  const actorNames = useActorName();
  const { getActorName } = actorNames;
  // The link text is usually "@Name"; strip the leading @ so we don't double
  // it, and use it as the fallback when the id isn't in the workspace cache.
  const fallback = label ? label.replace(/^@+/, "").trim() || undefined : undefined;
  const name = type === "all" ? "all" : getActorName(type, id, fallback);
  const color = agentColor(id);
  const chip = (
    <span
      className="not-prose font-semibold"
      style={{
        color: color.fg,
        backgroundColor: color.bg,
        borderRadius: "0.3125rem",
        padding: "0.0625rem 0.3125rem",
      }}
    >
      {highlightSearchText(`@${name}`, highlightQuery)}
    </span>
  );

  if (type === "member" || type === "agent") {
    return (
      <ActorProfileTrigger
        memberType={type === "agent" ? "agent" : "user"}
        memberId={id}
        triggerElement="span"
      >
        {chip}
      </ActorProfileTrigger>
    );
  }

  // @all (and any other non-person mention type) — pill only, no card.
  return chip;
}

function defaultRenderMention(
  {
    type,
    id,
    label,
  }: {
    type: string;
    id: string;
    label?: string;
  },
  highlightQuery?: string,
): React.ReactNode {
  if (type === "issue") {
    return <IssueMentionCard issueId={id} />;
  }
  if (type === "project") {
    return <ProjectMentionCard projectId={id} />;
  }
  return (
    <ActorMention
      type={type}
      id={id}
      label={label}
      highlightQuery={highlightQuery}
    />
  );
}

// A sticker token (:sticker:<id>:) is preprocessed into an image whose src is
// the public sticker endpoint. Matching that exact shape lets us render it as a
// lightweight inline sticker instead of the heavyweight attachment chrome.
const STICKER_SRC = /^\/api\/stickers\/([a-z0-9-]+)$/;

// absolutizeStickerURL pins the API base for surfaces whose document origin is
// not the API host (Electron desktop). On web, getBaseUrl() is empty (the
// Next.js rewrite proxies /api/*), so the site-relative path is left as-is. The
// api singleton is a Proxy that yields undefined before init, hence optional
// chaining.
function absolutizeStickerURL(src: string): string {
  const baseUrl = (api.getBaseUrl?.() ?? "").replace(/\/+$/, "");
  return baseUrl ? `${baseUrl}${src}` : src;
}

/**
 * Inline sticker. Sized like a chat sticker (not a full image), no lightbox or
 * download chrome. A missing/unknown id 404s the endpoint, so on error we
 * render nothing rather than a broken-image icon — the message text stays
 * intact (graceful degradation per the API-compat rules).
 */
function StickerImage({ id, alt }: { id: string; alt: string }): React.ReactNode {
  const [failed, setFailed] = React.useState(false);
  if (failed) return null;
  return (
    <img
      src={absolutizeStickerURL(`/api/stickers/${id}`)}
      alt={alt || `:sticker:${id}:`}
      className="not-prose my-1 inline-block select-none align-bottom"
      style={{ width: "6.5rem", height: "6.5rem" }}
      draggable={false}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}

function renderImage(
  { src, alt }: { src: string; alt: string },
  enableStickerShortcodes = true,
): React.ReactNode {
  const stickerMatch = STICKER_SRC.exec(src);
  if (enableStickerShortcodes && stickerMatch?.[1]) {
    return <StickerImage id={stickerMatch[1]} alt={alt} />;
  }
  return (
    <AttachmentRenderer
      attachment={{
        kind: "url",
        url: src,
        filename: alt,
        // chat / skill markdown `![]()` is structurally an image. Without
        // forceKind, empty/descriptive alt strings would route to the
        // file-card chrome via getPreviewKind autodetect.
        forceKind: "image",
      }}
    />
  );
}

function renderFileCard({
  href,
  filename,
}: {
  href: string;
  filename: string;
}): React.ReactNode {
  return (
    <AttachmentRenderer
      attachment={{ kind: "url", url: href, filename }}
    />
  );
}

/**
 * App-level Markdown wrapper. Injects:
 *   - entity chips for issue/project mentions
 *   - cdnDomain from the config store (drives fileCard preprocessing)
 *   - unified <Attachment> as the image / file-card renderer
 *   - AttachmentDownloadProvider so url → record resolution works inside
 *     the injected <Attachment> components
 */
export function Markdown(props: MarkdownProps): React.JSX.Element {
  const cdnDomain = useConfigStore((s) => s.cdnDomain);
  // Auto-link bare issue identifiers (e.g. "MUL-123") to issue chips, scoped to
  // the current workspace's prefix so it can't false-positive on tokens like
  // "UTF-8". Empty/absent prefix disables it.
  const issueRefPrefix = useCurrentWorkspace()?.issue_prefix || undefined;
  const { attachments, highlightQuery, enableStickerShortcodes = true, ...rest } = props;
  const renderAppImage = React.useCallback(
    (image: { src: string; alt: string }) => renderImage(image, enableStickerShortcodes),
    [enableStickerShortcodes],
  );
  const renderMention = React.useCallback(
    (mention: { type: string; id: string; label?: string }) =>
      defaultRenderMention(mention, highlightQuery),
    [highlightQuery],
  );
  const RenderAppLink = React.useCallback(
    ({ href, children }: { href: string; children: React.ReactNode }) => {
      if (isWindyCreateAgentLink(href)) {
        return <WindyCreateAgentLink href={href}>{children}</WindyCreateAgentLink>;
      }
      return null;
    },
    [],
  );
  return (
    <AttachmentDownloadProvider attachments={attachments}>
      <MarkdownBase
        renderMention={renderMention}
        renderAppLink={RenderAppLink}
        renderImage={renderAppImage}
        renderFileCard={renderFileCard}
        cdnDomain={cdnDomain}
        issueRefPrefix={issueRefPrefix}
        highlightQuery={highlightQuery}
        enableStickerShortcodes={enableStickerShortcodes}
        {...rest}
      />
    </AttachmentDownloadProvider>
  );
}

export const MemoizedMarkdown = React.memo(Markdown);
MemoizedMarkdown.displayName = "MemoizedMarkdown";
