import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchStageTimeline } from "./research-stage-timeline";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        stage: {
          s1_plan: "S1 · Plan",
          s2_sources: "S2 · Explore",
          s3_validation: "S3 · Validate",
          s4_delivery: "S4 · Deliver",
        },
        stage_short: {
          s1_plan: "S1",
          s2_sources: "S2",
          s3_validation: "S3",
          s4_delivery: "S4",
        },
        timeline: {
          label: "Research stages",
          done: "Done",
          current: "Current",
          upcoming: "Upcoming",
          done_feedback: "Stage completed",
        },
      }),
  }),
}));

const here = path.dirname(fileURLToPath(import.meta.url));
const componentSrc = fs.readFileSync(
  path.resolve(here, "research-stage-timeline.tsx"),
  "utf8",
);
const tokensSrc = fs.readFileSync(
  path.resolve(here, "../../../ui/styles/tokens.css"),
  "utf8",
);
const baseCssSrc = fs.readFileSync(
  path.resolve(here, "../../../ui/styles/base.css"),
  "utf8",
);

describe("ResearchStageTimeline", () => {
  it("renders the full stage sequence and highlights the current stage", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    expect(screen.getByLabelText("Research stages")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /S1 · Plan/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /S2 · Explore/i })).toHaveAttribute(
      "aria-current",
      "step",
    );
    expect(screen.getByRole("button", { name: /S4 · Deliver/i })).toBeDisabled();
    expect(container.querySelector('[data-stage-state="current"]')).toBeTruthy();
    expect(container.querySelectorAll('[data-stage-state="done"]').length).toBe(1);
    expect(container.querySelectorAll('[data-stage-state="upcoming"]').length).toBe(2);
  });

  it("invokes onSelectStage for done/current steps only", () => {
    const onSelectStage = vi.fn();
    render(
      <ResearchStageTimeline
        currentStage="s2_sources"
        sessionStatus="running"
        onSelectStage={onSelectStage}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /S1 · Plan/i }));
    fireEvent.click(screen.getByRole("button", { name: /S2 · Explore/i }));
    fireEvent.click(screen.getByRole("button", { name: /S3 · Validate/i }));
    expect(onSelectStage).toHaveBeenCalledWith("s1_plan");
    expect(onSelectStage).toHaveBeenCalledWith("s2_sources");
    expect(onSelectStage).toHaveBeenCalledTimes(2);
  });

  it("marks all stages done when session completed", () => {
    render(
      <ResearchStageTimeline
        currentStage="s4_delivery"
        sessionStatus="completed"
        onSelectStage={vi.fn()}
      />,
    );
    const s1 = screen.getByRole("button", { name: /S1 · Plan/i });
    expect(s1).not.toBeDisabled();
    expect(screen.getAllByText("Stage completed").length).toBe(4);
  });
});

/**
 * LRM-1252 — upcoming 阶段名曾是 `opacity-75` × `text-muted-foreground/80`
 * → 有效 alpha 0.60，亮色实测 ≈2.6:1（WCAG AA 4.5 FAIL）。
 * 弱化层级只允许靠字号/字重/等宽/glyph，不允许靠 alpha 压文字。
 *
 * LRM-1291 更新：原先两条「装饰不变」断言锚在灰阶 stepper 的具体类名
 * (`.bg-border/80` 连线、`[aria-hidden].opacity-70` upcoming glyph)。能量轨把
 * 连线换成 9px 色带、把 upcoming glyph 换成主题色描边，这两个类名已不存在。
 * 断言改锚到新的装饰实现，**文字禁 alpha 的原始意图逐条保留**。
 */
