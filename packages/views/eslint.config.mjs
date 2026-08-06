import reactConfig from "@multica/eslint-config/react";
import i18next from "eslint-plugin-i18next";

// Global i18n protection. Every JSX text node in this package must pass
// through useT() — raw strings become a build error. Scope of
// `mode: "jsx-text-only"`: flags raw strings inside JSX children only;
// attribute values and plain TS literals are allowed through.

export default [
  ...reactConfig,
  // Node tooling under scripts/ (Vitest reporters, baseline recorders) — not browser.
  {
    files: ["scripts/**/*.{mjs,js,cjs}"],
    languageOptions: {
      globals: {
        console: "readonly",
        process: "readonly",
        URL: "readonly",
        Buffer: "readonly",
        __dirname: "readonly",
        __filename: "readonly",
        module: "readonly",
        require: "readonly",
        exports: "readonly",
      },
    },
  },
  {
    files: ["**/*.tsx"],
    ignores: ["**/*.test.tsx", "test/**"],
    plugins: { i18next },
    rules: {
      "i18next/no-literal-string": [
        "error",
        { mode: "jsx-text-only" },
      ],
    },
  },
  // Reading surfaces render an issue reference ONE way: IssueRefLink (zero
  // decoration + peek). The chip belongs to the EDITOR, where you operate on the
  // reference and its box is a functional signal that it is one atomic token — so
  // this is an import boundary, NOT a global ban (Barry's precise form).
  //
  // #520 removed the last chip from a message body; this stops the next one coming
  // back. Prefer making the wrong thing impossible over writing it down (Iris's §0).
  //
  // ⚠️ DENY BY DEFAULT, AND THAT DIRECTION IS THE WHOLE POINT (Iris's catch).
  // The first cut allow-listed the reading surfaces — which silently fails the day
  // someone adds a new one: not on the list, so not linted, so unprotected, and
  // nothing says so. That is the same disease as a fixture whose ordering makes it
  // untestable — the rule looks green while covering nothing, except this version
  // starts covering nothing on a FUTURE day nobody witnesses. You can see a lint go
  // red; you cannot see it "not go red for a file that doesn't exist yet".
  //
  // The asymmetry decides it: the exceptions below (the editor, the chip's own
  // definition, the barrel that re-exports it) are FINITE and KNOWN. Reading
  // surfaces are UNBOUNDED and grow. Allow-listing the growing side guarantees
  // drift; allow-listing the fixed side is stable. So a new reading surface is
  // protected by default, and opening a hole is an explicit edit that shows up in
  // review.
  {
    files: ["**/*.ts", "**/*.tsx"],
    ignores: [
      // Operating state: you act on the reference here, so the chip is correct.
      "editor/**",
      // Defines the chip — not a consumer.
      "issues/components/issue-chip.tsx",
      // NOTE: the barrel is NOT listed, because it no longer re-exports IssueChip.
      // Wren found that route bypassed this ban entirely (`{ IssueChip } from
      // "../components"` never matches `**/issue-chip`). The fix was to delete the
      // second entrance, not to add it here — one controlled entrance is structural,
      // a longer ban list is another promise waiting to be outgrown.
      "**/*.test.ts",
      "**/*.test.tsx",
    ],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          // A pattern, not a path list: it must hold at any relative depth, from a
          // file that does not exist yet.
          patterns: [
            {
              group: ["**/issue-chip"],
              importNames: ["IssueChip"],
              message:
                "Reading surfaces must render IssueRefLink, not IssueChip — the chip is the editor's operating-state form (#520). useResolvedIssue/isIssueUuid from this module are fine. If you genuinely need the chip outside the editor, add the file to the ignores in eslint.config.mjs so the exception is reviewed rather than assumed.",
            },
          ],
        },
      ],
    },
  },
  // Dev-only smoke harnesses under __smoke__/ (standalone Vite screenshot
  // demos, .cjs shot scripts, their vite.config). These are not shipped
  // browser code: they render hardcoded fixture strings onto a dev-only canvas
  // for screenshot artifacts (LRM-1475 AC3) and import vite/plugin deps that
  // live in the app, not in the @multica/views runtime. Grant node globals for
  // the .cjs/vite tooling and exempt them from the JSX-i18n, phantom-dep and
  // require-import rules — same reasoning as the scripts/** carve-out above.
  // This block is intentionally LAST: flat config is last-match-wins, so later
  // blocks override the JSX-i18n rule that would otherwise flag the demo .tsx.
  {
    files: ["**/__smoke__/**/*.{mjs,js,cjs,ts,tsx}"],
    languageOptions: {
      globals: {
        console: "readonly",
        process: "readonly",
        URL: "readonly",
        __dirname: "readonly",
        __filename: "readonly",
        require: "readonly",
        module: "readonly",
        global: "readonly",
        setTimeout: "readonly",
      },
    },
    rules: {
      "i18next/no-literal-string": "off",
      "import-x/no-extraneous-dependencies": "off",
      "@typescript-eslint/no-require-imports": "off",
    },
  },
];
