/**
 * LRM-1264 R2 — defer katex / remark-math / rehype-katex until markdown
 * actually contains math. Channel shells and most chat bubbles never need
 * the KaTeX runtime; sync-importing it inflated the JS heap on every visit.
 */

import { looksLikeMathMarkdown } from "./math-detect";

export type MathPluginPair = {
  remarkMath: typeof import("remark-math").default;
  rehypeKatex: typeof import("rehype-katex").default;
};

export { looksLikeMathMarkdown };

let mathPluginsPromise: Promise<MathPluginPair> | null = null;

export function loadMathPlugins(): Promise<MathPluginPair> {
  mathPluginsPromise ??= Promise.all([
    import("remark-math"),
    import("rehype-katex"),
    import("./katex-style"),
  ]).then(([remarkMathMod, rehypeKatexMod]) => ({
    remarkMath: remarkMathMod.default,
    rehypeKatex: rehypeKatexMod.default,
  }));
  return mathPluginsPromise;
}
