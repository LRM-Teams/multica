"use client";

import type { ResearchGraphNode } from "@multica/core/types";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n/use-t";

export function ResearchNodeReportModal({
  open,
  node,
  onClose,
}: {
  open: boolean;
  node: ResearchGraphNode | null;
  onClose: () => void;
}) {
  const { t } = useT("research");

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent
        data-testid="research-node-report-modal"
        className="max-h-[min(820px,90vh)] max-w-[min(1040px,94vw)] overflow-hidden"
      >
        <DialogHeader>
          <DialogTitle>{node?.title || t(($) => $.d5.report.title)}</DialogTitle>
        </DialogHeader>
        <div className="max-h-[min(640px,70vh)] space-y-4 overflow-auto text-sm leading-relaxed text-muted-foreground">
          <p>{node?.summary || t(($) => $.d5.report.empty_summary)}</p>
        </div>
      </DialogContent>
    </Dialog>
  );
}
