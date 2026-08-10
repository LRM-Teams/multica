"use client";

/**
 * MentionView — NodeView for rendering @mentions inline in the editor.
 *
 * Member/agent/squad/@all: Slack soft-bg token via `mentionTokenClassName`
 * (brand ink + light rest fill — not per-actor identity colors, not capsules).
 * Issue mentions: IssueChip inside a custom <a> that supports cmd/shift-click
 * to open in a new tab (AppLink doesn't expose that intent hook).
 *
 * Issue chip sizing: must fit within the paragraph line box (14px * 1.625 =
 * 22.75px). Card is text-xs (12px) + py-0.5 + border ≈ 22px total. The
 * `vertical-align: middle` rule on `[data-node-view-wrapper]` in CSS handles
 * line-box alignment; setting it on the inner <a> has no effect because the
 * wrapper is the outermost inline element.
 */

import { NodeViewWrapper } from "@tiptap/react";
import type { NodeViewProps } from "@tiptap/react";
import type { ReactNode } from "react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useAuthStore } from "@multica/core/auth";
import { useNavigation } from "../../navigation";
import { IssueChip } from "../../issues/components/issue-chip";
import { ProjectChip } from "../../projects/components/project-chip";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { useMemberPanelStore } from "@multica/core/workspace";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useOpenAgentPanel } from "../../common/agent-panel-context";
import { useOpenMemberPanel } from "../../common/member-panel-context";
import { useActorMentionChipLabel } from "../../common/actor-mention-chip-label";
import {
  mentionTokenClassName,
  resolveMentionTokenKind,
  type MentionTokenVariant,
} from "../../common/mention-token";

export function MentionView({ node, mentionVariant = "soft-bg" }: NodeViewProps & { mentionVariant?: MentionTokenVariant }) {
  const viewerUserId = useAuthStore((s) => s.user?.id ?? null);
  // Same context-or-global-store fallback as ActorAvatarPanelTrigger, so an
  // @mention opens the panel whether it renders inside channels/DM (context)
  // or anywhere else an editor can render one (issue comments, etc.).
  const openAgentPanelFromContext = useOpenAgentPanel();
  const openAgentPanelFromStore = useAgentPanelStore((s) => s.open);
  const closeAgentPanel = useAgentPanelStore((s) => s.close);
  const openAgentPanel = openAgentPanelFromContext ?? openAgentPanelFromStore;
  const openMemberPanelFromContext = useOpenMemberPanel();
  const openMemberPanelFromStore = useMemberPanelStore((s) => s.open);
  const openMemberPanel = openMemberPanelFromContext ?? openMemberPanelFromStore;
  const { type, id, label } = node.attrs;

  if (type === "issue") {
    return (
      <NodeViewWrapper as="span" className="inline">
        <IssueMention issueId={id} fallbackLabel={label} />
      </NodeViewWrapper>
    );
  }

  if (type === "project") {
    return (
      <NodeViewWrapper as="span" className="inline">
        <ProjectMention projectId={id} fallbackLabel={label} />
      </NodeViewWrapper>
    );
  }

  // Member / agent / squad / all → Slack soft-bg token. Identity colors stay
  // on avatars (`agentColor`), not the token fill.
  // @all is a fixed protocol token (same as message renderer + markdown
  // `[@all](mention://all/all)`). Picker shows the localized "All members"
  // description; the chip always reads `@all`.
  // LRM-515: same render-time display_name path as ActorMention (not slug).
  return (
    <NodeViewWrapper as="span" className="inline">
      <ActorMentionEditorChip
        type={type}
        id={id}
        label={label}
        viewerUserId={viewerUserId}
        openAgentPanel={openAgentPanel}
        openMemberPanel={openMemberPanel}
        closeAgentPanel={closeAgentPanel}
        mentionVariant={mentionVariant}
      />
    </NodeViewWrapper>
  );
}

function ActorMentionEditorChip({
  type,
  id,
  label,
  viewerUserId,
  openAgentPanel,
  openMemberPanel,
  closeAgentPanel,
  mentionVariant = "soft-bg",
}: {
  type: string;
  id: string;
  label?: string;
  viewerUserId: string | null;
  openAgentPanel: ((id: string) => void) | null | undefined;
  openMemberPanel: ((id: string) => void) | null | undefined;
  closeAgentPanel: () => void;
  mentionVariant?: MentionTokenVariant;
}): ReactNode {
  const { name, unresolved, handlePeek } = useActorMentionChipLabel(type, id, label);
  const kind = resolveMentionTokenKind(type, id, viewerUserId);
  const chip = (
    <span
      className={mentionTokenClassName(
        kind,
        unresolved
          ? "bg-muted text-muted-foreground hover:bg-muted focus-visible:bg-muted"
          : undefined,
        mentionVariant,
      )}
      data-mention-kind={kind}
      data-mention-type={type}
      data-mention-unresolved={unresolved ? "true" : undefined}
      title={handlePeek ? `@${handlePeek}` : undefined}
    >
      @{name}
    </span>
  );

  if (type === "member" || type === "agent") {
    const openOnClick =
      type === "agent" && openAgentPanel
        ? () => openAgentPanel(id)
        : type === "member" && openMemberPanel
          ? () => {
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

  return chip;
}

function ProjectMention({
  projectId,
  fallbackLabel,
}: {
  projectId: string;
  fallbackLabel?: string;
}) {
  const p = useWorkspacePaths();
  const { push, openInNewTab } = useNavigation();
  const projectPath = p.projectDetail(projectId);

  const handleClick = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.metaKey || e.ctrlKey || e.shiftKey) {
      if (openInNewTab) openInNewTab(projectPath, fallbackLabel);
      return;
    }
    push(projectPath);
  };

  return (
    <a href={projectPath} onClick={handleClick} className="project-mention inline-flex">
      <ProjectChip
        projectId={projectId}
        fallbackLabel={fallbackLabel}
        className="cursor-pointer hover:bg-accent transition-colors"
      />
    </a>
  );
}

function IssueMention({
  issueId,
  fallbackLabel,
}: {
  issueId: string;
  fallbackLabel?: string;
}) {
  const p = useWorkspacePaths();
  const { push, openInNewTab } = useNavigation();
  const issuePath = p.issueDetail(issueId);

  const handleClick = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.metaKey || e.ctrlKey || e.shiftKey) {
      if (openInNewTab) openInNewTab(issuePath, fallbackLabel);
      return;
    }
    push(issuePath);
  };

  return (
    <a href={issuePath} onClick={handleClick} className="issue-mention inline-flex">
      <IssueChip
        issueId={issueId}
        fallbackLabel={fallbackLabel}
        className="cursor-pointer hover:bg-accent transition-colors"
      />
    </a>
  );
}
