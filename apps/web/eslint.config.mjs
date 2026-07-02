import nextConfig from "@multica/eslint-config/next";

export default [
  ...nextConfig,
  { ignores: [".next/", ".source/"] },
  {
    files: ["public/sw.js"],
    languageOptions: {
      globals: {
        self: "readonly",
      },
    },
  },
  {
    files: ["**/*.test.{ts,tsx}", "**/test/**/*.{ts,tsx}"],
    rules: {
      "react/display-name": "off",
    },
  },
];
