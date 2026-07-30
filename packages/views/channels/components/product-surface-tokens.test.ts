// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * LRM-227 — light-mode product-surface freeze must stay pinned in tokens.css
 * so chat / members / settings cannot drift back to a second hex palette.
 */
describe("product surface tokens (LRM-226/227)", () => {
  const tokensPath = resolve(
    dirname(fileURLToPath(import.meta.url)),
    "../../../ui/styles/tokens.css",
  );
  const css = readFileSync(tokensPath, "utf8");

  it("pins the frozen light palette values", () => {
    expect(css).toMatch(/--ink:\s*#1d1c1d/);
    expect(css).toMatch(/--ink-2:\s*#616061/);
    expect(css).toMatch(/--ink-3:\s*#868686/);
    expect(css).toMatch(/--line:\s*#e8e8e8/);
    expect(css).toMatch(/--line-strong:\s*#d1d1d1/);
    expect(css).toMatch(/--hover:\s*#f8f8f8/);
    expect(css).toMatch(/--brand:\s*#1264a3/);
    expect(css).toMatch(/--brand-soft:\s*#e8f5fa/);
    expect(css).toMatch(/--ok:\s*#007a5a/);
    expect(css).toMatch(/--danger:\s*#e01e5a/);
    expect(css).toMatch(/--online:\s*#2bac76/);
    expect(css).toMatch(/--busy:\s*#e8912d/);
    expect(css).toMatch(/--page-bg:\s*#f6f6f4/);
  });

  it("exposes design aliases on the Tailwind theme", () => {
    expect(css).toMatch(/--color-ink:\s*var\(--ink\)/);
    expect(css).toMatch(/--color-ink-2:\s*var\(--ink-2\)/);
    expect(css).toMatch(/--color-ink-3:\s*var\(--ink-3\)/);
    expect(css).toMatch(/--color-brand-soft:\s*var\(--brand-soft\)/);
    expect(css).toMatch(/--color-hover:\s*var\(--hover\)/);
    expect(css).toMatch(/--color-online:\s*var\(--online\)/);
  });

  it("maps semantic light tokens onto the freeze (no purple dual track)", () => {
    // Prefer the shared `:root, .light` block; fall back to bare `:root`.
    const rootStart = Math.max(
      css.search(/:root\s*,\s*\n?\s*\.light\s*\{/),
      css.indexOf(":root"),
    );
    const darkStart = css.search(/\n\.dark\s*\{/);
    const root = css.slice(rootStart, darkStart);
    expect(root).toMatch(/--foreground:\s*var\(--ink\)/);
    expect(root).toMatch(/--muted-foreground:\s*var\(--ink-2\)/);
    expect(root).toMatch(/--border:\s*var\(--line\)/);
    expect(root).toMatch(/--input:\s*var\(--line-strong\)/);
    expect(root).toMatch(/--destructive:\s*var\(--danger\)/);
    expect(root).toMatch(/--success:\s*var\(--ok\)/);
    expect(root).not.toMatch(/oklch\(0\.55 0\.16 255\)/);
  });

  it("shares light tokens with .light for nested theme previews (LRM-355)", () => {
    expect(css).toMatch(/:root\s*,\s*\n\.light\s*\{/);
  });
});
