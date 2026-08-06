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
import { useAuthStore } from "@multica/core/auth";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { useMemberPanelStore } from "@multica/core/workspace";
import { ActorProfileTrigger } from "./actor-profile-popover";
import { useOpenAgentPanel } from "./agent-panel-context";
import { useOpenMemberPanel } from "./member-panel-context";
import { IssueMentionCard } from "../issues/components/issue-mention-card";
import { ProjectChip } from "../projects/components/project-chip";
import { AppLink } from "../navigation/app-link";
import { Attachment as AttachmentRenderer } from "../editor/attachment";
import { AttachmentDownloadProvider } from "../editor/attachment-download-context";
import { WindyCreateAgentLink } from "./windy-create-agent-links";
import { isWindyCreateAgentLink } from "./windy-create-agent-link-utils";
import { useActorMentionChipLabel } from "./actor-mention-chip-label";
import {
  mentionTokenClassName,
  resolveMentionTokenKind,
} from "./mention-token";

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
  /** Source row id for issue references rendered inside a Messages timeline. */
  sourceMessageId?: string;
  /**
   * LRM-830 — citation renderer for [[cit:id]] tokens in report prose.
   * When provided, replaces the default link renderer for citation anchors.
   */
  renderCitation?: (props: { citationId: string; label: string }) => React.ReactNode;
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
 * Member / agent / @all / squad mention — Slack soft-bg token (not a capsule
 * and not a per-actor identity color). Avatars keep `agentColor`; tokens share
 * one hue family via `mentionTokenClassName`.
 *
 * Member/agent use the full profile popover (same surface as message author
 * avatars/names). @all is a broadcast keyword: token only, no profile card.
 */
/**
 * Member / agent / @all / squad mention token — Slack soft-bg emphasis link,
 * with the hover profile card + click-to-open for person mentions. Exported so
 * the shared inline-reference projector (#463) renders structured `reference`
 * mentions the SAME way as legacy `mention://` markdown links — one mention
 * look + one hover card everywhere, no second implementation (restores the
 * hover the bare-`@Label` migration window dropped).
 */
export function ActorMention({
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
  const viewerUserId = useAuthStore((s) => s.user?.id ?? null);
  // #349/#447 / LRM-893: rendered @agent → agent side panel; @member → member
  // Profile dock (same as avatar click). Context preferred, global store fallback.
  const openAgentPanelFromContext = useOpenAgentPanel();
  const openAgentPanelFromStore = useAgentPanelStore((s) => s.open);
  const closeAgentPanel = useAgentPanelStore((s) => s.close);
  const openAgentPanel = openAgentPanelFromContext ?? openAgentPanelFromStore;
  const openMemberPanelFromContext = useOpenMemberPanel();
  const openMemberPanelFromStore = useMemberPanelStore((s) => s.open);
  const openMemberPanel = openMemberPanelFromContext ?? openMemberPanelFromStore;
  // LRM-515: render-time display_name (not authored @handle slug). Handle is
  // peek-only via title when we resolved a real name.
  const { name, unresolved, handlePeek } = useActorMentionChipLabel(type, id, label);
  const kind = resolveMentionTokenKind(type, id, viewerUserId);
  const chip = (
    <span
      className={mentionTokenClassName(
        kind,
        unresolved
          ? "bg-muted text-muted-foreground hover:bg-muted focus-visible:bg-muted"
          : undefined,
      )}
      data-mention-kind={kind}
      data-mention-type={type}
      data-mention-unresolved={unresolved ? "true" : undefined}
      title={handlePeek ? `@${handlePeek}` : undefined}
    >
      {highlightSearchText(`@${name}`, highlightQuery)}
    </span>
  );

  if (type === "member" || type === "agent") {
    const openOnClick =
      type === "agent" && openAgentPanel
        ? () => openAgentPanel(id)
        : type === "member" && openMemberPanel
          ? () => {
              // Exclusive dock: same as ActorAvatarMemberPanelTrigger.
              closeAgentPanel();
              openMemberPanel(id);
            }
          : undefined;
    return (
      <ActorProfileTrigger
        memberType={type === "agent" ? "agent" : "user"}
        memberId={id}
        triggerElement="span"
        onClickCapture={openOnClick}
      >
        {chip}
      </ActorProfileTrigger>
    );
  }

  // @all / squad (and any other non-person mention type) — token only, no card.
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
  sourceMessageId?: string,
): React.ReactNode {
  if (type === "issue") {
    // Link text is the author's label (e.g. `[LRM-487](mention://issue/<uuid>)`).
    // Dropping it forced IssueMentionCard to paint the raw UUID — on mobile that
    // truncates to `fe57cec6-…` (LRM-493). Pass it through as fallbackLabel.
    return (
      <IssueMentionCard
        issueId={id}
        fallbackLabel={label}
        sourceMessageId={sourceMessageId}
      />
    );
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
  const { attachments, highlightQuery, enableStickerShortcodes = true, sourceMessageId, renderCitation, ...rest } = props;
  const renderAppImage = React.useCallback(
    (image: { src: string; alt: string }) => renderImage(image, enableStickerShortcodes),
    [enableStickerShortcodes],
  );
  const renderMention = React.useCallback(
    (mention: { type: string; id: string; label?: string }) =>
      defaultRenderMention(mention, highlightQuery, sourceMessageId),
    [highlightQuery, sourceMessageId],
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
        renderCitation={renderCitation}
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
