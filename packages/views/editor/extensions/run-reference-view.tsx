import type { NodeViewProps } from "@tiptap/react";
import { NodeViewWrapper } from "@tiptap/react";
import { appendQueryParams, useWorkspacePaths } from "@multica/core/paths";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";
import { cn } from "@multica/ui/lib/utils";

export function RunReferenceView({ node }: NodeViewProps) {
  const { id, label, agentId } = node.attrs;
  const { t } = useT("editor");
  const paths = useWorkspacePaths();
  const { push, openInNewTab } = useNavigation();
  const href = agentId
    ? appendQueryParams(paths.agentDetail(agentId), { run: id })
    : paths.agents();

  return (
    <NodeViewWrapper as="span" className="inline">
      <Tooltip>
        <TooltipTrigger
          render={
            <button
              type="button"
              className={cn(
                "mention inline-flex items-center rounded-md px-1.5 py-0.5 text-[0.9em] font-medium",
                "bg-muted text-foreground hover:bg-muted/80",
              )}
              onClick={(event) => {
                if ((event.metaKey || event.ctrlKey) && openInNewTab) openInNewTab(href);
                else push(href);
              }}
            >
              {label || t(($) => $.run_reference.fallback_label)}
            </button>
          }
        />
        <TooltipContent side="top">{t(($) => $.run_reference.open)}</TooltipContent>
      </Tooltip>
    </NodeViewWrapper>
  );
}
