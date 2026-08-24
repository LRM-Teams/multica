// @vitest-environment node

import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const viewsRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");

/**
 * One avatar semantic, site-wide.
 *
 * `views/common/actor-avatar.tsx` is the ONE component product surfaces reach
 * for: it resolves identity (directory + sticky cache + member-profile
 * fallback), applies the glyph/tone rules, and owns the presence dot. Rolling
 * a face by hand — a raw <img>, the shadcn Avatar primitives, or a disc of
 * hand-sliced initials — reintroduces the drift this test exists to stop:
 * "Unknown Agent" on hidden agents, two-letter fake faces (LRM-201), square
 * discs where the rest of the site is round.
 *
 * `apps/mobile` is out of scope by architecture — it cannot import
 * `@multica/ui` at all (Sharing Principles), and owns its own avatar stack.
 */

/**
 * Files allowed to import the dumb visual directly. Each one wraps it in
 * chrome the smart component must not grow a prop for, or already holds a
 * resolved identity and would double-query going through it.
 */
const BASE_AVATAR_ALLOWLIST = new Set([
  // The smart wrapper itself — this IS the composition point.
  "common/actor-avatar.tsx",
  // Wraps the base in edit chrome (camera wash, crop dialog, XP burst).
  "agents/components/agent-profile-avatar-editor.tsx",
  // Already fetched the profile; the smart path would re-query, and this is
  // the hover card so it must not nest another one.
  "common/actor-profile-popover.tsx",
  // Renders its own suppressed/grayscale wrapper and its own presence dot.
  "issues/components/comment-trigger-chips.tsx",
  // Fallback face for an author with no resolvable actor id.
  "channels/components/thread-root-preview.tsx",
  // Fallback face when the research fleet has no director member yet.
  "research/components/research-director-chat-header.tsx",
]);

const BASE_AVATAR_MODULE = "@multica/ui/components/common/actor-avatar";

/** Avatar image primitives — legitimate only inside the base component. */
const FORBIDDEN_PRIMITIVES = ["AvatarImage", "AvatarFallback"];

function sourceFiles(): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      if (entry === "node_modules" || entry === "locales") continue;
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        walk(full);
        continue;
      }
      if (!/\.tsx?$/.test(entry)) continue;
      if (/\.test\.tsx?$/.test(entry)) continue;
      out.push(full);
    }
  };
  walk(viewsRoot);
  return out;
}

describe("one avatar semantic, site-wide", () => {
  const files = sourceFiles();

  it("finds the views tree (guards against a broken walk)", () => {
    expect(files.length).toBeGreaterThan(100);
  });

  it("keeps the dumb base avatar behind a reviewed allowlist", () => {
    const offenders = files
      .filter((f) => readFileSync(f, "utf8").includes(BASE_AVATAR_MODULE))
      .map((f) => relative(viewsRoot, f))
      .filter((rel) => !BASE_AVATAR_ALLOWLIST.has(rel));

    expect(offenders).toEqual([]);
  });

  it("keeps every allowlist entry real (no stale exemptions)", () => {
    const importers = new Set(
      files
        .filter((f) => readFileSync(f, "utf8").includes(BASE_AVATAR_MODULE))
        .map((f) => relative(viewsRoot, f)),
    );
    const stale = [...BASE_AVATAR_ALLOWLIST].filter((rel) => !importers.has(rel));

    expect(stale).toEqual([]);
  });

  it("renders no actor face from the shadcn Avatar image primitives", () => {
    // Symbols, not the import path: AvatarGroup / AvatarGroupCount are layout
    // primitives and stay legitimate around an ActorAvatar.
    const offenders = files
      .filter((f) => {
        const src = readFileSync(f, "utf8");
        return FORBIDDEN_PRIMITIVES.some((symbol) => src.includes(symbol));
      })
      .map((f) => relative(viewsRoot, f));

    expect(offenders).toEqual([]);
  });
});
