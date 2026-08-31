import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const source = fs.readFileSync(
  path.join(__dirname, "research-session-page.tsx"),
  "utf8",
);

describe("research chat rail latest-message scroll", () => {
  it("pins auto-scroll to the visible D5 chat rail, not the unused drawer flag", () => {
    expect(source).not.toContain("s.chatDrawerOpen");
    expect(source).toContain(
      'useAutoScroll(chatScrollRef, d5RailOpen && d5RailMode === "chat")',
    );
  });

  it("keeps the chat feed as the overflowing column so latest messages can sit at the bottom", () => {
    expect(source).toContain('data-testid="research-chat-rail-column"');
    expect(source).toContain(
      'className="min-h-0 flex-1 space-y-2.5 overflow-y-auto p-3"',
    );
  });
});
