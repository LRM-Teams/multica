/**
 * Period Work Brief helpers (Slice J3-T4).
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

/** Fixture Agent Brief used in UI/handler confirmation tests. */
export const PERIOD_BRIEF_FIXTURE_MARKDOWN = `# 工作介绍 2026-W33

本周主线是把 Period Work Brief 从底稿合成到可汇报笔记。

## 主线

- 打通 Facts + Digest 派发与 Notes 入口
- Issue/PR 证据挂在主线要点上
- 本机未归类单独成节，不混入团队叙事

## 委派杠杆

- Agent 完成合成草稿；人点确认后落「工作介绍/」子页

## 未完成

- 自定义半开区间仍属可选增强

## 本机未归类

- 若干本地仓改动尚未映射到 Workspace remote
`;