describe("ResearchStageTimeline text contrast (LRM-1252)", () => {
  it("keeps upcoming rows free of opacity-* and renders a solid muted label", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const upcoming = [...container.querySelectorAll('[data-stage-state="upcoming"]')];
    expect(upcoming.length).toBe(2);
    for (const li of upcoming) {
      expect(li.className).not.toMatch(/\bopacity-\d/);
      const label = li.querySelector("span.font-mono.md\\:block");
      expect(label).not.toBeNull();
      expect(label!.className).toContain("text-muted-foreground");
      expect(label!.className).not.toMatch(/text-muted-foreground\/\d/);
    }
  });

  it("keeps the three step states visually distinct without alpha on the label", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const labelOf = (state: string) =>
      container.querySelector(
        `[data-stage-state="${state}"] span.truncate.md\\:block`,
      )?.className ?? "";
    expect(labelOf("current")).toContain("font-medium");
    expect(labelOf("current")).toContain("text-foreground");
    expect(labelOf("done")).toContain("font-mono");
    expect(labelOf("done")).toContain("text-foreground");
    expect(labelOf("upcoming")).toContain("font-mono");
    expect(labelOf("upcoming")).toContain("text-muted-foreground");
    expect(labelOf("upcoming")).not.toBe(labelOf("done"));
    // 装饰仍可用 alpha / 图案：色带三态各有独立实现，glyph 仍 aria-hidden。
    expect(container.querySelectorAll("[data-stage-band]").length).toBe(4);
    expect(container.querySelector('[data-stage-band="upcoming"]')).toBeTruthy();
    expect(container.querySelector("[data-stage-current-ring]")).toHaveAttribute(
      "aria-hidden",
    );
  });

  it("guard: no text-bearing node uses alpha-dimmed muted text or an opacity ancestor", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const offenders: string[] = [];
    for (const el of container.querySelectorAll<HTMLElement>("*")) {
      const ownText = [...el.childNodes]
        .filter((n) => n.nodeType === 3)
        .map((n) => n.textContent ?? "")
        .join("")
        .trim();
      if (!ownText) continue;
      if (el.closest('[aria-hidden="true"]')) continue;
      const chain: string[] = [];
      let cur: HTMLElement | null = el;
      while (cur && cur !== container) {
        chain.push(cur.className || "");
        cur = cur.parentElement;
      }
      const classes = chain.join(" ");
      if (/text-muted-foreground\/[5-8]\d/.test(classes)) {
        offenders.push(`${ownText}: dimmed muted text (${classes})`);
      }
      if (/\bopacity-\d/.test(classes)) {
        offenders.push(`${ownText}: opacity ancestor (${classes})`);
      }
    }
    expect(offenders).toEqual([]);
  });
});

/**
 * LRM-1291 — 详情顶栏阶段能量轨（跟 LRM-1271 冻稿）。
 *
 * jsdom 不解析 token、不合成 color-mix、不跑动画，所以这里只钉 **结构与源码级**
 * 契约（三态形状/文字冗余、色带连续性、单点动效、token 而非裸 hex、reduced-motion
 * 有降级块、亮色 token 数值达标）。像素与运行时对比度由真 Chromium 门补。
 */
