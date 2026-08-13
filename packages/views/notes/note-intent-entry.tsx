"use client";

import { Bot, FilePlus, Loader2, Sparkles } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../i18n/use-t";

/** Unified note intent routes (S3-A4 / D3). User picks; we never auto-classify. */
export type NoteIntentKind = "editor" | "worker" | "create_issue";

export function NoteIntentEntry({
  creatingIssue,
  onSelect,
}: {
  creatingIssue?: boolean;
  onSelect: (intent: NoteIntentKind) => void;
}) {
  const { t } = useT("layout");

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button variant="outline" size="sm" disabled={creatingIssue} aria-label={t(($) => $.notes_page.intent_entry_aria)} />
        }
      >
        {creatingIssue ? <Loader2 className="size-4 animate-spin" /> : <Sparkles className="size-4" />}
        {t(($) => $.notes_page.intent_entry_action)}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuItem
          onClick={() => onSelect("editor")}
          className="items-start gap-2 py-2"
        >
          <Sparkles className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-medium">{t(($) => $.notes_page.intent_editor_label)}</span>
            <span className="block text-xs text-muted-foreground">{t(($) => $.notes_page.intent_editor_hint)}</span>
          </span>
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => onSelect("worker")}
          className="items-start gap-2 py-2"
        >
          <Bot className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-medium">{t(($) => $.notes_page.intent_worker_label)}</span>
            <span className="block text-xs text-muted-foreground">{t(($) => $.notes_page.intent_worker_hint)}</span>
          </span>
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => onSelect("create_issue")}
          disabled={creatingIssue}
          className="items-start gap-2 py-2"
        >
          <FilePlus className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-medium">{t(($) => $.notes_page.intent_create_issue_label)}</span>
            <span className="block text-xs text-muted-foreground">{t(($) => $.notes_page.intent_create_issue_hint)}</span>
          </span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
