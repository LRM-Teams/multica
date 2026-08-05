import reactConfig from "@multica/eslint-config/react";

export default [
  ...reactConfig,
  // Plain .mjs test scripts run under `node --test` (not vitest/TS) and use
  // Node.js globals that are not in the default browser-centric env.
  {
    files: ["**/*.test.mjs"],
    languageOptions: {
      globals: {
        URL: "readonly",
      },
    },
  },
];
