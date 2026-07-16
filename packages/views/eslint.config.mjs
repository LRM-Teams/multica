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
  // back. A rule that only lives in a doc is a rule that gets re-broken: prefer
  // making the wrong thing impossible over writing it down (Iris's §0 standard).
  {
    files: [
      "common/**/*.tsx",
      "issues/components/issue-ref-link.tsx",
      "issues/components/issue-mention-card.tsx",
    ],
    ignores: ["**/*.test.tsx"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "../issues/components/issue-chip",
              importNames: ["IssueChip"],
              message:
                "Reading surfaces must render IssueRefLink, not IssueChip — the chip is the editor's operating-state form (#520). useResolvedIssue/isIssueUuid from this module are fine.",
            },
            {
              name: "./issue-chip",
              importNames: ["IssueChip"],
              message:
                "Reading surfaces must render IssueRefLink, not IssueChip — the chip is the editor's operating-state form (#520). useResolvedIssue/isIssueUuid from this module are fine.",
            },
          ],
        },
      ],
    },
  },
];