describe("ResearchStageTimeline energy track (LRM-1291)", () => {
  it("renders one continuous band: four segments, only the outer ends rounded", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const bands = [...container.querySelectorAll<HTMLElement>("[data-stage-band]")];
    expect(bands.length).toBe(4);
    for (const b of bands) expect(b.className).toContain("h-[9px]");
    expect(bands[0]!.className).toContain("rounded-l-full");
    expect(bands[0]!.className).not.toContain("rounded-r-full");
    expect(bands[3]!.className).toContain("rounded-r-full");
    expect(bands[3]!.className).not.toContain("rounded-l-full");
    expect(bands[1]!.className).not.toMatch(/rounded-[lr]-full/);
    expect(bands[2]!.className).not.toMatch(/rounded-[lr]-full/);
    // 段与段之间无 gap，四段等分 → 轨道连续且不溢出
    expect(container.querySelector("ol")!.className).toContain("gap-0");
    for (const li of container.querySelectorAll("li")) {
      expect(li.className).toContain("flex-1");
      expect(li.className).toContain("min-w-0");
    }
  });

  it("animates the current segment only — no marquee across the whole track", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const sheens = container.querySelectorAll("[data-stage-sheen]");
    expect(sheens.length).toBe(1);
    expect(sheens[0]!.className).toContain("animate-research-stage-sheen");
    expect(
      container.querySelector('[data-stage-state="current"] [data-stage-sheen]'),
    ).toBeTruthy();
  });

  it("has no moving part at all once every stage is done", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s4_delivery" sessionStatus="completed" />,
    );
    expect(container.querySelectorAll("[data-stage-sheen]").length).toBe(0);
    expect(container.querySelectorAll('[data-stage-band="done"]').length).toBe(4);
  });

  it("carries state by shape and text, not by hue alone", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    // 形状冗余：done=实心✓ · current=28px 环+中心点 · upcoming=空心描边
    expect(
      container.querySelector('[data-stage-state="done"] svg.lucide-check'),
    ).toBeTruthy();
    const ring = container.querySelector<HTMLElement>("[data-stage-current-ring]");
    expect(ring).not.toBeNull();
    expect(ring!.className).toContain("size-7");
    expect(ring!.className).toContain("ring-background");
    // 图案冗余：upcoming 是斜纹，不是同色淡版
    const upcomingBand = container.querySelector<HTMLElement>(
      '[data-stage-band="upcoming"]',
    );
    expect(upcomingBand!.className).toContain("repeating-linear-gradient");
    // 文字冗余：三态都自报状态
    const stateTexts = [
      ...container.querySelectorAll("[data-stage-state-text]"),
    ].map((n) => n.textContent);
    expect(stateTexts).toEqual(["Done", "Current", "Upcoming", "Upcoming"]);
  });

  it("keeps the full stage name in the accessible name while showing S1–S4 narrow", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    const first = container.querySelector<HTMLElement>('[data-stage="s1_plan"]')!;
    const short = first.querySelector<HTMLElement>("span.md\\:hidden")!;
    // 短名来自 locale `stage_short`，不是把完整名截断推导出来的
    expect(short.textContent).toBe("S1");
    expect(short.getAttribute("aria-hidden")).toBe("true");
    const full = first.querySelector<HTMLElement>("span.truncate.md\\:block")!;
    expect(full.textContent).toBe("S1 · Plan");
    expect(full.className).toContain("hidden");
    // 状态文字不能只留在 aria-label 或 tooltip：360–767 也始终实际渲染。
    const stateText = first.querySelector<HTMLElement>("[data-stage-state-text]")!;
    expect(stateText.textContent).toBe("Done");
    expect(stateText.className).toContain("block");
    expect(stateText.className).not.toMatch(/\bhidden\b|md:block/);
    // 无障碍名在任何宽度都是完整名 + 状态
    expect(
      screen.getByRole("button", { name: "S1 · Plan — Done" }),
    ).toBeInTheDocument();
  });

  it("uses only design-system text sizes and weights", () => {
    expect(componentSrc).not.toMatch(/\bfont-(?:semibold|bold)\b/);
    expect(componentSrc).not.toMatch(/\btext-\[\d+(?:\.\d+)?px\]/);
  });

  it("preserves nav / button / aria-current and the existing click boundary", () => {
    const { container } = render(
      <ResearchStageTimeline currentStage="s2_sources" sessionStatus="running" />,
    );
    expect(container.querySelector("nav")).toBeTruthy();
    expect(container.querySelectorAll("button").length).toBe(4);
    expect(container.querySelectorAll('[aria-current="step"]').length).toBe(1);
    expect(
      container.querySelector('[data-stage-state="upcoming"] button'),
    ).toBeDisabled();
  });

  it("resolves every stage color through --research-stage-* tokens, never JSX hex", () => {
    expect(componentSrc).not.toMatch(/#[0-9a-fA-F]{6}\b/);
    for (const token of [
      "--research-stage-define",
      "--research-stage-explore",
      "--research-stage-explore-2",
      "--research-stage-verify",
      "--research-stage-deliver",
    ]) {
      expect(componentSrc).toContain(`var(${token})`);
    }
  });

  /**
   * Regression from the first gate-shot run: define/verify/deliver had
   * `from === to`, so the CURRENT band painted as one flat color and was
   * indistinguishable from a `done` segment — while a naive
   * "backgroundImage !== none" assertion still went green. The two stops must
   * be different tokens for every stage.
   *
   * Parsed from source rather than imported: exporting the map from a component
   * file trips React Doctor's `only-export-components` (Fast Refresh can't
   * preserve state), and the harness contract here is the literal pairing.
   */
  it("gives every current segment two different hues", () => {
    const block = componentSrc.match(/const STAGE_HUES[^{]*\{([\s\S]*?)\n\};/)?.[1];
    expect(block, "STAGE_HUES literal found").toBeTruthy();
    const pairs = [
      ...block!.matchAll(
        /(\w+):\s*\{\s*from:\s*"(var\(--research-stage-[\w-]+\))",\s*to:\s*"(var\(--research-stage-[\w-]+\))",\s*\}/g,
      ),
    ].map((m) => ({ stage: m[1]!, from: m[2]!, to: m[3]! }));

    expect(pairs.map((p) => p.stage)).toEqual([
      "s1_plan",
      "s2_sources",
      "s3_validation",
      "s4_delivery",
    ]);
    for (const p of pairs) {
      expect(p.to, `${p.stage} needs a second hue`).not.toBe(p.from);
    }
    // Explore stays pinned to the frozen violet → fuchsia pair.
    expect(pairs.find((p) => p.stage === "s2_sources")).toEqual({
      stage: "s2_sources",
      from: "var(--research-stage-explore)",
      to: "var(--research-stage-explore-2)",
    });
  });
});

