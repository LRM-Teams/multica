"use client";

import { cn } from "@multica/ui/lib/utils";
import { buildNoteAILineDiff } from "./note-ai-diff-utils";

type DiffLineKind = "same" | "remove" | "add";

function lineMarker(kind: DiffLineKind) {
  if (kind === "add") return "+";
  if (kind === "remove") return "-";
  return " ";
}

export function NoteAIDiffPreview({
  before,
  after,
  beforeLabel,
  afterLabel,
  className,
}: {
  before: string;
  after: string;
  beforeLabel: string;
  afterLabel: string;
  className?: string;
}) {
  const lines = buildNoteAILineDiff(before, after);
  const changed = lines.some((line) => line.kind !== "same");
  return (
    <div className={cn("overflow-hidden rounded-lg border bg-muted/30", className)} data-testid="note-ai-diff-preview">
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        <span className="inline-flex items-center gap-1"><span className="size-2 rounded-full bg-red-500/70" />{beforeLabel}</span>
        <span className="text-muted-foreground/50">{"->"}</span>
        <span className="inline-flex items-center gap-1"><span className="size-2 rounded-full bg-emerald-500/70" />{afterLabel}</span>
      </div>
      <div className="max-h-72 overflow-y-auto font-mono text-xs leading-5">
        {changed ? lines.map((line, index) => (
          <div
            key={`${line.kind}:${line.oldLine ?? ""}:${line.newLine ?? ""}:${index}`}
            className={cn(
              "grid grid-cols-[3rem_3rem_1.5rem_minmax(0,1fr)] border-b border-transparent px-2",
              line.kind === "add" && "bg-emerald-500/10 text-emerald-950 dark:text-emerald-100",
              line.kind === "remove" && "bg-red-500/10 text-red-950 dark:text-red-100",
              line.kind === "same" && "text-muted-foreground",
            )}
          >
            <span className="select-none text-right text-muted-foreground/60">{line.oldLine ?? ""}</span>
            <span className="select-none text-right text-muted-foreground/60">{line.newLine ?? ""}</span>
            <span className="select-none text-center text-muted-foreground/70">{lineMarker(line.kind)}</span>
            <span className="whitespace-pre-wrap break-words">{line.text || " "}</span>
          </div>
        )) : (
          <div className="px-3 py-4 text-sm text-muted-foreground">No line changes.</div>
        )}
      </div>
    </div>
  );
}
