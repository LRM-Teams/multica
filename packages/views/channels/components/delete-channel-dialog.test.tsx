// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ComponentProps } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { DeleteChannelDialog } from "./delete-channel-dialog";

const TEST_RESOURCES = {
  en: { common: enCommon, channels: enChannels },
};

function renderDialog(
  props: Partial<ComponentProps<typeof DeleteChannelDialog>> = {},
) {
  const onConfirm = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <DeleteChannelDialog
        open
        channelName="multica-frank"
        onConfirm={onConfirm}
        onOpenChange={onOpenChange}
        {...props}
      />
    </I18nProvider>,
  );
  return { onConfirm, onOpenChange };
}

describe("DeleteChannelDialog (LRM-237)", () => {
  it("disables Delete until the permanent-delete checkbox is checked", () => {
    const { onConfirm } = renderDialog();

    expect(screen.getByText(/permanently delete "multica-frank"/i)).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();

    const confirmBtn = screen.getByRole("button", { name: "Delete Channel" });
    expect(confirmBtn).toBeDisabled();

    fireEvent.click(confirmBtn);
    expect(onConfirm).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("checkbox"));
    expect(screen.getByRole("checkbox").getAttribute("aria-checked")).toBe("true");
    expect(confirmBtn).not.toBeDisabled();

    fireEvent.click(confirmBtn);
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("resets the checkbox when the dialog re-opens", () => {
    const { rerender } = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DeleteChannelDialog
          open
          channelName="multica-frank"
          onConfirm={() => {}}
          onOpenChange={() => {}}
        />
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("checkbox"));
    expect(screen.getByRole("checkbox").getAttribute("aria-checked")).toBe("true");

    rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DeleteChannelDialog
          open={false}
          channelName="multica-frank"
          onConfirm={() => {}}
          onOpenChange={() => {}}
        />
      </I18nProvider>,
    );
    rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DeleteChannelDialog
          open
          channelName="multica-frank"
          onConfirm={() => {}}
          onOpenChange={() => {}}
        />
      </I18nProvider>,
    );

    expect(screen.getByRole("checkbox").getAttribute("aria-checked")).toBe("false");
    expect(screen.getByRole("button", { name: "Delete Channel" })).toBeDisabled();
  });
});
