"use client";

import { lazy, Suspense, type ComponentType, type ReactNode } from "react";
import { useModalStore } from "@multica/core/modals";

/**
 * LRM-1263 — modal implementations stay out of the dashboard shell chunk.
 * Only the open modal's module is fetched; closed state ships no modal UI graph.
 */
function lazyModal<T extends ComponentType<any>>(
  loader: () => Promise<Record<string, unknown>>,
  exportName: string,
) {
  return lazy(() =>
    loader().then((mod) => {
      const Comp = mod[exportName];
      if (typeof Comp !== "function") {
        throw new Error(
          `ModalRegistry: "${exportName}" missing — no silent whole-bundle fallback`,
        );
      }
      return { default: Comp as T };
    }),
  );
}

const CreateWorkspaceModal = lazyModal(
  () => import("./create-workspace"),
  "CreateWorkspaceModal",
);
const CreateIssueDialog = lazyModal(
  () => import("./create-issue-dialog"),
  "CreateIssueDialog",
);
const CreateProjectModal = lazyModal(
  () => import("./create-project"),
  "CreateProjectModal",
);
const FeedbackModal = lazyModal(() => import("./feedback"), "FeedbackModal");
const SetParentIssueModal = lazyModal(
  () => import("./set-parent-issue"),
  "SetParentIssueModal",
);
const AddChildIssueModal = lazyModal(
  () => import("./add-child-issue"),
  "AddChildIssueModal",
);
const DeleteIssueConfirmModal = lazyModal(
  () => import("./delete-issue-confirm"),
  "DeleteIssueConfirmModal",
);
const BacklogAgentHintModal = lazyModal(
  () => import("./backlog-agent-hint"),
  "BacklogAgentHintModal",
);

export function ModalRegistry() {
  const modal = useModalStore((s) => s.modal);
  const data = useModalStore((s) => s.data);
  const close = useModalStore((s) => s.close);

  if (!modal) return null;

  let body: ReactNode = null;
  switch (modal) {
    case "create-workspace":
      body = <CreateWorkspaceModal onClose={close} />;
      break;
    // Both modal types open the same shell so the in-modal mode switch is
    // instant — only the inner panel swaps, the Dialog Root stays mounted.
    case "create-issue":
      body = (
        <CreateIssueDialog onClose={close} initialMode="manual" data={data} />
      );
      break;
    case "quick-create-issue":
      body = (
        <CreateIssueDialog onClose={close} initialMode="agent" data={data} />
      );
      break;
    case "create-project":
      body = <CreateProjectModal onClose={close} />;
      break;
    case "feedback":
      body = <FeedbackModal onClose={close} data={data} />;
      break;
    case "issue-set-parent":
      body = <SetParentIssueModal onClose={close} data={data} />;
      break;
    case "issue-add-child":
      body = <AddChildIssueModal onClose={close} data={data} />;
      break;
    case "issue-delete-confirm":
      body = <DeleteIssueConfirmModal onClose={close} data={data} />;
      break;
    case "issue-backlog-agent-hint":
      body = <BacklogAgentHintModal onClose={close} data={data} />;
      break;
    default:
      return null;
  }

  return <Suspense fallback={null}>{body}</Suspense>;
}
