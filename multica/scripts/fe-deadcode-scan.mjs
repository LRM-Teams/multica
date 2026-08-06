#!/usr/bin/env node
/**
 * LRM-1040 — repeatable FE dead-code + duplication scan.
 *
 * 1) knip (knip.fe.json) against apps/web + packages/{views,ui,core}
 * 2) jscpd on packages/views (abstraction candidates; locales ignored in summary)
 *
 * Usage:
 *   pnpm fe:deadcode
 *   pnpm fe:deadcode:strict   # exit 1 if unused files remain
 */
import { spawnSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const outDir = resolve(root, "e2e/artifacts/fe-deadcode");
const strict = process.argv.includes("--strict");
mkdirSync(outDir, { recursive: true });

function run(cmd, args, opts = {}) {
  return spawnSync(cmd, args, {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    env: process.env,
    ...opts,
  });
}

function extractJson(raw) {
  const cleaned = raw
    .split("\n")
    .filter((line) => !line.startsWith("◇") && !line.includes("injected env"))
    .join("\n");
  const start = cleaned.indexOf("{");
  const end = cleaned.lastIndexOf("}");
  if (start < 0 || end < start) return null;
  return JSON.parse(cleaned.slice(start, end + 1));
}

const knip = run("pnpm", [
  "dlx",
  "knip@5",
  "--config",
  "knip.fe.json",
  "--include",
  "files,exports",
  "--reporter",
  "json",
]);
const knipRaw = `${knip.stdout || ""}\n${knip.stderr || ""}`;
if (/SyntaxError|Invalid regular expression|ERROR: Invalid input/.test(knipRaw)) {
  console.error(knipRaw.slice(0, 4000));
  process.exit(2);
}
const knipReport = extractJson(knipRaw) ?? { parseError: true, raw: knipRaw.slice(0, 4000) };
if (knipReport.parseError) {
  console.error("knip JSON parse failed");
  process.exit(2);
}

const unusedFiles = knipReport.files ?? [];
const unusedExports = knipReport.issues ?? knipReport.exports ?? [];

const jscpdOut = resolve(outDir, "jscpd");
mkdirSync(jscpdOut, { recursive: true });
run("pnpm", [
  "dlx",
  "jscpd@4",
  "packages/views",
  "--min-lines",
  "25",
  "--min-tokens",
  "100",
  "--ignore",
  "**/locales/**,**/node_modules/**,**/*.test.tsx,**/*.test.ts",
  "--reporters",
  "json",
  "--output",
  jscpdOut,
  "--silent",
]);

let dupCandidates = [];
try {
  const jscpd = JSON.parse(
    readFileSync(resolve(jscpdOut, "jscpd-report.json"), "utf8"),
  );
  const dups = jscpd.duplicates ?? [];
  dupCandidates = dups
    .map((d) => {
      const a = d.firstFile ?? {};
      const b = d.secondFile ?? {};
      return {
        lines: d.lines ?? 0,
        a: a.name ?? a.sourceId ?? "?",
        b: b.name ?? b.sourceId ?? "?",
      };
    })
    .filter((d) => d.a !== d.b)
    .sort((x, y) => y.lines - x.lines)
    .slice(0, 20);
} catch {
  dupCandidates = [];
}

const summary = {
  generatedAt: new Date().toISOString(),
  unusedFileCount: unusedFiles.length,
  unusedExportIssueCount: Array.isArray(unusedExports) ? unusedExports.length : 0,
  unusedFiles: unusedFiles.slice(0, 80),
  duplicateCandidates: dupCandidates,
  out: outDir,
};

writeFileSync(resolve(outDir, "summary.json"), JSON.stringify(summary, null, 2));
writeFileSync(resolve(outDir, "knip-raw.json"), JSON.stringify(knipReport, null, 2));
console.log(JSON.stringify(summary, null, 2));

if (strict && unusedFiles.length > 0) process.exit(1);
process.exit(0);
