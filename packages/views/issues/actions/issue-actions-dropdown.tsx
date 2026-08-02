"use client";

import { useState, type ReactElement } from "react";
import type { Issue } from "@multica/core/types";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
} from "@multica/ui/components/ui/dropdown-menu";
import { useIssueActions } from "./use-issue-actions";
import {
  IssueActionsMenuItems,
  dropdownPrimitives,
} from "./issue-actions-menu-items";
import { AssigneePicker } from "../components/pickers";
import {
  PromoteToWikiDialog,
  type PromoteWikiTarget,
} from "../../knowledge";

interface IssueActionsDropdownProps {
  issue: Issue;
  /** A single React element cloned by Base UI as the trigger (via `render` prop). */
  trigger: ReactElement;
  align?: "start" | "end" | "center";
  /** If set, navigate here after the issue is deleted. */
  onDeletedNavigateTo?: string;
  /** Detail-page mobile overflow action for opening the properties sheet. */
  onToggleSidebar?: () => void;
}

export function IssueActionsDropdown({
  issue,
  trigger,
  align = "end",
  onDeletedNavigateTo,
  onToggleSidebar,
}: IssueActionsDropdownProps) {
  const actions = useIssueActions(issue);
  const [assigneeOpen, setAssigneeOpen] = useState(false);
  const [promoteTarget, setPromoteTarget] = useState<PromoteWikiTarget | null>(null);

  // The outer `relative inline-flex` is the picker's anchor box: the
  // absolute, pointer-events-none span inside `triggerRender` fills it, so
  // the popover positions itself relative to the dropdown's 3-dot button
  // without us having to thread a ref through Base UI's anchor API.
  return (
    <span className="relative inline-flex">
      <DropdownMenu>
        <DropdownMenuTrigger render={trigger} />
        <DropdownMenuContent align={align} className="w-auto">
          <IssueActionsMenuItems
            issue={issue}
            actions={actions}
            primitives={dropdownPrimitives}
            onOpenAssignee={() => setAssigneeOpen(true)}
            onDeletedNavigateTo={onDeletedNavigateTo}
            onToggleSidebar={onToggleSidebar}
            onPromoteWiki={setPromoteTarget}
          />
        </DropdownMenuContent>
      </DropdownMenu>
      {/* Mount the picker only once the user actually opens it. Otherwise
          every row in a list/board would subscribe to members/agents/squads
          /frequency queries on mount, multiplying memory + render cost. */}
      {assigneeOpen && (
        <AssigneePicker
          assigneeType={issue.assignee_type}
          assigneeId={issue.assignee_id}
          onUpdate={actions.updateField}
          open={assigneeOpen}
          onOpenChange={setAssigneeOpen}
          triggerRender={
            <span
              aria-hidden
              className="pointer-events-none absolute inset-0"
            />
          }
          trigger={<span />}
          align={align}
        />
      )}
      {promoteTarget && (
        <PromoteToWikiDialog
          open
          onOpenChange={(open) => {
            if (!open) setPromoteTarget(null);
          }}
          targetKind={promoteTarget}
          sourceType="issue"
          sourceId={issue.id}
          initialTitle={issue.title}
          initialContent={issue.description?.trim() || issue.title}
          subjectId={
            promoteTarget === "decision"
              ? (issue.project_id ?? undefined)
              : (issue.channel?.channel_id ?? undefined)
          }
        />
      )}
    </span>
  );
}
