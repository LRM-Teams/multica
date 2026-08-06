// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * LRM-491 — channel composer empty-state copy must stay Slack-short (one line
 * with the channel name), never the old two-line @agent tutorial string.
 */
describe("channel composer placeholder locales (LRM-491)", () => {
  const here = dirname(fileURLToPath(import.meta.url));
  const localesRoot = resolve(here, "../../locales");

  it("uses Message #{{name}} / 发消息给 #{{name}} style across locales", () => {
    const en = JSON.parse(
      readFileSync(resolve(localesRoot, "en/channels.json"), "utf8"),
    ) as { composer: { placeholder: string } };
    const zh = JSON.parse(
      readFileSync(resolve(localesRoot, "zh-Hans/channels.json"), "utf8"),
    ) as { composer: { placeholder: string } };
    const ja = JSON.parse(
      readFileSync(resolve(localesRoot, "ja/channels.json"), "utf8"),
    ) as { composer: { placeholder: string } };
    const ko = JSON.parse(
      readFileSync(resolve(localesRoot, "ko/channels.json"), "utf8"),
    ) as { composer: { placeholder: string } };

    expect(en.composer.placeholder).toBe("Message #{{name}}");
    expect(zh.composer.placeholder).toBe("发消息给 #{{name}}");
    expect(ja.composer.placeholder).toContain("{{name}}");
    expect(ko.composer.placeholder).toContain("{{name}}");

    for (const placeholder of [
      en.composer.placeholder,
      zh.composer.placeholder,
      ja.composer.placeholder,
      ko.composer.placeholder,
    ]) {
      expect(placeholder).not.toMatch(/Describe what you need/i);
      expect(placeholder).not.toMatch(/@mention an agent/i);
      expect(placeholder).not.toMatch(/描述你需要什么/);
      expect(placeholder.length).toBeLessThan(40);
    }
  });

  it("channels-page interpolates the active channel name into the placeholder", () => {
    const pageSrc = readFileSync(resolve(here, "channels-page.tsx"), "utf8");
    expect(pageSrc).toMatch(/composer\.placeholder/);
    expect(pageSrc).toMatch(/name:\s*active\.name/);
    expect(pageSrc).toMatch(/text-\[15px\]/);
  });
});
