"use client";

import { cn } from "@multica/ui/lib/utils";

type DiffLineKind = "same" | "remove" | "add";

export type NoteAIDiffLine = {
  kind: DiffLineKind;
  text: string;
  oldLine: number | null;
  newLine: number | null;
};

function splitMarkdownLines(value: string) {
  const normalized = value.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  return normalized.length ? normalized.split("\n") : [];
}

export function buildNoteAILineDiff(before: string, after: string): NoteAIDiffLine[] {
  const oldLines = splitMarkdownLines(before);
  const newLines = splitMarkdownLines(after);
  const dp = Array.from({ length: oldLines.length + 1 }, () => Array<number>(newLines.length + 1).fill(0));

  for (let i = oldLines.length - 1; i >= 0; i--) {
    for (let j = newLines.length - 1; j >= 0; j--) {
      dp[i]![j] = oldLines[i]! === newLines[j]!
        ? dp[i + 1]![j + 1]! + 1
        : Math.max(dp[i + 1]![j]!, dp[i]![j + 1]!);
    }
  }

  const lines: NoteAIDiffLine[] = [];
  let i = 0;
  let j = 0;
  let oldNumber = 1;
  let newNumber = 1;
  while (i < oldLines.length || j < newLines.length) {
    if (i < oldLines.length && j < newLines.length && oldLines[i]! === newLines[j]!) {
      lines.push({ kind: "same", text: oldLines[i]!, oldLine: oldNumber++, newLine: newNumber++ });
      i++;
      j++;
      continue;
    }
    if (j < newLines.length && (i === oldLines.length || dp[i]![j + 1]! > dp[i + 1]![j]!)) {
      lines.push({ kind: "add", text: newLines[j]!, oldLine: null, newLine: newNumber++ });
      j++;
      continue;
    }
    lines.push({ kind: "remove", text: oldLines[i]!, oldLine: oldNumber++, newLine: null });
    i++;
  }
  return lines;
}

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
