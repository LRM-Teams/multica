type DiffLineKind = "same" | "remove" | "add" | "omitted";

export type NoteAIDiffLine = {
  kind: DiffLineKind;
  text: string;
  oldLine: number | null;
  newLine: number | null;
  omittedLineCount?: number;
};

export type NoteAIDiffOptions = {
  compact?: boolean;
  contextLines?: number;
  maxCompareCells?: number;
  maxChangedLinesPerSide?: number;
};

const DEFAULT_CONTEXT_LINES = 3;
const DEFAULT_MAX_COMPARE_CELLS = 160_000;
const DEFAULT_MAX_CHANGED_LINES_PER_SIDE = 120;

function splitMarkdownLines(value: string) {
  const normalized = value.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  return normalized.length ? normalized.split("\n") : [];
}

function omittedLine(count: number): NoteAIDiffLine | null {
  if (count <= 0) return null;
  return { kind: "omitted", text: "", oldLine: null, newLine: null, omittedLineCount: count };
}

function pushOmitted(lines: NoteAIDiffLine[], count: number) {
  const line = omittedLine(count);
  if (line) lines.push(line);
}

function buildFullLineDiff(oldLines: string[], newLines: string[]): NoteAIDiffLine[] {
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

function compactSameRuns(lines: NoteAIDiffLine[], contextLines: number) {
  const compacted: NoteAIDiffLine[] = [];
  for (let i = 0; i < lines.length;) {
    if (lines[i]?.kind !== "same") {
      compacted.push(lines[i]!);
      i++;
      continue;
    }
    const start = i;
    while (i < lines.length && lines[i]?.kind === "same") i++;
    const run = lines.slice(start, i);
    const isEdgeRun = start === 0 || i === lines.length;
    const visibleAtEdge = contextLines;
    const visibleInside = contextLines * 2;
    const maxVisible = isEdgeRun ? visibleAtEdge : visibleInside;
    if (run.length <= maxVisible) {
      compacted.push(...run);
      continue;
    }
    if (start !== 0) compacted.push(...run.slice(0, contextLines));
    const tailCount = i === lines.length ? 0 : contextLines;
    const hidden = run.length - (start !== 0 ? contextLines : 0) - tailCount;
    pushOmitted(compacted, hidden);
    if (tailCount > 0) compacted.push(...run.slice(-tailCount));
  }
  return compacted;
}

function pushCappedChangedLines(
  out: NoteAIDiffLine[],
  kind: "remove" | "add",
  source: string[],
  start: number,
  end: number,
  startLineNumber: number,
  maxLines: number,
) {
  const count = Math.max(0, end - start);
  if (count <= maxLines) {
    for (let i = start; i < end; i++) {
      out.push({
        kind,
        text: source[i]!,
        oldLine: kind === "remove" ? startLineNumber + (i - start) : null,
        newLine: kind === "add" ? startLineNumber + (i - start) : null,
      });
    }
    return;
  }

  const head = Math.max(1, Math.floor(maxLines / 2));
  const tail = Math.max(1, maxLines - head);
  for (let i = start; i < start + head; i++) {
    out.push({
      kind,
      text: source[i]!,
      oldLine: kind === "remove" ? startLineNumber + (i - start) : null,
      newLine: kind === "add" ? startLineNumber + (i - start) : null,
    });
  }
  pushOmitted(out, count - head - tail);
  for (let i = end - tail; i < end; i++) {
    out.push({
      kind,
      text: source[i]!,
      oldLine: kind === "remove" ? startLineNumber + (i - start) : null,
      newLine: kind === "add" ? startLineNumber + (i - start) : null,
    });
  }
}

function buildAnchoredLargeDiff(
  oldLines: string[],
  newLines: string[],
  contextLines: number,
  maxChangedLinesPerSide: number,
) {
  let prefix = 0;
  while (prefix < oldLines.length && prefix < newLines.length && oldLines[prefix] === newLines[prefix]) {
    prefix++;
  }
  if (prefix === oldLines.length && prefix === newLines.length) return [];

  let suffix = 0;
  while (
    suffix < oldLines.length - prefix &&
    suffix < newLines.length - prefix &&
    oldLines[oldLines.length - 1 - suffix] === newLines[newLines.length - 1 - suffix]
  ) {
    suffix++;
  }

  const out: NoteAIDiffLine[] = [];
  const prefixStart = Math.max(0, prefix - contextLines);
  pushOmitted(out, prefixStart);
  for (let i = prefixStart; i < prefix; i++) {
    out.push({ kind: "same", text: oldLines[i]!, oldLine: i + 1, newLine: i + 1 });
  }

  const oldChangeEnd = oldLines.length - suffix;
  const newChangeEnd = newLines.length - suffix;
  pushCappedChangedLines(out, "remove", oldLines, prefix, oldChangeEnd, prefix + 1, maxChangedLinesPerSide);
  pushCappedChangedLines(out, "add", newLines, prefix, newChangeEnd, prefix + 1, maxChangedLinesPerSide);

  const suffixVisible = Math.min(contextLines, suffix);
  for (let offset = 0; offset < suffixVisible; offset++) {
    const oldIndex = oldChangeEnd + offset;
    const newIndex = newChangeEnd + offset;
    out.push({ kind: "same", text: oldLines[oldIndex]!, oldLine: oldIndex + 1, newLine: newIndex + 1 });
  }
  pushOmitted(out, suffix - suffixVisible);
  return out;
}

export function buildNoteAILineDiff(before: string, after: string, options: NoteAIDiffOptions = {}): NoteAIDiffLine[] {
  const oldLines = splitMarkdownLines(before);
  const newLines = splitMarkdownLines(after);
  if (!options.compact) return buildFullLineDiff(oldLines, newLines);

  const contextLines = options.contextLines ?? DEFAULT_CONTEXT_LINES;
  const maxCompareCells = options.maxCompareCells ?? DEFAULT_MAX_COMPARE_CELLS;
  const maxChangedLinesPerSide = options.maxChangedLinesPerSide ?? DEFAULT_MAX_CHANGED_LINES_PER_SIDE;
  const compareCells = oldLines.length * newLines.length;

  if (compareCells > maxCompareCells) {
    return buildAnchoredLargeDiff(oldLines, newLines, contextLines, maxChangedLinesPerSide);
  }
  return compactSameRuns(buildFullLineDiff(oldLines, newLines), contextLines);
}
