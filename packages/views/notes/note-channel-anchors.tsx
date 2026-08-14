"use client";

import { Hash } from "lucide-react";
import type { NotePage } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../navigation";
import { useT } from "../i18n/use-t";
import { notePageChannelRefs } from "./note-channel-refs";

/**
 * Compact list of collaboration channels anchored to this note (N2-A3).
 * Empty when there are no accessible channel refs — renders nothing.
 */
export function NoteChannelAnchors({ page }: { page: NotePage }) {
  const { t } = useT("layout");
  const paths = useWorkspacePaths();
  const channels = notePageChannelRefs(page);
  if (channels.length === 0) return null;

  return (
    <div className="mt-3 space-y-1" data-testid="note-channel-anchors">
      <div className="px-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {t(($) => $.notes_page.collaboration_links)}
      </div>
      {channels.map((ref) => {
        const label = ref.label?.trim() || ref.title?.trim() || ref.id;
        return (
          <AppLink
            key={ref.id}
            href={paths.channelDetail(ref.id)}
            className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm text-muted-foreground hover:bg-muted/70 hover:text-foreground"
          >
            <Hash className="size-4 shrink-0" />
            <span className="truncate">{label}</span>
          </AppLink>
        );
      })}
    </div>
  );
}
