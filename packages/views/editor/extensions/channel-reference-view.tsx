import type { NodeViewProps } from "@tiptap/react";
import { NodeViewWrapper } from "@tiptap/react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "../../navigation";
import { ChannelChip } from "../../channels/components/channel-chip";

// Split out of channel-reference.tsx (react-doctor: only-export-components —
// a file mixing a non-component export (the Node config) with an unexported
// component breaks Fast Refresh). Mirrors issue-reference.tsx's pattern in
// spirit, kept as a component-only file here instead.
export function ChannelReferenceView({ node }: NodeViewProps) {
  const { id, label } = node.attrs;
  const p = useWorkspacePaths();
  const { push, openInNewTab } = useNavigation();
  const channelPath = p.channelDetail(id);

  const handleClick = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.metaKey || e.ctrlKey || e.shiftKey) {
      if (openInNewTab) openInNewTab(channelPath, label);
      return;
    }
    push(channelPath);
  };

  return (
    <NodeViewWrapper as="span" className="inline">
      <a href={channelPath} onClick={handleClick} className="channel-mention inline-flex">
        <ChannelChip name={label ?? id} className="cursor-pointer hover:bg-accent transition-colors" />
      </a>
    </NodeViewWrapper>
  );
}
