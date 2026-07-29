import eslint from "@eslint/js";
import tseslint from "typescript-eslint";
import importPlugin from "eslint-plugin-import-x";

/** @type {import("eslint").Linter.Config[]} */
export default [
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    plugins: {
      "import-x": importPlugin,
    },
    rules: {
      // Already enforced by TypeScript compiler (noUnusedLocals/noUnusedParameters)
      "@typescript-eslint/no-unused-vars": "off",
      // Allow explicit any where needed
      "@typescript-eslint/no-explicit-any": "off",
      // Prevent phantom dependencies — imports must be declared in package.json
      "import-x/no-extraneous-dependencies": ["error", {
        devDependencies: [
          "**/*.test.{ts,tsx}",
          "**/*.spec.{ts,tsx}",
          "**/test/**",
          "**/tests/**",
          // Test-support code that is not itself a test file: shared mock
          // factories, fixtures and manual mocks. These legitimately import
          // vitest, and it is correctly a devDependency — the allowlist was
          // simply missing the path, so the first shared fixture (#1390) broke
          // `views` lint for everyone. Widening the path list keeps the rule's
          // real job (no phantom PRODUCTION dependencies) intact; moving vitest
          // into `dependencies` would have "fixed" lint by shipping a test
          // runner to users.
          "**/__fixtures/**",
          "**/__fixtures__/**",
          "**/__mocks__/**",
          "**/vitest.config.*",
          "**/vite.config.*",
          "**/electron.vite.config.*",
          "**/eslint.config.*",
          "**/scripts/**",
          "**/src/main/**",
          "**/src/preload/**",
        ],
        peerDependencies: true,
      }],
    },
  },
  {
    ignores: [
      "node_modules/",
      "dist/",
      ".next/",
      "out/",
    ],
  },
];
