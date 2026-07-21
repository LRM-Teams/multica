import { useRef } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { Drawer, DrawerContent } from "@multica/ui/components/ui/drawer";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelProjectSettingsPanel } from "./channel-project-settings-panel";

// Mirrors channels-page.tsx's `mobilePanel === "settings"` branch exactly:
// a ref attached to a node inside `DrawerContent`, handed to the panel as
// `portalContainer` so the picker's dropdown portals inside the Drawer's
// already-unlocked content instead of the default `document.body`.
function MobileSettingsDrawer({ onChange }: { onChange: (projectId: string | null) => void }) {
  const bodyRef = useRef<HTMLDivElement | null>(null);
  return (
    <Drawer open direction="bottom" onOpenChange={() => {}}>
      <DrawerContent>
        <div className="p-4" ref={bodyRef}>
          <ChannelProjectSettingsPanel
            wsId="ws-1"
            projectId={null}
            onChange={onChange}
            portalContainer={bodyRef}
          />
        </div>
      </DrawerContent>
    </Drawer>
  );
}

// #576 (task #576 mobile follow-up) — at 375px, the Group Settings mobile
// panel renders `ChannelProjectSettingsPanel` (and its `ProjectPickerButton`
// dropdown) inside a Vaul bottom-sheet Drawer (channels-page.tsx's
// `mobilePanel === "settings"` branch). Vaul's modal Drawer locks background
// interaction by setting `document.body.style.pointerEvents = "none"` and
// re-enabling `pointer-events: auto` only on its OWN `DrawerContent` DOM
// node. The picker's dropdown (`@multica/ui/components/ui/dropdown-menu`,
// a Base UI `Menu`) portals its popup to `document.body` by default — a
// SIBLING of the Drawer's portal, not a descendant of `DrawerContent` — so
// it inherits `pointer-events: none` from `<body>` and every
// `[role=menuitemradio]` option becomes unclickable. Every other
// Drawer-adjacent test in this repo mocks the Drawer (or the dropdown) away
// entirely (see channels-page-message-actions.test.tsx, dm-list.test.tsx,
// app-sidebar.test.tsx), so this exact portal-boundary interaction was never
// exercised — that's why the bug shipped. These tests render the REAL
// `Drawer`/`DrawerContent` (vaul) nested around the REAL
// `ChannelProjectSettingsPanel` (real `ProjectPickerButton` /
// `DropdownMenu`), exactly like channels-page.tsx's mobile branch, and use
// `userEvent` — which enforces a `pointer-events` check before clicking —
// so a regression here fails the test instead of silently no-oping.

const projectListFixture = vi.hoisted(() => ({
  current: [
    { id: "proj-1", title: "Test Project" },
    { id: "proj-2", title: "Other Project" },
  ] as Array<{ id: string; title: string }>,
}));

vi.mock("@multica/core/projects/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/projects/queries")>()),
  projectListOptions: () => ({
    queryKey: ["projects", "ws-1", "list"],
    queryFn: async () => ({ projects: projectListFixture.current, total: projectListFixture.current.length }),
    select: (data: { projects: typeof projectListFixture.current }) => data.projects,
  }),
}));

function renderWithProviders(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ChannelProjectSettingsPanel", () => {
  beforeEach(() => {
    projectListFixture.current = [
      { id: "proj-1", title: "Test Project" },
      { id: "proj-2", title: "Other Project" },
    ];
  });

  // Control case: the desktop docked side panel never wraps this component
  // in a Drawer (see channel-settings-side-panel.tsx). Picking an option
  // must fire onChange — this is the baseline the mobile case must match.
  it("fires onChange when a project is picked outside any Drawer (desktop baseline)", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithProviders(
      <ChannelProjectSettingsPanel wsId="ws-1" projectId={null} onChange={onChange} />,
    );

    await user.click(await screen.findByRole("button", { name: /Project/ }));
    const option = await screen.findByRole("menuitemradio", { name: "Test Project" });
    await user.click(option);

    await waitFor(() => expect(onChange).toHaveBeenCalledWith("proj-1"));
  });

  // The actual #576 regression: same component, same picker, nested inside
  // a real modal Vaul Drawer exactly like channels-page.tsx's mobile
  // `mobilePanel === "settings"` branch. Before the fix, every
  // `[role=menuitemradio]` here has computed `pointer-events: none`
  // (inherited from the Drawer's `body.style.pointerEvents = "none"` lock)
  // and `userEvent.click` throws instead of firing onChange.
  it("fires onChange when a project is picked inside the mobile Group Settings Drawer (#576)", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithProviders(<MobileSettingsDrawer onChange={onChange} />);

    await user.click(await screen.findByRole("button", { name: /Project/ }));
    const option = await screen.findByRole("menuitemradio", { name: "Test Project" });

    // The pointer-events lock, when present, makes every ancestor up to
    // <body> report "none" — assert the real bug signature directly so a
    // future regression is diagnosable from the failure message alone, not
    // just "click didn't work".
    expect(getComputedStyle(option).pointerEvents).toBe("auto");

    await user.click(option);

    await waitFor(() => expect(onChange).toHaveBeenCalledWith("proj-1"));
  });
});
