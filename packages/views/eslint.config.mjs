import reactConfig from "@multica/eslint-config/react";
import i18next from "eslint-plugin-i18next";

// Global i18n protection. Every JSX text node in this package must pass
// through useT() — raw strings become a build error. Scope of
// `mode: "jsx-text-only"`: flags raw strings inside JSX children only;
// attribute values and plain TS literals are allowed through.

export default [
  ...reactConfig,
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
      // Defines and re-exports the chip — not a consumer.
      "issues/components/issue-chip.tsx",
      "issues/components/index.ts",
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
];
