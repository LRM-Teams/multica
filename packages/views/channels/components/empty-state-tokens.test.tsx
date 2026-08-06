import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  EMPTY_DESCRIPTION_CLASSNAME,
  EMPTY_MEDIA_ICON_CLASSNAME,
  EMPTY_TITLE_CLASSNAME,
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";

const here = dirname(fileURLToPath(import.meta.url));

describe("LRM-357 empty-state semantic tokens", () => {
  it("shared Empty title/description/icon stay on foreground · muted tokens (no light-gray hex)", () => {
    expect(EMPTY_TITLE_CLASSNAME).toContain("text-foreground");
    expect(EMPTY_DESCRIPTION_CLASSNAME).toContain("text-muted-foreground");
    expect(EMPTY_MEDIA_ICON_CLASSNAME).toContain("bg-muted");
    expect(EMPTY_MEDIA_ICON_CLASSNAME).toContain("text-muted-foreground");
    expect(EMPTY_TITLE_CLASSNAME).not.toMatch(/#[0-9a-fA-F]{3,8}/);
    expect(EMPTY_DESCRIPTION_CLASSNAME).not.toMatch(/#[0-9a-fA-F]{3,8}/);
    expect(EMPTY_MEDIA_ICON_CLASSNAME).not.toMatch(/#[0-9a-fA-F]{3,8}/);

    render(
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <span data-testid="empty-icon-glyph">∅</span>
          </EmptyMedia>
          <EmptyTitle>Nothing here</EmptyTitle>
          <EmptyDescription>Try another filter.</EmptyDescription>
        </EmptyHeader>
      </Empty>,
    );

    expect(screen.getByText("Nothing here").className).toContain("text-foreground");
    expect(screen.getByText("Try another filter.").className).toContain(
      "text-muted-foreground",
    );
    const icon = screen.getByTestId("empty-icon-glyph").parentElement;
    expect(icon?.className).toContain("bg-muted");
    expect(icon?.className).toContain("text-muted-foreground");
  });

  it("channel empty message slot uses foreground primary copy", () => {
    const src = readFileSync(resolve(here, "channel-message-list.tsx"), "utf8");
    expect(src).toContain('data-slot="message-list-empty"');
    expect(src).toMatch(
      /data-slot="message-list-empty"[\s\S]*?className="text-sm text-foreground"/,
    );
  });

  it("channel / sidebar / search empty sources avoid hardcoded light-gray hex", () => {
    const files = [
      resolve(here, "../../../ui/components/ui/empty.tsx"),
      resolve(here, "channel-message-list.tsx"),
      resolve(here, "channels-page.tsx"),
      resolve(here, "dm-list.tsx"),
      resolve(here, "channel-members-list.tsx"),
      resolve(here, "../../search/global-search-dialog.tsx"),
    ];
    for (const file of files) {
      const src = readFileSync(file, "utf8");
      expect(src, file).not.toMatch(/#71717a|#a1a1aa|#868686|#9ca3af|#f4f4f4/);
    }

    const channelsPage = readFileSync(resolve(here, "channels-page.tsx"), "utf8");
    expect(channelsPage).toContain("text-xl font-semibold text-foreground");
    expect(channelsPage).toContain("bg-muted text-muted-foreground");
    expect(channelsPage).toContain("bg-card");

    const dmList = readFileSync(resolve(here, "dm-list.tsx"), "utf8");
    expect(dmList).toContain('text-xs text-foreground">{t(($) => $.dm.empty)}</p>');

    const members = readFileSync(resolve(here, "channel-members-list.tsx"), "utf8");
    expect(members).toContain("text-sm text-foreground");

    const search = readFileSync(resolve(here, "../../search/global-search-dialog.tsx"), "utf8");
    expect(search).toContain("globalSearch.states.empty");
    expect(search).toContain("text-muted-foreground");
  });
});
