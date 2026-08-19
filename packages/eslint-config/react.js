import baseConfig from "./base.js";
import reactPlugin from "eslint-plugin-react";
import reactHooksPlugin from "eslint-plugin-react-hooks";

/** @type {import("eslint").Linter.Config[]} */
export default [
  ...baseConfig,
  // React rules (JSX only)
  {
    files: ["**/*.{jsx,tsx}"],
    plugins: { react: reactPlugin },
    rules: {
      ...reactPlugin.configs.recommended.rules,
      ...reactPlugin.configs["jsx-runtime"].rules,
      "react/prop-types": "off",
      "react/no-unknown-property": "off",
    },
    settings: {
      react: { version: "detect" },
    },
  },
  // React Hooks rules apply to .ts files too — hooks (useEffect, useCallback,
  // useMemo) can live in plain .ts modules and we want exhaustive-deps to
  // run + inline disable comments to resolve.
  {
    files: ["**/*.{ts,tsx,js,jsx}"],
    plugins: { "react-hooks": reactHooksPlugin },
    rules: {
      ...reactHooksPlugin.configs["recommended-latest"].rules,
    },
  },
  // Native `title` attribute ban. Browser-native `title` tooltips are
  // inconsistent, not accessible (not reliably read by screen readers / keyboard),
  // and visually lag styled tooltips — the frontend moved native titles to the
  // @multica/ui Tooltip component (see #3618, 25 places). This rule prevents
  // regressions: flag the `title` ATTRIBUTE on host (lowercase) JSX elements.
  // Component `title` props (<Tooltip title=...>) and SVG <title> accessible
  // names are intentionally NOT matched. Warning (not error) per product choice:
  // surface in lint/CI output without blocking merges.
  //
  // Implemented as a dedicated rule (not no-restricted-syntax) because #835 below
  // already uses no-restricted-syntax for showErrorToast; flat-config later blocks
  // would just clobber that single-rule selector.
  {
    files: ["**/*.{jsx,tsx}"],
    plugins: {
      multica: {
        rules: {
          "no-native-title": {
            meta: {
              type: "suggestion",
              docs: { description: "Ban native title attribute on host JSX elements" },
              schema: [],
            },
            create(context) {
              return {
                JSXAttribute(node) {
                  if (
                    !node.name ||
                    node.name.type !== "JSXIdentifier" ||
                    node.name.name !== "title"
                  ) {
                    return;
                  }
                  const opening = node.parent;
                  if (
                    !opening ||
                    opening.type !== "JSXOpeningElement" ||
                    !opening.name ||
                    opening.name.type !== "JSXIdentifier" ||
                    !/^[a-z]/.test(opening.name.name)
                  ) {
                    return;
                  }
                  // <iframe title> is an a11y accessible name describing the
                  // embedded document — NOT a tooltip — so it must stay as a
                  // native title and is exempt from this ban.
                  if (opening.name.name === "iframe") {
                    return;
                  }
                  context.report({
                    node,
                    message:
                      "Native `title` attribute: use the Tooltip component from @multica/ui (base-ui) instead, and keep any accessible name via aria-label/aria-labelledby. Component title props, iframe a11y titles, and SVG <title> are exempt.",
                  });
                },
              };
            },
          },
        },
      },
    },
    rules: {
      "multica/no-native-title": "warn",
    },
  },
  // #835 — a failure is announced in exactly one place: showErrorToast.
  //
  // sonner's defaults were never chosen by us: TOAST_LIFETIME is 4s and only 3
  // toasts stay visible, so every raw `toast.error` auto-dismissed after four
  // seconds and a fourth toast silently evicted an unresolved one. That cannot
  // be fixed at `<Toaster>` — `toastOptions` is a single global config with no
  // per-type override, so pinning errors there pins every success toast too.
  // One shared call site is the only place the distinction can exist, which
  // means the ban has to hold at the CALL, not at the import: `toast` itself is
  // legitimate for success/info/warning (114 such calls today).
  //
  // Deny by default, allow-list only the finite side (same reasoning as the
  // IssueChip boundary in views): the helper's own definition and tests are
  // known and fixed; the surfaces that report failures are unbounded and grow.
  // A new one is protected without anyone remembering to add it.
  {
    files: ["**/*.{ts,tsx,js,jsx}"],
    ignores: [
      // Defines the helper — it is the one place allowed to call sonner's error.
      "**/lib/error-toast.ts",
      "**/*.test.ts",
      "**/*.test.tsx",
    ],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "CallExpression[callee.object.name='toast'][callee.property.name='error']",
          message:
            "Call showErrorToast(message, { description }) from @multica/ui/lib/error-toast instead of toast.error — a raw sonner error toast auto-dismisses in 4s and can be evicted by later toasts, so an unresolved failure disappears on its own (#835). toast.success/info/warning are unaffected. And remember the toast is the ANNOUNCEMENT, not the record: the failing surface still owes a durable state that survives dismissal.",
        },
      ],
    },
  },
];
