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
