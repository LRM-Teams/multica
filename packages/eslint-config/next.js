import reactConfig from "./react.js";
import nextPlugin from "@next/eslint-plugin-next";

const serviceWorkerGlobals = {
  self: "readonly",
  clients: "readonly",
  caches: "readonly",
  registration: "readonly",
  skipWaiting: "readonly",
  importScripts: "readonly",
  ServiceWorkerGlobalScope: "readonly",
};

/** @type {import("eslint").Linter.Config[]} */
export default [
  ...reactConfig,
  {
    files: ["**/*.{js,jsx,ts,tsx}"],
    plugins: {
      "@next/next": nextPlugin,
    },
    rules: {
      ...nextPlugin.configs.recommended.rules,
      ...nextPlugin.configs["core-web-vitals"].rules,
    },
  },
  {
    files: ["**/sw.js", "**/*-sw.js", "**/service-worker.js", "**/*.service-worker.js"],
    languageOptions: {
      globals: serviceWorkerGlobals,
    },
  },
];
