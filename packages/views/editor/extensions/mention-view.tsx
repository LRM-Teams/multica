"use client";

/**
 * MentionView — NodeView for rendering @mentions inline in the editor.
 *
 * Member/agent/squad/@all: brand semantic pill via `mentionTokenClassName`
 * (Iris / Slack-like — not per-actor identity colors).
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
import { useWorkspacePaths } from "@multica/core/paths";
import { useAuthStore } from "@multica/core/auth";
import { useNavigation } from "../../navigation";
import { IssueChip } from "../../issues/components/issue-chip";
import { ProjectChip } from "../../projects/components/project-chip";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useOpenAgentPanel } from "../../common/agent-panel-context";
import {
  mentionTokenClassName,
  resolveMentionTokenKind,
} from "../../common/mention-token";

export function MentionView({ node }: NodeViewProps) {
  const viewerUserId = useAuthStore((s) => s.user?.id ?? null);
  const openAgentPanel = useOpenAgentPanel();
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

  // Member / agent / squad / all → brand-ink prose token. Identity colors stay
  // on avatars (`agentColor`), not the token fill.
  // @all is a fixed protocol token (same as message renderer + markdown
  // `[@all](mention://all/all)`). Picker shows the localized "All members"
  // description; the chip always reads `@all`.
  const displayLabel = type === "all" ? "all" : (label ?? id);
  const kind = resolveMentionTokenKind(type, id, viewerUserId);
  const chip = (
    <span
      className={mentionTokenClassName(kind)}
      data-mention-kind={kind}
      data-mention-type={type}
    >
      @{displayLabel}
    </span>
  );

  if (type === "member" || type === "agent") {
    return (
      <NodeViewWrapper as="span" className="inline">
        <ActorProfileTrigger
          memberType={type === "agent" ? "agent" : "user"}
          memberId={id}
          triggerElement="span"
          onClickCapture={
            type === "agent" && openAgentPanel ? () => openAgentPanel(id) : undefined
          }
        >
          {chip}
        </ActorProfileTrigger>
      </NodeViewWrapper>
    );
  }

  return (
    <NodeViewWrapper as="span" className="inline">
      {chip}
    </NodeViewWrapper>
  );
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
