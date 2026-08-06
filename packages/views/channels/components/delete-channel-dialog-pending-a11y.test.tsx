import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { DeleteChannelDialog } from "./delete-channel-dialog";

/**
 * LRM-1251 — the DELETE is in flight, so `busy` is true. Before this test the
 * dialog answered that by putting a NATIVE `disabled` on every focusable node
 * it owns, which costs twice over (same root cause as LRM-1213/1236/1239/1241):
 *
 *  1. the button the user just pressed is the one that goes disabled, so focus
 *     falls out of the dialog onto BODY;
 *  2. nothing focusable is left inside an `aria-modal` container — for a
 *     destructive, irreversible request there is then no keyboard target at all.
 *
 * The unchecked gate (LRM-239) is a different thing and stays native `disabled`:
 * the user has not pressed that button yet, so no focus can be lost.
 */
function renderDialog(overrides: Partial<ComponentProps<typeof DeleteChannelDialog>> = {}) {
  const onConfirm = vi.fn();
  const onOpenChange = vi.fn();
  const view = render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <DeleteChannelDialog
        open
        channelName="multica-frank"
        onConfirm={onConfirm}
        onOpenChange={onOpenChange}
        {...overrides}
      />
    </I18nProvider>,
  );
  const rerenderWith = (next: Partial<ComponentProps<typeof DeleteChannelDialog>>) =>
    view.rerender(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
        <DeleteChannelDialog
          open
          channelName="multica-frank"
          onConfirm={onConfirm}
          onOpenChange={onOpenChange}
          {...overrides}
          {...next}
        />
      </I18nProvider>,
    );
  return { onConfirm, onOpenChange, rerenderWith };
}

const confirmButton = () => screen.getByRole("button", { name: "Delete channel" });
const cancelButton = () => screen.getByRole("button", { name: "Cancel" });

/** Focusable nodes the dialog itself owns — a natively disabled control is not one. */
function focusableInDialog(): HTMLElement[] {
  const dialog = screen.getByRole("alertdialog");
  return Array.from(
    dialog.querySelectorAll<HTMLElement>("button, [href], input, select, textarea, [tabindex]"),
  ).filter((el) => {
    if (el.hasAttribute("disabled")) return false;
    if (el.getAttribute("aria-hidden") === "true") return false;
    const tabIndex = el.getAttribute("tabindex");
    return tabIndex === null || Number(tabIndex) >= 0;
  });
}

describe("DeleteChannelDialog pending a11y (LRM-1251)", () => {
  it("keeps the pressed Delete focused and focusable while the delete is in flight", async () => {
    const user = userEvent.setup();
    const { onConfirm, rerenderWith } = renderDialog({ pending: false });

    await user.click(screen.getByRole("checkbox"));
    await user.click(confirmButton());
    expect(onConfirm).toHaveBeenCalledTimes(1);

    // The parent flips `deleteChannel.isPending` (channels-page.tsx).
    rerenderWith({ pending: true });

    const confirm = confirmButton();
    expect(confirm).not.toBeDisabled();
    expect(confirm.getAttribute("aria-disabled")).toBe("true");
    expect(document.activeElement).toBe(confirm);
  });

  it("does not fire a second onConfirm from the aria-disabled Delete", async () => {
    const user = userEvent.setup();
    const { onConfirm, rerenderWith } = renderDialog({ pending: false });

    await user.click(screen.getByRole("checkbox"));
    await user.click(confirmButton());
    rerenderWith({ pending: true });

    await user.click(confirmButton());
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("keeps Cancel focusable but inert while pending", async () => {
    const user = userEvent.setup();
    const { onConfirm, onOpenChange } = renderDialog({ pending: true });

    const cancel = cancelButton();
    expect(cancel).not.toBeDisabled();
    expect(cancel.getAttribute("aria-disabled")).toBe("true");

    cancel.focus();
    expect(document.activeElement).toBe(cancel);

    await user.click(cancel);
    expect(onConfirm).not.toHaveBeenCalled();
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("leaves at least one focusable control inside the modal while pending", () => {
    renderDialog({ pending: true });
    expect(focusableInDialog().length).toBeGreaterThanOrEqual(1);
  });

  it("still uses native disabled for the unchecked gate (LRM-239 unchanged)", () => {
    renderDialog({ pending: false });
    const confirm = confirmButton();
    expect(confirm).toBeDisabled();
    expect(confirm.getAttribute("aria-disabled")).toBeNull();
  });
});
