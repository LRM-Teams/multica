import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, it, expect } from "vitest";

const viewsRoot = join(__dirname, "..");

describe("Members Directory surface cutover (ADR 0013)", () => {
  it("settings page no longer mounts MembersTab", () => {
    const src = readFileSync(
      join(viewsRoot, "settings/components/settings-page.tsx"),
      "utf8",
    );
    expect(src).not.toMatch(/MembersTab/);
    expect(src).not.toMatch(/value="members"/);
    expect(src).toMatch(/members: "general"/);
  });

  it("exports MembersDirectoryPage", () => {
    const src = readFileSync(join(viewsRoot, "members/index.ts"), "utf8");
    expect(src).toMatch(/MembersDirectoryPage/);
  });

  it("sidebar navigates via members path key", () => {
    const src = readFileSync(join(viewsRoot, "layout/app-sidebar.tsx"), "utf8");
    expect(src).toMatch(/key: "members"/);
    expect(src).toMatch(/labelKey: "members"/);
    expect(src).not.toMatch(/key: "agents"/);
  });
});
