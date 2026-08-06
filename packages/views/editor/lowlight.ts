/**
 * LRM-1264 — one shared lowlight instance for editor + readonly surfaces.
 * Creating `createLowlight(common)` per module duplicated the language pack
 * on the heap without changing highlight behaviour.
 *
 * R2: register a curated chat/code set instead of highlight.js `common`
 * (~36 grammars). Unknown fences still render as plain code; visual for
 * registered languages is unchanged.
 */
import { createLowlight } from "lowlight";
import bash from "highlight.js/lib/languages/bash";
import css from "highlight.js/lib/languages/css";
import go from "highlight.js/lib/languages/go";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import plaintext from "highlight.js/lib/languages/plaintext";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

export const sharedLowlight = createLowlight({
  bash,
  sh: bash,
  shell: bash,
  css,
  go,
  java,
  javascript,
  js: javascript,
  json,
  markdown,
  md: markdown,
  plaintext,
  text: plaintext,
  python,
  py: python,
  rust,
  sql,
  typescript,
  ts: typescript,
  tsx: typescript,
  xml,
  html: xml,
  yaml,
  yml: yaml,
});
