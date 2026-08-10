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
