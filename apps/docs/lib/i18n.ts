import { defineI18n } from "fumadocs-core/i18n";

// English is the default and Chinese is available under /zh/. The
// hideLocale: 'default-locale' setting keeps English URLs prefix-free
// (`/docs/`) while translated locales live under `/docs/<lang>/...`.
// parser: 'dot' picks up `page.zh.mdx` and `meta.<lang>.json`.
export const i18n = defineI18n({
  languages: ["en", "zh"],
  defaultLanguage: "en",
  hideLocale: "default-locale",
  parser: "dot",
});

export type Lang = (typeof i18n.languages)[number];