/**
 * Token + stylesheet contract for the energy track. Kept source-level on
 * purpose: `tokens.css` values and the reduced-motion override never reach
 * jsdom, so a DOM-only test would pass with the tokens deleted.
 */
describe("research stage tokens (LRM-1291)", () => {
  const STAGE_TOKENS = [
    "research-stage-define",
    "research-stage-explore",
    "research-stage-explore-2",
    "research-stage-verify",
    "research-stage-deliver",
  ] as const;

  function blockFor(selector: "light" | "dark"): string {
    const opener = selector === "light" ? ":root,\n.light {" : ".dark {";
    const start = tokensSrc.indexOf(opener);
    expect(start, `${selector} block present`).toBeGreaterThanOrEqual(0);
    const end = tokensSrc.indexOf("\n}", start);
    expect(end, `${selector} block terminated`).toBeGreaterThan(start);
    return tokensSrc.slice(start, end);
  }

  function declared(block: string): Map<string, string> {
    const out = new Map<string, string>();
    for (const m of block.matchAll(/--(research-stage-[\w-]+)\s*:\s*([^;]+);/g)) {
      out.set(m[1]!, m[2]!.trim());
    }
    return out;
  }

  function srgbToLinear(channel: number): number {
    const c = channel / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }

  function contrast(hex: string, otherHex: string): number {
    const lum = (h: string) => {
      const v = h.replace("#", "");
      const [r, g, b] = [0, 2, 4].map((i) =>
        srgbToLinear(Number.parseInt(v.slice(i, i + 2), 16)),
      ) as [number, number, number];
      return 0.2126 * r + 0.7152 * g + 0.0722 * b;
    };
    const a = lum(hex);
    const b = lum(otherHex);
    return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
  }

  it("declares all five stage hues in both light and dark", () => {
    const light = declared(blockFor("light"));
    const dark = declared(blockFor("dark"));
    for (const token of STAGE_TOKENS) {
      expect(light.get(token), `${token} light`).toBeTruthy();
      expect(dark.get(token), `${token} dark`).toBeTruthy();
    }
    // 暗色不得照抄亮色（LRM-1208 的原始教训：SVG/gradient 位置吃不到 `dark:`）
    for (const token of STAGE_TOKENS) {
      expect(dark.get(token)).not.toBe(light.get(token));
    }
  });

  it("light stage hues clear the 4.5:1 text floor on --surface", () => {
    const light = declared(blockFor("light"));
    for (const token of STAGE_TOKENS) {
      const value = light.get(token)!;
      expect(value, `${token} is a bare hex in light`).toMatch(/^#[0-9a-fA-F]{6}$/);
      expect(contrast(value, "#ffffff"), `${token} on --surface`).toBeGreaterThanOrEqual(
        4.5,
      );
    }
  });

  it("keeps the sheen to 2.4s and drops it under prefers-reduced-motion", () => {
    expect(baseCssSrc).toContain("@keyframes research-stage-sheen");
    expect(baseCssSrc).toMatch(/animation:\s*research-stage-sheen\s+2\.4s\s+linear/);
    const reducedBlocks = baseCssSrc.split("@media (prefers-reduced-motion: reduce)");
    const guarded = reducedBlocks
      .slice(1)
      .some((b) => /\.animate-research-stage-sheen\s*{[^}]*animation:\s*none/.test(b));
    expect(guarded, "sheen has a reduced-motion override").toBe(true);
  });
});
