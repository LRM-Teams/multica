// @vitest-environment node
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * LRM-1276 — `-webkit-overflow-scrolling: touch` was hand-copied as a Tailwind
 * arbitrary property in six places across five files, so it had no single
 * source. That blocked two things: new scroll containers had no reusable name
 * (they were copied by grep, which is why adoption was only partial), and the
 * eventual removal of this deprecated iOS prefix would have to touch six sites.
 *
 * This is a declaration move only — it does not change scroll behaviour and is
 * not a fix for the mobile scroll defect tracked in LRM-1222.
 */

const viewsRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const baseCssPath = resolve(viewsRoot, "../ui/styles/base.css");

/** Every call site that carried the hand-copied declaration on dev@2c0a34811. */
const CALL_SITES = [
  "channels/components/channel-add-people-dialog.tsx",
  "channels/components/channel-members-list.tsx",
  "channels/components/composer-attachment-tray.tsx",
  "issues/components/issues-header.tsx",
  "my-issues/components/my-issues-header.tsx",
] as const;

const RAW_DECLARATION = "[-webkit-overflow-scrolling:touch]";

const SKIP_DIRECTORIES = new Set(["node_modules", "dist", ".turbo", "coverage"]);

function collectSourceFiles(directory: string, found: string[] = []): string[] {
  for (const entry of readdirSync(directory)) {
    const absolute = join(directory, entry);

    if (statSync(absolute).isDirectory()) {
      if (!SKIP_DIRECTORIES.has(entry)) collectSourceFiles(absolute, found);
      continue;
    }

    if (/\.(ts|tsx|css)$/.test(entry) && !/\.test\.(ts|tsx)$/.test(entry)) {
      found.push(absolute);
    }
  }

  return found;
}

describe("shared momentum-scroll token (LRM-1276)", () => {
  const baseCss = readFileSync(baseCssPath, "utf8");

  it("defines exactly one .momentum-scroll rule in the shared base stylesheet", () => {
    const definitions = baseCss.match(/^\.momentum-scroll\s*\{/gm) ?? [];

    expect(definitions).toHaveLength(1);
    expect(baseCss).toMatch(
      /\.momentum-scroll\s*\{\s*-webkit-overflow-scrolling:\s*touch;\s*\}/,
    );
  });

  it("keeps the reason the deprecated prefix is retained next to the rule", () => {
    const ruleIndex = baseCss.search(/^\.momentum-scroll\s*\{/m);
    const before = baseCss.slice(0, ruleIndex);
    const comment = before.slice(before.lastIndexOf("/*"));

    expect(comment).toMatch(/LRM-1276/);
    expect(comment).toMatch(/iOS/);
  });

  it("has no hand-copied arbitrary property left anywhere in packages/views", () => {
    const offenders = collectSourceFiles(viewsRoot).filter((file) =>
      readFileSync(file, "utf8").includes(RAW_DECLARATION),
    );

    expect(offenders.map((file) => file.slice(viewsRoot.length + 1))).toEqual(
      [],
    );
  });

  it("routes every known call site through the shared class", () => {
    for (const relative of CALL_SITES) {
      const source = readFileSync(resolve(viewsRoot, relative), "utf8");

      expect(source, relative).toContain("momentum-scroll");
    }
  });

  it("leaves the surrounding scroll declarations byte-for-byte intact", () => {
    const addPeople = readFileSync(
      resolve(viewsRoot, "channels/components/channel-add-people-dialog.tsx"),
      "utf8",
    );
    const members = readFileSync(
      resolve(viewsRoot, "channels/components/channel-members-list.tsx"),
      "utf8",
    );
    const tray = readFileSync(
      resolve(viewsRoot, "channels/components/composer-attachment-tray.tsx"),
      "utf8",
    );
    const issuesHeader = readFileSync(
      resolve(viewsRoot, "issues/components/issues-header.tsx"),
      "utf8",
    );
    const myIssuesHeader = readFileSync(
      resolve(viewsRoot, "my-issues/components/my-issues-header.tsx"),
      "utf8",
    );

    expect(addPeople).toContain(
      "min-h-0 flex-1 overflow-y-auto overscroll-contain momentum-scroll pb-2",
    );
    expect(members).toContain(
      "min-h-0 space-y-2 overflow-y-auto overscroll-contain px-5 py-3 momentum-scroll",
    );
    expect(members).toContain(
      "min-h-0 overflow-y-auto overscroll-contain px-2 pb-2 momentum-scroll",
    );
    expect(tray).toContain("touch-pan-x momentum-scroll [scrollbar-width:thin]");
    expect(issuesHeader).toContain(
      "h-12 shrink-0 overflow-x-auto px-4 momentum-scroll",
    );
    expect(myIssuesHeader).toContain(
      "h-12 shrink-0 overflow-x-auto px-4 momentum-scroll",
    );
  });
});
