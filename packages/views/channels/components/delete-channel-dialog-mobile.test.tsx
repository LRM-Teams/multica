import { type ReactNode, useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { Drawer, DrawerContent } from "@multica/ui/components/ui/drawer";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { DeleteChannelDialog } from "./delete-channel-dialog";

// LRM-265 — mobile permanent-delete confirm opens while the channel details
// "…" surface is a modal Vaul Drawer (channels-page.tsx). Vaul locks
// background interaction with `body.style.pointerEvents = "none"` and only
// re-enables `pointer-events: auto` on its own `DrawerContent`. AlertDialog
// portals to `document.body` (a sibling of that content), so without an
// explicit unlock the confirm checkbox / actions inherit the lock and
// become unclickable — Frank's "无法点击和选中". channels-page also dismisses
// the drawer before opening the dialog, but the dialog must stay interactive
// even if both are briefly open (drawer exit animation). These tests render
// the REAL Drawer + REAL DeleteChannelDialog and use `userEvent`, which
// enforces pointer-events before clicking.

function MobileDeleteOverDrawer({
  onConfirm,
}: {
  onConfirm: () => void;
}) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <Drawer open direction="bottom" onOpenChange={() => {}}>
        <DrawerContent>
          <p>Settings danger zone</p>
        </DrawerContent>
      </Drawer>
      <DeleteChannelDialog
        open={open}
        channelName="multica-frank"
        onConfirm={onConfirm}
        onOpenChange={setOpen}
      />
    </>
  );
}

function renderWithProviders(ui: ReactNode) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      {ui}
    </I18nProvider>,
  );
}

describe("DeleteChannelDialog over mobile Drawer (LRM-265)", () => {
  it("keeps the confirm checkbox pointer-events auto while a modal Drawer is open", async () => {
    renderWithProviders(<MobileDeleteOverDrawer onConfirm={() => {}} />);

    const checkbox = await screen.findByRole("checkbox");
    expect(getComputedStyle(checkbox).pointerEvents).toBe("auto");

    // Walk ancestors: body is locked by Vaul, but the AlertDialog popup must
    // explicitly unlock so the checkbox remains interactive.
    let node: HTMLElement | null = checkbox;
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

  it("lets the user check and confirm delete while a modal Drawer is open", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    renderWithProviders(<MobileDeleteOverDrawer onConfirm={onConfirm} />);

    const confirm = await screen.findByRole("button", { name: "Delete channel" });
    expect(confirm).toBeDisabled();

    await user.click(await screen.findByRole("checkbox"));
    expect(confirm).toBeEnabled();

    await user.click(confirm);
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
  });

  it("cancel closes without confirming while a modal Drawer is open", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    renderWithProviders(<MobileDeleteOverDrawer onConfirm={onConfirm} />);

    await user.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(onConfirm).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Delete channel" })).not.toBeInTheDocument(),
    );
  });
});
