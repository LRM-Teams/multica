// @vitest-environment jsdom
// This file asserts getComputedStyle() cascade behavior (pointer-events under
// Vaul's body lock), which happy-dom does not model — keep it on jsdom.
import { type ReactNode, useState } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Drawer, DrawerContent } from "@multica/ui/components/ui/drawer";
import { ChannelAddPeopleDialog, type InviteCandidate } from "./channel-add-people-dialog";

// LRM-1195 — Add people opens from the mobile Members surface, which is hosted
// in a modal Vaul Drawer (channels-page.tsx). Vaul locks background interaction
// with `body.style.pointerEvents = "none"` and only re-enables `pointer-events:
// auto` on its own DrawerContent. ChannelAddPeopleDialog portals to
// document.body (sibling of that content), so without Dialog's explicit unlock
// the search input inherits the lock and cannot focus — Frank's「搜框点不进」.
// channels-page also dismisses the drawer before opening; this suite keeps the
// drawer open to prove Dialog stays interactive during the exit animation.
// Uses userEvent (enforces pointer-events before click).

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: Record<string, unknown>) => string) => {
      const resources = {
        members: {
          add_people_title: "Add people to {{name}}",
          add_people_subtitle: "Invite teammates",
          search: "Search",
          suggestions: "Suggestions",
          no_results: "No results",
          no_candidates: "No candidates",
          candidates_error: "Couldn't load people to invite",
          candidates_retry: "Retry",
          candidates_slow: "Still loading…",
          candidates_timeout: "Loading is taking too long",
          remove_aria: "Remove",
          cancel: "Cancel",
          add: "Add",
        },
        profile_popover: {
          role: { agent: "Agent" },
        },
      };
      const raw = selector(resources as never);
      return typeof raw === "string" ? raw.replace("{{name}}", "general") : String(raw);
    },
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

vi.mock("../../common/actor-identity-row", () => ({
  ActorIdentityRow: ({ displayName }: { displayName: string }) => (
    <span>{displayName}</span>
  ),
}));

const candidates: InviteCandidate[] = [
  {
    key: "user:u1",
    type: "user",
    id: "u1",
    presentation: {
      displayName: "Alice",
      handle: "alice",
      handleLabel: "@alice",
      showHandleLabel: true,
    },
  },
];

function AddPeopleOverDrawer({
  onQueryChange,
}: {
  onQueryChange: (q: string) => void;
}) {
  const [query, setQuery] = useState("");
  return (
    <>
      <Drawer open direction="bottom" onOpenChange={() => {}}>
        <DrawerContent>
          <p>Members roster</p>
        </DrawerContent>
      </Drawer>
      <ChannelAddPeopleDialog
        open
        onOpenChange={() => {}}
        channelName="general"
        candidates={candidates}
        allCandidates={candidates}
        query={query}
        onQueryChange={(q) => {
          setQuery(q);
          onQueryChange(q);
        }}
        selected={new Set()}
        onToggle={() => {}}
        onClearOne={() => {}}
        onSubmit={() => {}}
      />
    </>
  );
}

function renderUi(ui: ReactNode) {
  return render(ui);
}

describe("ChannelAddPeopleDialog over mobile Drawer (LRM-1195)", () => {
  it("keeps the search input pointer-events auto while a modal Drawer is open", async () => {
    renderUi(<AddPeopleOverDrawer onQueryChange={() => {}} />);

    const input = await screen.findByPlaceholderText("Search");
    expect(getComputedStyle(input).pointerEvents).toBe("auto");

    let node: HTMLElement | null = input;
    let sawExplicitAuto = false;
    while (node && node !== document.body) {
      if (getComputedStyle(node).pointerEvents === "auto") {
        sawExplicitAuto = true;
        break;
      }
      node = node.parentElement;
    }
    expect(sawExplicitAuto).toBe(true);
  });

});
