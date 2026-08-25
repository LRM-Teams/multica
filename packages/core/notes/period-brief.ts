/**
 * Period Work Brief helpers (ADR 0019 / Slice K2-T1).
 *
 * Detection is intentionally lightweight: product Briefs must read as a
 * reporting narrative with section headings, not a raw commit dump.
 */

/** True when markdown looks like a structured Period Work Brief, not a commit list. */
export function periodBriefLooksStructured(markdown: string): boolean {
  const text = markdown.trim();
  if (!text) return false;
  const headings = text.match(/^#{1,3}\s+\S.+$/gm) ?? [];
  if (headings.length < 2) return false;

  const lines = text.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  const commitish = lines.filter(
    (line) =>
      /^(?:[-*]\s+)?(?:commit\s+)?[0-9a-f]{7,40}\b/i.test(line) ||
      /^(?:[-*]\s+).{0,40}\b[0-9a-f]{7,40}\b/i.test(line),
  );
  if (commitish.length >= 5 && commitish.length * 2 >= lines.length) {
    return false;
  }
  return true;
}

/** True when markdown looks like a collector pack (ADR 0019), not a final Brief. */
export function collectorPackLooksStructured(markdown: string): boolean {
  const text = markdown.trim();
  if (!text) return false;
  if (!/^#\s+采集包(?:\s|$)/m.test(text)) return false;
  const required = [
    "## Runtime",
    "## Repos / roots",
    "## Highlights",
    "## Work groups",
    "## Unscoped / unclear",
  ];
  return required.every((heading) => text.includes(heading));
}

/**
 * Fixture collector pack used when confirming synthesis input → Brief shape.
 * Mirrors the headings collectors are instructed to produce.
 */
export const PERIOD_BRIEF_COLLECTOR_PACK_FIXTURE = `# 采集包 2026-W33

## Runtime
- local / laptop-dev

## Repos / roots
- ~/code/multica — Period Brief collectors + synthesizer path

## Highlights
- wired Facts + collector packs into synthesizer wake (\`<packs>\`)
- sticky \`note_brief\` targets 工作介绍/ folder for \`--note-write\`

## Work groups

### Period Work Brief 采集与合成链路
- why: same repo/project (multica)
- repos/paths: ~/code/multica
- items:
  - wired Facts + collector packs into synthesizer wake
  - sticky note_brief targets 工作介绍/ for --note-write

## Unscoped / unclear
- scratch notes under ~/tmp not mapped to Workspace remotes
`;

/**
 * Deterministic Brief derived from a ready collector-pack fixture.
 * Used in confirm UI / structure tests — not an LLM call.
 */
export function periodBriefFromCollectorPackFixture(
  packMarkdown: string,
  windowLabel = "2026-W33",
): string {
  const pack = packMarkdown.trim();
  const highlightLines = pack
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.startsWith("- ") && !line.includes("local /") && !line.includes("~/"));
  const highlights = highlightLines.length > 0
    ? highlightLines.slice(0, 5).map((line) => line.replace(/^- /, "- "))
    : ["- (no highlights in pack)"];

  const unscopedMatch = pack.match(/## Unscoped \/ unclear\s*([\s\S]*?)(?=\n## |\n# |$)/i);
  const unscopedBody = (unscopedMatch?.[1] ?? "").trim() || "- （无）";

  return `# 工作介绍 ${windowLabel}

本周主线是把 Period Work Brief 从采集包汇合到可汇报笔记。

## 主线

${highlights.join("\n")}

## 委派杠杆

- 采集员 Agent 写出 OS 工作痕迹包；周报 Agent 合成草稿；人点确认后落「工作介绍/」子页

## 未完成

- 自定义半开区间仍属可选增强

## 本机未归类

${unscopedBody}
`;
}

/** Fixture Agent Brief used in UI/handler confirmation tests. */
export const PERIOD_BRIEF_FIXTURE_MARKDOWN = periodBriefFromCollectorPackFixture(
  PERIOD_BRIEF_COLLECTOR_PACK_FIXTURE,
);
